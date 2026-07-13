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
   ┌────────────┬────────────────┬─────────────────┬───────────────────┐
internal/       internal/ingest/ internal/ingest/  internal/ingest/
collect         scrape           influx            remotewrite
[BUILT: v0.5.0] [BUILT]          [planned, no       [BUILT: v0.3.0]
                                  milestone yet]
(/proc, sysctl, (pull: expfmt    (InfluxDB line     (protobuf+Snappy
etc. — own      against          protocol on        receive + send,
host, default   configured       /write, flagged)   both flagged)
on)             targets,
                always on)
        │              │                 │                   │
        └──────────────┴────────┬────────┴─────────┬─────────┘
             internal/ingest            — [BUILT] normalizes samples from every source above into one shape, fanned out to every enabled consumer below
                  │
   ┌──────────────┼──────────────┬──────────────┐
internal/          internal/     internal/      internal/
storage            alert         anomaly        backup
[BUILT: tier-0/     [BUILT:       [BUILT:        [planned,
1/2, Gorilla        v0.4.0]       v0.4.0]        v0.7.0]
compression,
anomaly bit]
(RRD tiers,        (rule engine, (k-means        (CDC export
Gorilla             notifiers,   ensemble,       to Iceberg,
compression,        feature-     anomaly bit     watermark-
LSB anomaly         flagged)     embedded in     driven,
bit trick)                       storage,        feature-
                                 feature-        flagged)
                                 flagged)
                  │
internal/config             — [BUILT: v0.4.0] single YAML file, full schema + cross-field Validate() incl. alerting/anomaly; `circa config init`/`check` (cmd/circa/config_cli.go)
internal/auth                — [BUILT: v0.3.0] optional multi-user bcrypt basic auth, no-auth default; `circa auth add-user`/`reset-password` (cmd/circa/auth_cli.go)
web/                           — [BUILT: v0.6.0] 8 pages (Overall + one per collector category + Metrics), static HTML/CSS/JS, uPlot, embedded via go:embed
```

`internal/storage`'s compression is a from-scratch Gorilla-style bit-packer (`gorilla.go`/`bitstream.go`), not mmap — see [RELEASE/v0.2.0.md](RELEASE/v0.2.0.md) for why mmap was skipped and the actual compression ratio measured. Tier-1/tier-2 rollups (`tiered.go`) are stored as three ordinary series per real metric (`#min`/`#avg`/`#max`), reusing the tier-0 engine rather than a shared-timestamp triplet format — also a deliberate v0.2.0 simplification. As of v0.4.0, `internal/storage` also carries the anomaly bit (`anomalybit.go`) — see [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 and [DESIGN/10](DESIGN/10_ml_summary.md) before touching either.

Every package in the consumer fan-out below `internal/ingest` (`alert`, `anomaly`, `backup`) is optional and gated by its own feature flag from `internal/config` — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.3. On the source side, `internal/collect` (v0.5.0+) and `internal/ingest/scrape` are the two mechanisms that are effectively always-on — `collect`'s own flag (`features.collect`) defaults **true**, unlike every other feature flag, and scrape's target list simply defaults empty; `influx` and `remotewrite` are themselves feature-flagged **and** default off. `internal/storage` is the only always-on consumer; the baseline binary's footprint is self-collection + storage, not just scrape client + storage. **`internal/anomaly` is not itself a fanned-out `ingest.Consumer`** despite the diagram's positioning — its `Detector.Score` runs in `cmd/circa`'s `handleSample` *before* `ingest.Pipeline.Ingest` is called, so the anomaly bit is already set on the `ingest.Sample` by the time `internal/storage` and `internal/alert` (which *is* a real `Consumer`) both see it. See "An ingestion event, end to end" below.

## Where things go

