# Circa — Design 07: Remote backup — CDC-style delta export into an Iceberg lake

> **Built as of v0.7.0** (`internal/backup`) — see [RELEASE/v0.7.0.md](../RELEASE/v0.7.0.md) for what actually shipped vs. this design's original sketch. The one factual correction worth flagging inline: §7.4 originally assumed `apache/iceberg-go` had no write support yet and recommended a community fork as the more directly usable v1 writer. By implementation time it did (`Table.AppendTable`, confirmed by reading the real v0.6.0 source, not assumed) — Circa uses the official module directly, not the fork §7.4 originally pointed to.

Simpler than a Thanos-style block/sidecar/store-gateway split: treat the local RRD store as a source of changes, periodically ship *only the new rows since last export* into an Iceberg table, and get federation almost for free because Iceberg is an open, SQL-queryable format — no bespoke querier component needed.

## 7.1 Why this is simpler, and what it gives up

- No segment/block lifecycle to manage (no "closed segment" concept, no meta.json, no block compaction) — just a watermark and an append.
- No custom query-fan-out protocol to design — once data lands in Iceberg, DuckDB, Trino, Spark, or Athena can already query across every node's data with plain SQL, joined/filtered by a `node_id` column.
- Trade-off: this is an **eventually-consistent, batch-oriented** backup (export every N minutes), not a live queryable replica the moment data is collected — fine for backup/history/federation, not a substitute for the local store's low-latency dashboard queries.

## 7.2 CDC mechanism

- The local RRD store already produces exactly the signal CDC needs: a sequence of `(node_id, metric_name, labels, timestamp, value, anomaly_bit)` rows written in order. No WAL-tailing or trigger machinery required — just track a **watermark** (last exported timestamp, per metric or per shard) in a small local state file.
- On each run, read all rows written since the watermark, batch them into an Arrow record batch, write as Parquet, and append to the Iceberg table as a new snapshot. Advance the watermark only after the Iceberg commit succeeds — if the run crashes mid-way, the next run just re-reads from the old watermark (safe to re-export the same window; dedupe at query time on `(node_id, metric_name, labels, timestamp)` if exact-once matters, or use upsert mode — see §7.4).
- Export tier-1 (already downsampled) data, not raw tier-0 — lower churn, smaller batches, and it's the data worth keeping long-term anyway.
- This is CDC in spirit (incremental, watermark-driven, append-only) without needing an actual CDC connector (Debezium etc.) — the local store already gives us ordered, timestamped writes for free.

## 7.3 Two backup modes: push and pull

Both are worth supporting since they suit different network topologies:

- **Push mode** (`backup.mode: push`): each Circa instance runs its own scheduler (Go ticker, or `robfig/cron` for calendar schedules) and pushes its own delta directly to the Iceberg catalog/S3 endpoint. Simplest to reason about, but means **every node needs outbound network access and Iceberg/S3 credentials** — a real problem in segmented or restricted networks, and it means credentials are distributed across the whole fleet rather than held centrally.
- **Pull mode** (`backup.mode: pull`): each node instead exposes a small `/api/v1/backup_range` HTTP endpoint (reusing the same query engine as `/api/v1/query_range`, just scoped to "give me everything after watermark X"). A separate, centrally-run backup agent (could be a Circa binary running in a different role, or a small standalone tool) polls each node on its own schedule, pulls the delta, and performs the Iceberg write itself. This means **only the central backup agent needs Iceberg/S3 credentials and outbound network access** — individual nodes need only be reachable *inbound* from the backup agent, which is usually the easier direction to arrange in a locked-down network, and keeps credentials in one place instead of scattered across every node.
- Recommendation: support both from the same underlying mechanism (watermark + delta read), since the only real difference is *which side* initiates the Iceberg write. Default to pull mode for anything beyond a handful of nodes, given the credential-centralization benefit; push mode is simplest for a single standalone instance.

## 7.4 Iceberg write path in Go

