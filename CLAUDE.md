# CLAUDE.md

Orientation for working in this repo. Read this first; it points to everything else rather than duplicating it.

## What this is

Circa: a self-monitoring single-binary metrics aggregator — storage, UI, alerting, and lightweight anomaly detection over its own host's built-in `/proc`/`/sys`/`sysctl` metrics (v0.5.0+, on by default) plus anything ingested from a Prometheus-format exporter, Telegraf, or Prometheus remote-write — no external database, no separate frontend deploy, no Alertmanager required. See [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.5 for the built-in collector, §4.1-4.4 for everything ingested from elsewhere.

@DESIGN.md is the desired design
@ARCHITECTURE.md is the architecture decisions
@RELEASE.md is the directory records released
@README.md is for end user

Licensed [Apache 2.0](LICENSE). Current version lives in [VERSION](VERSION), not hardcoded anywhere else.

## Build, test, release

@Makefile

Gotchas beyond what the Makefile itself documents:

- **Before claiming a change works**: run `make vet test`. If you touch the on-disk storage format, also re-measure disk footprint against [DESIGN/03](DESIGN/03_design_storage.md) §3.3 — a regression there is easy to miss with unit tests alone, since they use tiny fixtures rather than the metric-count/retention scale the format is designed for.
- **If a change touches `web/static/js`**: `go test`/`curl` smoke-testing doesn't execute client-side JS. Verify with an actual browser (see servicedesk's `CLAUDE.md` for the Playwright-based pattern to copy).
- **Before ending a task**: if the change affects behavior, update whichever of `DESIGN.md`/`DESIGN/`, `ARCHITECTURE.md`, or `RELEASE.md`/`RELEASE/vX.Y.Z.md` it touches — don't wait to be asked for each one separately. Close out with a one-line git commit message; the user commits it themselves.
- **CI/CD is tag-driven** (push a `v*` tag), not push-to-main-driven — see `.github/workflows/` and [TEMPLATE.md](TEMPLATE.md) §3 for the rationale.
- Do not run `make release`, `make bump`, or any git push/commit/tag command yourself unless the user explicitly asks — releasing is the user's call.

## Conventions worth knowing before you're surprised by them

- **This is a per-node agent, not a stateless web app.** Every deployment artifact (`k8s/` DaemonSet, `helm/circa/`) reflects that — don't add an `Ingress`/`HPA` "to match servicedesk's pattern"; there's no horizontal-scaling dimension here (one pod per node, by definition). See [ARCHITECTURE.md](ARCHITECTURE.md) "Deployment shape."
- **No database, anywhere.** Storage is local, self-contained, fixed-size round-robin — don't introduce Postgres/MySQL/SQLite for anything in this project; see [DESIGN/03](DESIGN/03_design_storage.md) for why that's a deliberate constraint, not an oversight.
- **Every optional subsystem (ML, alerts, backup, influx receive, push send/receive) is feature-flagged off by default.** `features.collect` (built-in self-monitoring, v0.5.0+) is the one deliberate exception — it defaults **on**, since Circa is a self-monitoring single binary first, not a scrape-and-store shell around some other exporter; see [RELEASE/v0.5.0.md](RELEASE/v0.5.0.md). Every other subsystem still follows the off-by-default rule — don't wire a new one to run unconditionally "for simplicity." See [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.3.
- **`internal/collect` (the built-in `/proc`/`/sys`/`sysctl` collector) is from-scratch in both directions — never `import` node_exporter's `collector` package, never vendor/copy files from node_exporter or netdata.** node_exporter is Apache-2.0 (legally reusable, deliberately not reused anyway — see `internal/collect`'s package doc and [RELEASE/v0.5.0.md](RELEASE/v0.5.0.md)'s "Reuse policy"); netdata is GPL-3.0 (copying it into this Apache-2.0 tree would be a real licensing conflict, not just a style choice). Read either as reference material for parsing edge cases, write every line fresh. Scope is Linux + macOS only through v1.0.0 — Windows/other OS is [RELEASE/v1.1.0.md](RELEASE/v1.1.0.md), not this milestone. See [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.5 for the full history, including why this reverses §4.1's "doesn't collect metrics" position a second time.
- **Anomaly bit lives inside the existing storage format, not as a separate time series.** See [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 before adding a new anomaly-related column/series.
