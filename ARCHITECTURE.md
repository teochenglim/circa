# Architecture

How the code is organized and how an ingestion event flows through it. For *what* the system does and *why*, see [DESIGN.md](DESIGN.md); this file is about *how* it's built.

> **Status: v0.1.0 shipped.** `cmd/circa`, `internal/config`, `internal/ingest` (+ `scrape`), `internal/storage`, `internal/query`, and `internal/httpapi` exist and run end to end — see [RELEASE/v0.1.0.md](RELEASE/v0.1.0.md). Everything else in the diagram below (`influx`, `remotewrite`, `alert`, `anomaly`, `backup`, `auth`, `web/`) is still the *target* layout, not built yet; check [RELEASE.md](RELEASE.md) → `RELEASE/` for which milestone lands each one.

## Layering

```
cmd/circa/main.go        — [BUILT] wires everything together, owns the process lifecycle
        │
internal/httpapi          — [BUILT, partial] HTTP handlers + router
                             (query_range, healthz, readyz built; metrics, write,
                             write (influx), /, status still planned)
        │
   ┌────┴──────────┬───────────────┐
internal/ingest/    internal/ingest/  internal/ingest/
scrape              influx            remotewrite
[BUILT]             [planned, v0.3.0] [planned, v0.3.0]
(pull: expfmt       (InfluxDB line    (protobuf+Snappy
against configured  protocol on       receive, and send
targets, always on) /write, flagged)  to upstream, flagged)
        │                 │                 │
        └────────┬────────┴────────┬────────┘
             internal/ingest            — [BUILT] normalizes samples from every source above into one shape, fanned out to every enabled consumer below
                  │
   ┌──────────────┼──────────────┬──────────────┐
internal/          internal/     internal/      internal/
storage            alert         anomaly        backup
[BUILT, tier-0      [planned,     [planned,      [planned,
only - no           v0.4.0]       v0.4.0]        v0.5.0]
compression yet]
(RRD tiers,        (rule engine, (k-means        (CDC export
Gorilla             notifiers,   ensemble,       to Iceberg,
compression,        feature-     anomaly bit     watermark-
mmap files)         flagged)     embedded in     driven,
                                 storage,        feature-
                                 feature-        flagged)
                                 flagged)
                  │
internal/config             — [BUILT, partial] single YAML file + env overrides; only server/ingest.scrape/storage acted on so far, feature flags/`circa config init`/`check` are v0.3.0
internal/auth                — [planned, v0.3.0] optional multi-user bcrypt basic auth, no-auth default
web/                           — [planned, v0.2.0] static HTML/CSS/JS (uPlot-based dashboard), embedded via go:embed
```

Every package in the consumer fan-out below `internal/ingest` (`alert`, `anomaly`, `backup`) is optional and gated by its own feature flag from `internal/config` — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.3. On the source side, `internal/ingest/scrape` is the only always-on mechanism (an empty target list just means it does nothing); `influx` and `remotewrite` are themselves feature-flagged. `internal/storage` is the only always-on consumer; the baseline binary's footprint is scrape client + storage, nothing more.

## Where things go

