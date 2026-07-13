# Circa — Design Index

The full design has been split into `DESIGN/` so each concern is easy to find and keep current as the system evolves. Start here:

1. [DESIGN/01_design_overview.md](DESIGN/01_design_overview.md) — problem statement, prior art (node_exporter, RRDtool, Netdata, Prometheus), and the decisions each one suggests
2. [DESIGN/02_design_architecture.md](DESIGN/02_design_architecture.md) — high-level architecture diagram
3. [DESIGN/03_design_storage.md](DESIGN/03_design_storage.md) — the RRD-like storage engine: tiers, Gorilla-style compression, footprint
4. [DESIGN/04_design_collection_and_ingestion.md](DESIGN/04_design_collection_and_ingestion.md) — ingesting from any exporter, Telegraf, and remote-write: scrape, InfluxDB line protocol, push
5. [DESIGN/05_design_ui.md](DESIGN/05_design_ui.md) — the embedded static HTML dashboard
6. [DESIGN/06_design_alerting_and_anomaly_detection.md](DESIGN/06_design_alerting_and_anomaly_detection.md) — alert rule engine and k-means anomaly detection
7. [DESIGN/07_design_backup.md](DESIGN/07_design_backup.md) — CDC-style export into an Iceberg lake, push/pull, federation
8. [DESIGN/08_design_config_auth_ops.md](DESIGN/08_design_config_auth_ops.md) — single-YAML config, feature flags, auth, operability
9. [DESIGN/09_design_tech_stack_and_roadmap.md](DESIGN/09_design_tech_stack_and_roadmap.md) — tech stack, milestones, open questions

Related docs:
- [README.md](README.md) — how to run it
- [ARCHITECTURE.md](ARCHITECTURE.md) — how the code is (intended to be) organized, and how a collection tick flows through it
- [RELEASE.md](RELEASE.md) — version history and roadmap
- [CLAUDE.md](CLAUDE.md) — where things go in this repo, for anyone (human or agent) working on it
