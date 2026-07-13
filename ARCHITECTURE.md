# Architecture

How the code is organized and how an ingestion event flows through it. For *what* the system does and *why*, see [DESIGN.md](DESIGN.md); this file is about *how* it's built.

> The diagram and table below reflect current structure — `[BUILT]`/`Built` marks what exists, everything else is still the *target* layout. See [RELEASE.md](RELEASE.md) → `RELEASE/` for what shipped in which version and why.

## Layering

```
cmd/circa/main.go        — [BUILT] wires everything together, owns the process lifecycle
        │
internal/httpapi          — [BUILT, partial] HTTP handlers + router
                             (query_range, series, status, healthz, readyz,
                             dashboard, write (remote-write) built;
                             metrics, write (influx) still planned)
        │
   ┌────┴──────────┬───────────────┐
internal/ingest/    internal/ingest/  internal/ingest/
scrape              influx            remotewrite
[BUILT]             [planned, no      [BUILT: v0.3.0]
                     milestone yet]
(pull: expfmt       (InfluxDB line    (protobuf+Snappy
against configured  protocol on       receive + send,
targets, always on) /write, flagged)  both flagged)
        │                 │                 │
        └────────┬────────┴────────┬────────┘
             internal/ingest            — [BUILT] normalizes samples from every source above into one shape, fanned out to every enabled consumer below
                  │
   ┌──────────────┼──────────────┬──────────────┐
internal/          internal/     internal/      internal/
storage            alert         anomaly        backup
[BUILT: tier-0/     [planned,     [planned,      [planned,
1/2, Gorilla        v0.4.0]       v0.4.0]        v0.5.0]
compression]
(RRD tiers,        (rule engine, (k-means        (CDC export
Gorilla             notifiers,   ensemble,       to Iceberg,
compression,        feature-     anomaly bit     watermark-
mmap files)         flagged)     embedded in     driven,
                                 storage,        feature-
                                 feature-        flagged)
                                 flagged)
                  │
internal/config             — [BUILT: v0.3.0] single YAML file, full schema + cross-field Validate(); `circa config init`/`check` (cmd/circa/config_cli.go)
internal/auth                — [BUILT: v0.3.0] optional multi-user bcrypt basic auth, no-auth default; `circa auth add-user`/`reset-password` (cmd/circa/auth_cli.go)
web/                           — [BUILT] static HTML/CSS/JS (uPlot-based dashboard), embedded via go:embed
```

`internal/storage`'s compression is a from-scratch Gorilla-style bit-packer (`gorilla.go`/`bitstream.go`), not mmap — see [RELEASE/v0.2.0.md](RELEASE/v0.2.0.md) for why mmap was skipped and the actual compression ratio measured. Tier-1/tier-2 rollups (`tiered.go`) are stored as three ordinary series per real metric (`#min`/`#avg`/`#max`), reusing the tier-0 engine rather than a shared-timestamp triplet format — also a deliberate v0.2.0 simplification.

Every package in the consumer fan-out below `internal/ingest` (`alert`, `anomaly`, `backup`) is optional and gated by its own feature flag from `internal/config` — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.3. On the source side, `internal/ingest/scrape` is the only always-on mechanism (an empty target list just means it does nothing); `influx` and `remotewrite` are themselves feature-flagged. `internal/storage` is the only always-on consumer; the baseline binary's footprint is scrape client + storage, nothing more.

## Where things go