| Kind of change | Goes in |
| :--- | :--- |
| Scrape client (pull from any Prometheus-format endpoint) | **Built.** `internal/ingest/scrape/` — target list, `expfmt` parsing, per-target ticker, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.2 |
| InfluxDB line protocol receiver | Planned, v0.3.0. `internal/ingest/influx/` — `/write` handler, measurement/field → series mapping, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.3 |
| Remote-write receive/send | Planned, v0.3.0. `internal/ingest/remotewrite/` — protobuf+Snappy decode/encode, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.4 |
| RRD tier logic, compression, mmap layout | **Built: tier-0 only, no compression/mmap yet** (v0.2.0). `internal/storage/` — see [DESIGN/03](DESIGN/03_design_storage.md) before changing tier sizes or the on-disk format |
| Query API (`/api/v1/query`, `/query_range`) | **Built: `query_range` only**, exact-label-match (no PromQL yet). `internal/query/` — reads from `internal/storage`, no writes |
| Alert rule evaluation + notifiers | Planned, v0.4.0. `internal/alert/` — new notifier channels go in `internal/alert/notify/<channel>.go` behind the existing dispatch interface, see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.1 |
| Anomaly detection (k-means ensemble) | Planned, v0.4.0. `internal/anomaly/` — see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 before changing model count/window defaults |
| Iceberg CDC export | Planned, v0.5.0. `internal/backup/` — watermark state, Arrow/Parquet batching, push+pull modes, see [DESIGN/07](DESIGN/07_design_backup.md) |
| Config keys | **Built: `server`/`ingest.scrape`/`storage` only**; `features`/`alerting`/`backup`/`push`/`auth` decode into `Config` but aren't acted on yet. `internal/config/config.go` (YAML field + env override), *and* [config.example.yaml](config.example.yaml) — that file is the user-facing reference, config.go alone isn't enough |
| HTTP handlers + routes | **Built: `query_range`, `healthz`, `readyz` only.** `internal/httpapi/` |
| Dashboard HTML/CSS/JS (no build step) | Planned, v0.2.0. `web/template/` for HTML templates, `web/static/{css,js}` for static assets, embed wiring in `web/embed.go` |
| Auth (basic auth, user store) | Planned, v0.3.0. `internal/auth/` — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.2 before adding anything beyond stateless basic auth |
| Tests | unit: alongside the package (`_test.go`, white-box — see `internal/storage/storage_test.go` for the pattern); full-stack: an `internal/httpapi/integration_test.go` pattern once the HTTP layer grows beyond query_range (see servicedesk's `testEnv`/`client` pattern for the shape to copy) |

## An ingestion event, end to end

Steps 1–3 and 7 are built as of v0.1.0; steps 4–6 (and the `/metrics` registry write in step 3) are still planned per RELEASE.md.

1. **Built.** Each configured scrape target gets its own ticker in `internal/ingest/scrape`, firing on that target's own interval (config-driven, per-target — mirrors Prometheus's own scrape loop, not one global tick). Line-protocol and remote-write samples will instead arrive whenever `internal/ingest/influx` or `internal/ingest/remotewrite`'s HTTP handlers receive a request (v0.3.0) — event-driven, not ticked.
2. **Built.** Whichever source produced the batch normalizes it into Circa's own sample shape and hands it to `internal/ingest.Pipeline.Ingest()`.
3. **Built (storage write); planned (`/metrics` write, v0.5.0).** The batch is handed to `internal/storage.Append()` — the only mandatory consumer, regardless of source. It will also be written to the standard Prometheus registry (serves `/metrics` for external scraping/federation) once that milestone lands.
4. Planned, v0.4.0. If `features.alerts` is on, the same batch also goes to `internal/alert.Evaluate()` against tier-0 data; a rule crossing its threshold + hysteresis dispatches through `internal/alert/notify`.
5. Planned, v0.4.0. If `features.ml` is on, the batch also feeds `internal/anomaly.Score()`, which embeds the anomaly bit back into the value written by step 3 (see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 — no separate anomaly time series).
6. Planned, v0.5.0. If `features.backup` is on, `internal/backup`'s own scheduler (independent of any ingestion event) periodically reads everything past its watermark from `internal/storage` and appends it to the configured Iceberg table.
7. **Built.** The UI (`web/`, planned v0.2.0) and any external tool talk to `internal/query`, never to `internal/storage` directly — `internal/query` is the only reader, `internal/ingest`/`internal/backup` are the only writers.

Nothing downstream of step 2 needs to know whether a sample was scraped, received as line protocol, or received over remote-write — all three sources converge on the same `internal/ingest.Ingest()` call.

## Pointing Circa at a new metrics source

Most new sources need **no code change** — this is the point of treating ingestion as generic protocols rather than per-source collectors (see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.1):

1. Anything exposing a Prometheus-format `/metrics` (node_exporter, postgres_exporter, blackbox_exporter, a custom app, even another Prometheus server's `/federate`): add an entry to `ingest.scrape.targets` in `config.yaml` — see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.2.
2. A Telegraf agent: repoint its `outputs.influxdb`/`outputs.http` at Circa's `/write` endpoint — a Telegraf-side config change, not a Circa-side one, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.3.
3. Only add Go code in `internal/ingest/<protocol>/` if the source speaks a protocol Circa doesn't already support (i.e. not scrape, not line protocol, not remote-write) — this should be rare.

## Adding a new alert notifier

1. Implement the dispatch interface in `internal/alert/notify/<channel>.go` (see the existing webhook/Slack notifiers for the shape once they exist).
2. Register the channel type in `internal/config` under `alerting.notifiers`.
3. Don't touch `internal/alert`'s rule-evaluation logic — notifiers are deliberately decoupled from the rule engine so adding a channel never risks the evaluation path.

## Deployment shape

Circa no longer reads `/proc`/`/sys` itself ([DESIGN/04](DESIGN/04_design_collection_and_ingestion.md)) — that's a co-located exporter's job now — but it's still designed as a **per-node local agent**, not a stateless multi-replica web app: see [DESIGN/05](DESIGN/05_design_ui.md) (per-node dashboard, not a multi-host view). The natural Kubernetes shape is therefore still a **DaemonSet** (one Circa pod per node, scraping a co-located node_exporter — or other exporter — over `localhost`), not a `Deployment` behind a `Service`/`Ingress`: there's still no "replica count" dimension to scale, and each node's storage/alerting/dashboard stays local to that node. What changed is that Circa's own pod no longer needs `hostPath` mounts for `/proc`/`/sys`; the co-located exporter is deployed and secured independently. See [k8s/README.md](k8s/README.md) and [helm/circa/README.md](helm/circa/README.md) for how that maps to manifests/chart, and [DESIGN/07](DESIGN/07_design_backup.md) §7.3 for why a *separate*, centrally-run backup agent (not the DaemonSet itself) is the one that talks to Iceberg in pull mode. A centralized-aggregator shape (one Circa instance scraping many remote nodes) is architecturally possible too, since scraping doesn't require co-location — see [DESIGN/09 open questions](DESIGN/09_design_tech_stack_and_roadmap.md) — but isn't the default recommendation here.
