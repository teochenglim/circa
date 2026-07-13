# Release History

`cmd/circa` landed with v0.1.0, UI + compression with v0.2.0 — see [ARCHITECTURE.md](ARCHITECTURE.md) for the as-built layout. The table below tracks the *planned* release scope per [DESIGN/09](DESIGN/09_design_tech_stack_and_roadmap.md) §9.2's milestone list, so progress becomes traceable as each one is actually built.

Newest first.

| Version | Theme | Status |
| :--- | :--- | :--- |
| [v1.0.0](RELEASE/v1.0.0.md) | Hardening, sizing calculator, packaging, docs | Not started |
| [v0.5.0](RELEASE/v0.5.0.md) | Federation & self-expose (backup CDC to Iceberg, federation search, `/metrics`) | Not started |
| [v0.4.0](RELEASE/v0.4.0.md) | Intelligence (alerting, anomaly detection) | Not started |
| [v0.3.0](RELEASE/v0.3.0.md) | Operability (config & CLI, auth, push/pull remote-write ingestion) | Not started |
| [v0.2.0](RELEASE/v0.2.0.md) | Visibility (UI, compression) | **Shipped** |
| [v0.1.0](RELEASE/v0.1.0.md) | Core (scrape ingestion + storage + `/api/v1/query_range`) | **Shipped** |

Each version file lists the planned scope (from [DESIGN/09](DESIGN/09_design_tech_stack_and_roadmap.md) §9.2) and, once built, what actually shipped vs. was deferred, plus any real bugs found and how they were fixed — useful context for future changes in that area. Don't backfill a file's "Built" section until the milestone is actually done; an aspirational status here is worse than no status at all.
