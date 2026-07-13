# Release History

`cmd/circa` landed with v0.1.0, UI + compression with v0.2.0, config/auth/push ingestion with v0.3.0, alerting + anomaly detection with v0.4.0, built-in self-collection with v0.5.0, a Netdata-style tabbed dashboard with v0.6.0, Iceberg backup + `/metrics` + self-metrics with v0.7.0 — see [ARCHITECTURE.md](ARCHITECTURE.md) for the as-built layout. Each `RELEASE/vX.Y.Z.md` file below is the single source of truth for that milestone's planned scope, cross-referencing the relevant `DESIGN/*.md` docs directly rather than duplicating a milestone list here or in DESIGN/09.

Newest first.

| Version | Theme | Status |
| :--- | :--- | :--- |
| [v1.1.0](RELEASE/v1.1.0.md) | Windows (and other non-Linux/non-macOS) system collection | Not started |
| [v1.0.0](RELEASE/v1.0.0.md) | Hardening, sizing calculator, packaging, docs | Not started |
| [v0.7.0](RELEASE/v0.7.0.md) | Federation & self-expose (backup CDC to Iceberg, federation search, `/metrics`, self-metrics) | **Shipped** |
| [v0.6.0](RELEASE/v0.6.0.md) | Netdata-style dashboard (zero-config system overview) | **Shipped** |
| [v0.5.0](RELEASE/v0.5.0.md) | Built-in system collection, Linux + macOS (`/proc`/`/sys`/`sysctl`, node_exporter/Netdata-referenced) | **Shipped** |
| [v0.4.0](RELEASE/v0.4.0.md) | Intelligence (alerting, anomaly detection) | **Shipped** |
| [v0.3.0](RELEASE/v0.3.0.md) | Operability (config & CLI, auth, push/pull remote-write ingestion) | **Shipped** |
| [v0.2.0](RELEASE/v0.2.0.md) | Visibility (UI, compression) | **Shipped** |
| [v0.1.0](RELEASE/v0.1.0.md) | Core (scrape ingestion + storage + `/api/v1/query_range`) | **Shipped** |

Each version file lists its own planned scope and, once built, what actually shipped vs. was deferred, plus any real bugs found and how they were fixed — useful context for future changes in that area. Don't backfill a file's "Built" section until the milestone is actually done; an aspirational status here is worse than no status at all.