| Kind of change | Goes in |
| :--- | :--- |
| Built-in local system collection (self-monitoring: cpu/memory/disk/network/load/uname) | **Built: v0.5.0.** `internal/collect/` — `/proc`+`/sys` on Linux, `sysctl`/`vm_stat`/`netstat`/`top`/`route`+`Getfsstat`/`Uname` syscalls on macOS. `features.collect` defaults **on** (unlike every other flag). From-scratch — node_exporter/netdata are reference material only, never imported/vendored, see the package doc comment and [RELEASE/v0.5.0.md](RELEASE/v0.5.0.md)'s "Reuse policy." Windows/other OS deferred to v1.1.0. See [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.5 |
| Scrape client (pull from any Prometheus-format endpoint) | **Built.** `internal/ingest/scrape/` — target list, `expfmt` parsing, per-target ticker, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.2 |
| InfluxDB line protocol receiver | Planned, not yet assigned to a milestone. `internal/ingest/influx/` — `/write` handler, measurement/field → series mapping, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.3. `ingest.influx.path` config + validation already exist (`internal/config`); the handler itself doesn't |
| Remote-write receive/send | **Built: v0.3.0.** `internal/ingest/remotewrite/` — `receiver.go` decodes protobuf+Snappy `POST <push.receive.path>` into the shared `ingest.Pipeline`; `sender.go` ticks on `push.send.interval`, batching every series' points since an in-memory (not persisted) watermark and pushing them onward. See [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.4 |
| RRD tier logic, compression, mmap layout | **Built: tier-0/1/2, Gorilla-compressed, no mmap** (from-scratch bit-packer, not `go-tsz` — see RELEASE/v0.2.0.md). `internal/storage/` — see [DESIGN/03](DESIGN/03_design_storage.md) before changing tier sizes or the on-disk format |
| Query API (`/api/v1/query`, `/query_range`) | **Built: `query_range` + `series` + `AnomalyRanking`**, exact-label-match (no PromQL yet), tier=raw/minute/hour. `internal/query/` — reads from `internal/storage`, no writes |
| Alert rule evaluation + notifiers | **Built: v0.4.0.** `internal/alert/` — `Rule`/`Condition` (threshold/rate_of_change/anomaly), `Engine` (an `ingest.Consumer`). New notifier channels go in `internal/alert/notify/<channel>.go` behind the existing `Notifier` interface — see "Adding a new alert notifier" below and [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.1 |
| Anomaly detection (k-means ensemble) | **Built: v0.4.0**, matched against Netdata's real source, not just DESIGN/06 §6.2's summary — see [DESIGN/10](DESIGN/10_ml_summary.md) for the full mapping before changing model count/window/threshold defaults. `internal/anomaly/` (`model.go` — k-means + Netdata-matching preprocessing/scoring, `detector.go` — FIFO ensemble + staggered retraining). Wired into `cmd/circa` as a pre-ingest scoring step, not an `ingest.Consumer` — see the diagram note above |
| Iceberg CDC export | Planned, v0.7.0. `internal/backup/` — watermark state, Arrow/Parquet batching, push+pull modes, see [DESIGN/07](DESIGN/07_design_backup.md) |
| Config keys | **Built: v0.5.0, all sections except backup.** `server`/`ingest.collect`/`ingest.scrape`/`ingest.influx`/`storage`/`push`/`auth`/`alerting`/`anomaly` are acted on; `backup` decodes into `Config` (with cross-field `Validate()` checks) but isn't acted on yet — that's v0.7.0. `internal/config/config.go` + `template.go` (the `circa config init` template), *and* [config.example.yaml](config.example.yaml) — that file is the user-facing reference, config.go alone isn't enough |
| `circa config init`/`check`, `circa auth add-user`/`reset-password`/`hash-password` | **Built: v0.3.0.** `cmd/circa/config_cli.go`, `cmd/circa/auth_cli.go` — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.2 |
| HTTP handlers + routes | **Built: `query_range`, `series`, `alerts`, `anomalies`, `status`, `healthz`, `readyz`, dashboard (`/`, `/static/*`), write receiver (`POST push.receive.path`, when `features.push_receive` is on).** `internal/httpapi/` — `/healthz`/`/readyz` stay unauthenticated even when `auth.users` is set, everything else goes through `internal/auth.Middleware` |
| Dashboard HTML/CSS/JS (no build step) | **Built: v0.6.0 restructured this from one page into 8** — Overall (zero-config collector-category grid), one detail page per category (CPU/Memory/Network/Disk/Filesystem/Load), and Metrics (the original manual metric-picker chart + v0.4.0's Alerts/"what's unusual" panels), sharing one nav partial. `web/template/*.html` (one per page + `nav.html`), `web/static/js/{circa-chart,circa-data,overview,detail,app}.js`, `web/static/css/app.css` (category-color design tokens), embed/routing wiring in `web/embed.go`'s `pages` map |
| Auth (basic auth, user store) | **Built: v0.3.0.** `internal/auth/` (`auth.go` — `Middleware`, bcrypt check; `userfile.go` — `SetUser`, edits `auth.users` into the YAML `yaml.Node` tree in place). See [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.2 before adding anything beyond stateless basic auth |
| Tests | unit: alongside the package (`_test.go`, white-box — see `internal/storage/storage_test.go` for the pattern); full-stack: an `internal/httpapi/integration_test.go` pattern once the HTTP layer grows beyond query_range/series (see servicedesk's `testEnv`/`client` pattern for the shape to copy) |

## An ingestion event, end to end

Every step except step 6 (backup) is built; the `/metrics` registry write folded into step 4 is also still planned, per RELEASE.md.

1. **Built** for self-collection, scrape, and remote-write; influx line protocol still planned. `internal/collect.Collector` ticks on its own interval (`ingest.collect.interval`, default 15s), reading the local host directly — no target URL, no HTTP round trip (v0.5.0). Each configured scrape target separately gets its own ticker in `internal/ingest/scrape`, firing on that target's own interval (config-driven, per-target — mirrors Prometheus's own scrape loop, not one global tick). Remote-write samples arrive event-driven whenever `internal/ingest/remotewrite`'s `ReceiveHandler` gets a `POST` (v0.3.0); line-protocol samples will similarly arrive via `internal/ingest/influx`'s HTTP handler once that's built.
2. **Built: v0.4.0.** If `features.ml` is on, `cmd/circa`'s `handleSample` calls `internal/anomaly.Detector.Score()` **before** the sample reaches the pipeline, setting `ingest.Sample.Anomalous`. This runs ahead of (not as part of) step 4 deliberately — see the diagram note above and [DESIGN/10](DESIGN/10_ml_summary.md) §3.
3. **Built.** Whichever source produced the batch normalizes it into Circa's own sample shape (with `Anomalous` already set, if applicable) and hands it to `internal/ingest.Pipeline.Ingest()`.
4. **Built (storage write); planned (`/metrics` write, v0.7.0).** The batch is handed to `internal/storage.Append()` — the only mandatory consumer, regardless of source — which embeds the anomaly bit into the value it writes (`internal/storage/anomalybit.go`). It will also be written to the standard Prometheus registry (serves `/metrics` for external scraping/federation) once that milestone lands.
5. **Built: v0.4.0.** If `features.alerts` is on, the same batch also goes to `internal/alert.Engine.Consume()` (a real `ingest.Consumer`, fanned out alongside `internal/storage`) against tier-0 data; a rule crossing its threshold/rate-of-change/anomaly-bit condition + hysteresis dispatches through `internal/alert/notify`.
6. Planned, v0.7.0. If `features.backup` is on, `internal/backup`'s own scheduler (independent of any ingestion event) periodically reads everything past its watermark from `internal/storage` and appends it to the configured Iceberg table.
7. **Built: v0.4.0**, separately from the ingestion path. `internal/anomaly.Detector.Run()` retrains models on its own schedule (staggered across `RetrainInterval`), reading recent history back out through `internal/query`, not from the live ingestion stream.
8. **Built.** The UI (`web/`) and any external tool talk to `internal/query`, never to `internal/storage` directly — `internal/query` is the only reader, `internal/ingest`/`internal/backup` are the only writers.

Nothing downstream of step 2 needs to know whether a sample was self-collected, scraped, received as line protocol, or received over remote-write — every source converges on the same `internal/ingest.Ingest()` call.

## Pointing Circa at a new metrics source

The local host itself needs **no configuration at all** — `internal/collect` monitors it automatically (`features.collect`, on by default, v0.5.0+, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.5). For anything else, most sources need **no code change either** — this is the point of treating ingestion as generic protocols rather than per-source collectors (see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.1):

1. Anything exposing a Prometheus-format `/metrics` (node_exporter, postgres_exporter, blackbox_exporter, a custom app, even another Prometheus server's `/federate`): add an entry to `ingest.scrape.targets` in `config.yaml` — see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.2.
2. A Telegraf agent: repoint its `outputs.influxdb`/`outputs.http` at Circa's `/write` endpoint — a Telegraf-side config change, not a Circa-side one, see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.3.
3. Only add Go code in `internal/ingest/<protocol>/` if the source speaks a protocol Circa doesn't already support (i.e. not scrape, not line protocol, not remote-write) — this should be rare. `internal/collect/<goos>.go` is the one exception, and only for a genuinely new OS (Windows, v1.1.0) — not a new metrics source on an already-supported OS, which is still config, not code.

## Adding a new alert notifier

1. Implement `alert.Notifier` (one `Notify(alert.Alert) error` method) in a new `internal/alert/notify/<channel>.go` — see `webhook.go`/`slack.go` for the shape.
2. Add the channel's `type` string to the `switch` in `cmd/circa/main.go`'s alert-engine wiring (constructing the concrete `notify.*` type from each `config.NotifierConfig`), and to `validateAlerting`'s type check in `internal/config/config.go`.
3. Don't touch `internal/alert`'s rule-evaluation logic (`engine.go`) — notifiers are deliberately decoupled from the rule engine so adding a channel never risks the evaluation path.

## Deployment shape

Circa reads `/proc`/`/sys` itself again as of v0.5.0 ([DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.5) — a co-located exporter is no longer required, though still supported via `ingest.scrape.targets` for anything beyond the local host. It's still designed as a **per-node local agent**, not a stateless multi-replica web app: see [DESIGN/05](DESIGN/05_design_ui.md) (per-node dashboard, not a multi-host view). The natural Kubernetes shape is therefore still a **DaemonSet** (one Circa pod per node, self-monitoring that node), not a `Deployment` behind a `Service`/`Ingress`: there's still no "replica count" dimension to scale, and each node's storage/alerting/dashboard stays local to that node. A pod's own `/proc` is its container's, not the node's, so `k8s/20-daemonset.yaml`/`helm/circa`'s chart `hostPath`-mount the host's real `/proc` read-only at `/host/proc` and point `internal/collect` at it via `HOST_PROC` — the same pattern node_exporter's own official manifests use, for the same reason. Downward API env vars (`POD_NAME`/`POD_NAMESPACE`/`NODE_NAME`) tag every self-collected sample with its origin. See [k8s/README.md](k8s/README.md) and [helm/circa/README.md](helm/circa/README.md) for how that maps to manifests/chart, and [DESIGN/07](DESIGN/07_design_backup.md) §7.3 for why a *separate*, centrally-run backup agent (not the DaemonSet itself) is the one that talks to Iceberg in pull mode. A centralized-aggregator shape (one Circa instance scraping many remote nodes) is architecturally possible too, since scraping doesn't require co-location — see [DESIGN/09 open questions](DESIGN/09_design_tech_stack_and_roadmap.md) — but isn't the default recommendation here.
