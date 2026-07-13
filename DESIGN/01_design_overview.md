# Circa — Design 01: Overview & Prior Art

> Project name: **Circa** — nails both the time-series and circular-ring (round-robin) nature of the storage engine, and is short enough to type into a systemd unit file without frustration.

## 1. Problem statement

Prometheus `node_exporter` exposes metrics but stores nothing and ships no UI. Netdata gives you storage, UI, alerting and ML anomaly detection out of the box, but it is a C/C++ codebase with a heavier footprint and a more involved build. MRTG/RRDtool pioneered round-robin storage but the UI and tooling are dated.

Goal: a single Go binary that:
- ingests node-level and custom metrics from whatever's already producing them — scraping any Prometheus-exposition-format exporter (node_exporter, postgres_exporter, a custom app's `/metrics`, an existing Prometheus server's `/federate`), accepting Telegraf via InfluxDB line protocol, and accepting/sending Prometheus remote-write — rather than collecting metrics itself
- stores them locally in a fixed-size round-robin structure (constant disk footprint, no unbounded growth)
- serves a static-HTML dashboard directly from the binary (no separate frontend build/deploy)
- supports both pull (Prometheus-style scrape) and push (Prometheus remote-write-style) metrics ingestion
- evaluates alert rules and dispatches notifications
- flags anomalies per-metric using lightweight, explainable ML (no GPU, no heavy deps)
- backs up locally-stored data into an Iceberg lake on a schedule, in either push or pull mode
- is configured from a single YAML file with feature flags controlling every optional subsystem, so the default footprint stays lean
- deploys as one file, no external DB, no separate agent/server split (though the architecture shouldn't preclude a central collector later)

## 2. Prior art, and what to take from each

| System | Storage | UI | Alerting | Anomaly detection | Deployment |
|---|---|---|---|---|---|
| **node_exporter** | none (pull-only, stateless) | none | none (delegates to Alertmanager) | none | single Go binary |
| **MRTG / RRDtool** | fixed-size round-robin archives (RRAs) at multiple resolutions, pre-aggregated (avg/min/max) on insert | static PNG/HTML, regenerated on a cron | external (via thresholds in scripts) | none | C binary + Perl/cron |
| **Netdata** | `dbengine`: in-memory ring buffer + memory-mapped page files on disk, tiered downsampling (tier0 raw, tier1/tier2 aggregated), 4 bytes/sample raw | embedded web dashboard, JS charts | built-in health engine, template-based rules | ensemble of k-means clustering models (k=2) trained per metric, unsupervised, using dlib | single C binary |
| **Prometheus + Alertmanager** | TSDB, 2h blocks + WAL, not round-robin (grows with retention config) | Grafana (separate) | Alertmanager (separate service) | none built-in | multi-binary |

Design decisions this suggests:
- **Ingestion**: don't reimplement what node_exporter, Telegraf, and Prometheus already do well — speak their existing wire protocols (scrape, InfluxDB line protocol, remote-write) and treat every metrics source as a target to pull from or a client to accept pushes from, rather than collecting anything locally (see [04](04_design_collection_and_ingestion.md)).
- **Storage**: borrow RRD's *fixed-size, multi-resolution round-robin* model (constant disk usage regardless of retention length) but use Netdata/Gorilla-style **delta + XOR compression** instead of RRD's raw 4/8-byte-per-sample layout, so more history fits in the same footprint.
- **UI**: borrow Netdata's *embedded dashboard* idea, but keep it genuinely static (pre-built HTML/JS/CSS embedded via `go:embed`) rather than dynamically templated, matching the "static HTML" requirement and keeping the binary simple to audit and cache.
- **Alerting**: borrow Netdata's *template-based health engine* (rule = metric selector + condition + severity + hysteresis) but keep notification dispatch pluggable (webhook, Slack, email, PagerDuty-compatible) rather than requiring a separate Alertmanager.
- **Anomaly detection**: directly adopt Netdata's approach — it works on preprocessed feature vectors rather than raw values and flags an anomaly when the distance from learned cluster centers exceeds a threshold based on the 99th percentile of training data. This is deliberately simple, cheap per-metric, and explainable, which matters if this is meant to run on constrained nodes.
- **Everything optional is feature-flagged**, off by default, so the baseline binary — a scrape client, storage, and `/metrics` — costs close to what node_exporter itself costs, without carrying node_exporter's own collection code to get there. Leanness is a default, not an afterthought.

## Document map

| File | Covers |
| :--- | :--- |
| [02_design_architecture.md](02_design_architecture.md) | High-level architecture |
| [03_design_storage.md](03_design_storage.md) | The RRD-like storage engine: tiers, compression, footprint |
| [04_design_collection_and_ingestion.md](04_design_collection_and_ingestion.md) | Ingesting from any exporter, Telegraf, and remote-write — scrape, line protocol, push |
| [05_design_ui.md](05_design_ui.md) | The embedded static HTML dashboard |
| [06_design_alerting_and_anomaly_detection.md](06_design_alerting_and_anomaly_detection.md) | Alert rule engine and k-means anomaly detection |
| [07_design_backup.md](07_design_backup.md) | CDC-style export into an Iceberg lake |
| [08_design_config_auth_ops.md](08_design_config_auth_ops.md) | Single-YAML config, feature flags, auth, operability |
| [09_design_tech_stack_and_roadmap.md](09_design_tech_stack_and_roadmap.md) | Tech stack, milestones, open questions |

Release history and what's shipped in each version lives in [../RELEASE.md](../RELEASE.md), not here — this folder describes the system as designed, not a changelog.
