# CLAUDE.md

Orientation for working in this repo. Read this first; it points to everything else rather than duplicating it.

## What this is

Circa: a single-binary metrics aggregator — storage, UI, alerting, and lightweight anomaly detection layered over metrics ingested from any Prometheus-format exporter, Telegraf, and Prometheus remote-write — no external database, no separate frontend deploy, no Alertmanager required. It doesn't collect metrics itself; see [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) for why.

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
- **Every optional subsystem (ML, alerts, backup, influx receive, push send/receive) is feature-flagged off by default.** Don't wire a new subsystem to run unconditionally "for simplicity" — the whole leanness pitch depends on the baseline binary (scrape client + storage) costing close to what node_exporter alone costs. See [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1.3.
- **Circa doesn't collect metrics — it ingests them.** Don't add a `/proc`/`/sys`-reading collector or wrap `node_exporter/collector`; a new metrics source is a scrape target in `config.yaml`, not new Go code. See [DESIGN/04](DESIGN/04_design_collection_and_ingestion.md) §4.1 for why this was deliberately reversed from an earlier design that did import node_exporter's collector package.
- **Anomaly bit lives inside the existing storage format, not as a separate time series.** See [DESIGN/06](DESIGN/06_design_alerting_and_anomaly_detection.md) §6.2 before adding a new anomaly-related column/series.