| Kind of change | Goes in |
| :--- | :--- |
| Scrape client (pull from any Prometheus-format endpoint) | **Built.** `internal/ingest/scrape/` — target list, `expfmt` parsing, per-target ticker, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.2 |
| InfluxDB line protocol receiver | Planned, not yet assigned to a milestone. `internal/ingest/influx/` — `/write` handler, measurement/field → series mapping, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.3. `ingest.influx.path` config + validation already exist (`internal/config`); the handler itself doesn't |
| Remote-write receive/send | **Built: v0.3.0.** `internal/ingest/remotewrite/` — `receiver.go` decodes protobuf+Snappy `POST <push.receive.path>` into the shared `ingest.Pipeline`; `sender.go` ticks on `push.send.interval`, batching every series' points since an in-memory (not persisted) watermark and pushing them onward. See [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.4 |
| RRD tier logic, compression, mmap layout | **Built: tier-0/1/2, Gorilla-compressed, no mmap** (from-scratch bit-packer, not `go-tsz` — see RELEASE/v0.2.0.md). `internal/storage/` — see [DESIGN/03](DESIGN/03_design_storage.md) before changing tier sizes or the on-disk format |
| Query API (`/api/v1/query`, `/query_range`) | **Built: `query_range` + `series`**, exact-label-match (no PromQL yet), tier=raw/minute/hour. `internal/query/` — reads from `internal/storage`, no writes |
| Alert rule evaluation + notifiers | Planned, v0.4.0. `internal/alert/` — new notifier channels go in `internal/alert/notify/<channel>.go` behind the existing dispatch interface, see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.1 |
| Anomaly detection (k-means ensemble) | Planned, v0.4.0. `internal/anomaly/` — see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 before changing model count/window defaults |
| Iceberg CDC export | Planned, v0.5.0. `internal/backup/` — watermark state, Arrow/Parquet batching, push+pull modes, see [DESIGN/07](DESIGN/07_design_backup.md) |
| Config keys | **Built: v0.3.0, all sections.** `server`/`ingest.scrape`/`ingest.influx`/`storage`/`push`/`auth` are acted on; `alerting`/`backup` decode into `Config` (with cross-field `Validate()` checks) but aren't acted on yet — that's v0.4.0/v0.5.0. `internal/config/config.go` + `template.go` (the `circa config init` template), *and* [config.example.yaml](config.example.yaml) — that file is the user-facing reference, config.go alone isn't enough |
| `circa config init`/`check`, `circa auth add-user`/`reset-password` | **Built: v0.3.0.** `cmd/circa/config_cli.go`, `cmd/circa/auth_cli.go` — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.2 |
| HTTP handlers + routes | **Built: `query_range`, `series`, `status`, `healthz`, `readyz`, dashboard (`/`, `/static/*`), write receiver (`POST push.receive.path`, when `features.push_receive` is on).** `internal/httpapi/` — `/healthz`/`/readyz` stay unauthenticated even when `auth.users` is set, everything else goes through `internal/auth.Middleware` |
| Dashboard HTML/CSS/JS (no build step) | **Built.** `web/template/` for HTML templates, `web/static/{css,js}` for static assets (vendored uPlot v1.6.32), embed wiring in `web/embed.go` |
| Auth (basic auth, user store) | **Built: v0.3.0.** `internal/auth/` (`auth.go` — `Middleware`, bcrypt check; `userfile.go` — `SetUser`, edits `auth.users` into the YAML `yaml.Node` tree in place). See [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.2 before adding anything beyond stateless basic auth |
| Tests | unit: alongside the package (`_test.go`, white-box — see `internal/storage/storage_test.go` for the pattern); full-stack: an `internal/httpapi/integration_test.go` pattern once the HTTP layer grows beyond query_range/series (see servicedesk's `testEnv`/`client` pattern for the shape to copy) |

## An ingestion event, end to end

Steps 1–3 and 7 are built; steps 4–6 (and the `/metrics` registry write in step 3) are still planned per RELEASE.md.

1. **Built** for scrape and remote-write; influx line protocol still planned. Each configured scrape target gets its own ticker in `internal/ingest/scrape`, firing on that target's own interval (config-driven, per-target — mirrors Prometheus's own scrape loop, not one global tick). Remote-write samples arrive event-driven whenever `internal/ingest/remotewrite`'s `ReceiveHandler` gets a `POST` (v0.3.0); line-protocol samples will similarly arrive via `internal/ingest/influx`'s HTTP handler once that's built.
2. **Built.** Whichever source produced the batch normalizes it into Circa's own sample shape and hands it to `internal/ingest.Pipeline.Ingest()`.
3. **Built (storage write); planned (`/metrics` write, v0.5.0).** The batch is handed to `internal/storage.Append()` — the only mandatory consumer, regardless of source. It will also be written to the standard Prometheus registry (serves `/metrics` for external scraping/federation) once that milestone lands.
4. Planned, v0.4.0. If `features.alerts` is on, the same batch also goes to `internal/alert.Evaluate()` against tier-0 data; a rule crossing its threshold + hysteresis dispatches through `internal/alert/notify`.
5. Planned, v0.4.0. If `features.ml` is on, the batch also feeds `internal/anomaly.Score()`, which embeds the anomaly bit back into the value written by step 3 (see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 — no separate anomaly time series).
6. Planned, v0.5.0. If `features.backup` is on, `internal/backup`'s own scheduler (independent of any ingestion event) periodically reads everything past its watermark from `internal/storage` and appends it to the configured Iceberg table.
7. **Built.** The UI (`web/`) and any external tool talk to `internal/query`, never to `internal/storage` directly — `internal/query` is the only reader, `internal/ingest`/`internal/backup` are the only writers.

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