- **Built with the official `apache/iceberg-go` module directly** (v0.6.0 at implementation time) — `Table.AppendTable(ctx, arrowTable, batchSize, snapshotProps)` appends an in-memory Arrow table as one new Iceberg snapshot, confirmed against the real module source and verified end to end against a live REST catalog + MinIO (not assumed from the README alone). This design's original draft assumed no write support existed yet and pointed at a community fork (`BrobridgeOrg/go-iceberg`) as the more usable v1 option — that fork was never needed.
- Catalog: a REST catalog (self-hosted, or Polaris/Nessie/Tabular) in front of S3-compatible storage, loaded via `catalog.Load(ctx, "rest", props)` — deployable independently of Circa itself; Circa (or the central backup agent, in pull mode) is just a writer against it.
- Table schema, as built (`internal/backup.icebergSchema`) — two deliberate departures from this section's original sketch:
  - `node_id`, `hostname`, `metric_name`, `labels_json`, `ts`, `value`, `anomaly_bit`. No `env`/`region` columns — nothing in Circa's config populates those today, and speculative unused columns aren't worth the schema complexity; add them if a real need shows up.
  - `labels` travels as a JSON string column (`labels_json`), not a native Iceberg map/struct column — simpler to build with Arrow's `RecordBuilder` API and just as queryable via DuckDB/Trino's JSON functions (confirmed: `json_extract(labels_json, '$.cpu')` works against the real written table), without depending on iceberg-go's map-type write support.
  - Partitioned by `(day(ts), node_id)` via `iceberg.NewPartitionSpec` + `DayTransform`/`IdentityTransform`, so both time-range pruning and per-node queries stay cheap, exactly as originally planned.
  - `anomaly_bit` is always `false` in practice — §7.2 exports tier-1 (rollup) data, and rollups have no single anomaly verdict of their own (see [ARCHITECTURE.md](../ARCHITECTURE.md)/`internal/storage/anomalybit.go`). The column stays in the schema per this section's original plan in case a future raw-tier export path populates it meaningfully.
- Credentials: `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` env vars (the AWS SDK's own default credential chain, not a Circa-specific scheme) plus an optional `CIRCA_BACKUP_CATALOG_TOKEN` for a REST catalog's own bearer auth, separate from S3 — see §7.6 and `internal/backup.NewIcebergWriter`'s doc comment.

## 7.5 Federation search becomes a query, not a component

Because every node's data ends up in the same catalog/table (or same-namespace per-node tables) regardless of whether it got there via push or pull, "federation" stops being a distinct system to build:

- Cross-node queries are just `SELECT ... WHERE node_id IN (...) AND ts BETWEEN ...` against the Iceberg table, run from whatever engine the user already has (DuckDB locally, or Trino/Spark for larger fleets).
- No live fan-out protocol, no dedup-at-merge-time logic, no separate stateless querier binary to build and secure — the query engine and the table format already solve that.
- The one thing worth keeping: still expose `/api/v1/query_range` locally for the live dashboard ([05](05_design_ui.md)), since Iceberg queries are batch-latency (minutes-old data at best) and shouldn't be on the path for the real-time UI. Local store = live view; Iceberg = historical/cross-node view.

## 7.6 Practical notes

- Credentials for the catalog/S3 endpoint: `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` env vars (see §7.4), consistent with keeping the binary itself config-light — never a config.yaml field. In pull mode, only the central backup agent needs these, not every node.
- Retry/backlog: if the catalog or S3 endpoint is unreachable, the export job simply skips this run and retries next cycle; the watermark doesn't move, so nothing is lost — local retention just needs to comfortably outlast a plausible outage window.
- Compaction/expiry of old Iceberg snapshots is a lake-maintenance concern (Iceberg's own `expire-snapshots`/`clean-orphan-files` operations), not something Circa needs to reinvent — these are standard Iceberg CLI operations designed to be run safely from cron jobs.
- Feature-flagged (`features.backup`), default off.
