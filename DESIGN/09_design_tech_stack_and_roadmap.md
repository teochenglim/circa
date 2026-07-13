# Circa — Design 09: Tech stack, milestones, and open questions

Tracked status per milestone lives in [../RELEASE.md](../RELEASE.md) → `RELEASE/`, not here.

## 9.1 Suggested tech stack

| Concern | Choice | Why |
|---|---|---|
| Language/runtime | Go, single static binary | matches the stated requirement, cross-compiles trivially, no runtime deps |
| Ingestion (scrape) | `prometheus/common/expfmt` text parser against a configured target list | one code path scrapes node_exporter, any other Prometheus-format exporter, or an existing Prometheus server's `/federate` — no collector code to maintain |
| Ingestion (line protocol) | InfluxDB line protocol on `/write` / `/api/v2/write` | Telegraf fleets repoint via a URL swap, no output-plugin change |
| Metrics format | Prometheus exposition format on `/metrics` | interop with existing Prometheus/Grafana if wanted; also lets Circa itself be scraped/federated from |
| Push ingestion | Prometheus remote-write protocol (protobuf + Snappy) both as receiver and sender | standard, widely-implemented wire format — works with existing agents unmodified |
| Storage | custom RRA-style ring buffers, Gorilla-style delta+XOR compression, mmap-backed files | constant disk footprint + good compression ratio; prototype against `go-tsz` |
| Frontend build | plain HTML/CSS/JS (or a minimal build step), embedded via `go:embed` | keeps "single binary, static HTML" property |
| Charting | uPlot | smallest/fastest canvas time-series lib, ~50KB, no framework lock-in |
| Alerting | custom rule engine + pluggable notifiers (webhook/Slack/email), feature-flagged | avoids requiring a separate Alertmanager |
| Anomaly detection | k-means (k=2) ensemble per metric, anomaly bit embedded in storage, feature-flagged | proven lightweight design, explainable, cheap |
| Remote backup | watermark-driven CDC-style delta export → Iceberg table (`go-iceberg` or `apache/iceberg-go` + Arrow/Parquet), push or pull mode, feature-flagged | no block/sidecar machinery; reuses open table format for durability; pull mode centralizes credentials |
| Federation search | plain SQL against the shared Iceberg table (DuckDB/Trino/Spark), filtered by `node_id` | no bespoke querier component; federation is a query, not a system |
| Config | single YAML file with feature flags, `circa config init`/`check` CLI | one file to reason about, version-control, and validate before deploy |
| Auth | optional multi-user bcrypt basic auth (Prometheus exporter-toolkit pattern), stateless, no-auth default | proven, minimal-surface pattern; CLI-driven password reset instead of a self-service flow |

## 9.2 Open questions — resolved as of v1.0.0

Each of these was genuinely open when this file was first written; every one has since been settled by what actually shipped (see [RELEASE.md](../RELEASE.md) → `RELEASE/` for which milestone). Recorded here as decisions, not questions, so a future change has to be a deliberate reversal rather than re-litigating from scratch.

- **Retention/tier defaults**: mirror Netdata's, as originally suggested — 2h raw / 7d at 1min / 365d at 1hr (`config.Default()`, `internal/config/config.go`). Unchanged since v0.1.0; no pressure to size differently has shown up.
- **Alert rules and ML config location**: stayed inline in the single YAML (`alerting:`/`anomaly:` sections, built v0.4.0) — "single file, version-controls naturally" won over "easier diffing at scale," and rule sets in practice haven't grown large enough to make that a real cost.
- **Multi-tenant/multi-host dashboard**: resolved as strictly per-node local (v0.6.0's tabbed dashboard is one host's view, full stop) — closer to Netdata's local agent than Netdata Cloud, per the original framing. A multi-host *view* exists, but as a SQL query against the shared Iceberg table (see federation, below), not a Circa UI feature. This is also why RBAC stayed out of scope ([08 §8.2.3](08_design_config_auth_ops.md)) — there's no per-host permission boundary to enforce inside a single-host UI.
- **CDC export cadence**: left as an operator-set `backup.schedule` cron string (no fixed built-in cadence) rather than circa picking one — `config.example.yaml` ships `*/15 * * * *` as a reasonable starting point, but the right value trades freshness against batch size/cost per deployment, which only the operator can judge.
- **Default backup mode**: no hardcoded default — `backup.mode` is a required field, validated at startup (`Config.Validate`, `internal/config/config.go`) — but `config.example.yaml` ships `mode: pull`, matching [07 §7.3](07_design_backup.md)'s recommendation for anything beyond a single standalone instance (credential centralization in the central `circa backup-agent`, not scattered across every node). A single-instance deployment should explicitly choose `push` instead.
- **Shared Iceberg catalog/bucket**: each deployment brings its own (`backup.catalog.uri`/`.warehouse` are per-config, never assumed shared) — "federation" is then just whether multiple deployments happen to point at the *same* catalog, a deployment choice, not something circa enforces either way.
- **`apache/iceberg-go` vs. the community `go-iceberg` fork**: `apache/iceberg-go` is what shipped (`internal/backup/iceberg.go`, v0.7.0) — its write support (`Table.AppendTable`) had caught up enough by implementation time; no need fell out for the community fork.
- **Basic auth sufficiency**: still the only auth mechanism, deliberately — [08 §8.2.3](08_design_config_auth_ops.md)'s "point real RBAC/SSO pressure at a reverse proxy instead" position held through v1.0.0 with no real pressure otherwise. Stated as an explicit non-goal, not a gap: building RBAC/SSO into circa itself would cut against the "lean, single-binary" goal.
- **Push receiver auth**: resolved as *the same* basic-auth config as everything else, not a separate bearer-token scheme — `POST <push.receive.path>` is registered on the same auth-wrapped mux as the dashboard and every other API route (`internal/httpapi/httpapi.go`'s `protected` mux, wrapped once in `auth.Middleware`). One auth story for the whole process, not two.
- **Zero-config onboarding**: resolved by reversing the premise rather than picking a default scrape target — `internal/collect` (v0.5.0) means circa collects its own host's metrics again, so a fresh install has something to show with zero config, no assumption about a co-located node_exporter on `localhost:9100` required. `ingest.scrape.targets` is now purely for *additional* hosts/exporters, never a prerequisite.
