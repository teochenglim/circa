# Circa — Design 09: Tech stack, milestones, and open questions

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

## 9.2 Suggested milestones

1. **v0.1.0 — core (ingestion + storage)**: scrape client against a configured target list (`prometheus/common/expfmt`); tier-0 round-robin ring buffer in memory + disk persistence; `/api/v1/query_range`. No compression, no UI, no `/metrics` yet. Validates that treating every source as a scrape target is sufficient, and that the storage model holds what gets scraped, before any UI/alerting/backup work depends on either.
2. **v0.2.0 — visibility (UI + compression)**: static dashboard embedded via `go:embed`, uPlot charts wired to the query API, multi-tier downsampling (min/avg/max rollups); swap raw storage for Gorilla-style delta+XOR encoding, re-measured against the v0.1.0 baseline.
3. **v0.3.0 — operability (config, auth, push/pull ingestion)**: single YAML config with feature flags, `circa config init`/`check`, `/status` page; no-auth default with bcrypt multi-user basic auth, `circa auth add-user`/`reset-password`; `/api/v1/write` receiver and outbound push sender, both feature-flagged.
4. **v0.4.0 — intelligence (alerting + anomaly detection)**: rule engine + webhook/Slack notifier, alerts view in UI, feature-flagged; per-metric k-means (start with 3–5 models per metric), anomaly bit in storage, "what's unusual" view in UI, feature-flagged.
5. **v0.5.0 — built-in system collection (`/proc`/`/sys`, Linux + macOS)**: a second reversal of §4.1's "wrapper, not a fork" decision — an in-binary collector reading `/proc`/`/sys` on Linux and `sysctl`/`host_statistics` on macOS (cpu, meminfo, diskstats, filesystem, netdev, loadavg), feeding the same ingest pipeline as scrape/influx/remote-write, so a fresh install has real host metrics with zero external exporter required. Windows and every other non-Linux/non-macOS target deferred to **v1.1.0** (see below) — no `/proc`/`sysctl` equivalent on Windows; that's `windows_exporter`'s WMI-based territory, a separate codebase/license to evaluate on its own. Node_exporter and netdata are reference material only — read for parsing edge cases and coverage, never imported or vendored, even though node_exporter's Apache-2.0 license would permit it (netdata's GPL-3.0 wouldn't). Requires updating §4.1, ARCHITECTURE.md, and CLAUDE.md's existing "don't add a `/proc`/`/sys` collector" guidance as part of the work, not after. Exists specifically to unblock milestone 6 below.
6. **v0.6.0 — Netdata-style dashboard (zero-config system overview)**: redesign the UI around a Netdata-like local-agent layout (system overview, dense auto-updating chart grid) for whatever v0.5.0's built-in collector produces, with no scrape target or manual chart config required. Depends on v0.5.0 directly; existing per-scrape-target grouping and the v0.4.0 alert/anomaly panels stay as-is alongside it.
7. **v0.7.0 — federation & self-expose (backup, federation search, `/metrics`)**: watermark-driven CDC export to Iceberg (Arrow/Parquet batching, push and pull modes, feature-flagged); confirm cross-node queries work via plain SQL (DuckDB/Trino) against the shared table; wire up `/metrics` in Prometheus exposition format. Deliberately last — this milestone is about Circa's data leaving the node or being re-exposed to another scraper, which matters less than the node's own storage/UI/alerting/intelligence/zero-config collection.
8. **v1.0.0**: hardening, sizing calculator, packaging (systemd unit, Docker image, Helm chart), docs — including explicit resource-cost notes per feature flag.
9. **v1.1.0 — Windows (and other non-Linux/non-macOS) system collection**: extend v0.5.0's built-in collector to Windows, referencing `windows_exporter` (WMI-based, MIT-licensed — a separate codebase from node_exporter, reference-only under the same policy as §9.2 item 5) the same way node_exporter/netdata were referenced for Linux/macOS. Any other target OS (*BSD, etc.) evaluated at the same time if there's real demand. Deliberately post-1.0: v1.0.0 is a hardening/packaging pass over what v0.1.0–v0.7.0 already ship, not a vehicle for new platform coverage.

Tracked status per milestone lives in [../RELEASE.md](../RELEASE.md) → `RELEASE/`, not here.

## 9.3 Open questions to resolve before/while building

- Retention/tier defaults — mirror Netdata's (2h raw / days at 1min / a year at 1hr) or size differently for this project's typical host count?
- Should alert rule definitions and ML config live inline in the single YAML (as sketched in [08](08_design_config_auth_ops.md)), or is a separate rules file worth it once rule sets get large — trading "single file" simplicity for easier diffing/review at scale?
- Multi-tenant/multi-host dashboard, or is this strictly a per-node local dashboard (closer to netdata's local agent, not netdata Cloud)? This interacts with whether RBAC is ever worth revisiting ([08 §8.2.3](08_design_config_auth_ops.md)).
- Export cadence for the CDC job — every few minutes, hourly? Affects both freshness of the federation view and batch size/cost.
- Default backup mode — push or pull? [07 §7.3](07_design_backup.md) leans toward pull for fleets given credential centralization, but push is simpler for a single standalone instance; worth deciding a sensible out-of-the-box default either way.
- Shared Iceberg catalog/bucket, or does each deployment bring its own?
- `apache/iceberg-go` write support is actively evolving — worth re-checking at implementation time whether it's caught up, versus committing to the community `go-iceberg` fork now.
- Is basic auth ([08 §8.2.1](08_design_config_auth_ops.md)) sufficient long-term, or will there be real pressure for RBAC/SSO despite the recommendation in [08 §8.2.3](08_design_config_auth_ops.md) to push that to a reverse proxy instead?
- Push receiver (`/api/v1/write`) auth: should incoming pushes be subject to the same basic-auth config as the UI, or does it need a separate bearer-token/API-key scheme more typical of machine-to-machine remote-write senders?
- Zero-config onboarding: since Circa no longer collects local metrics itself ([04](04_design_collection_and_ingestion.md)), a fresh install with an empty target list shows nothing until the user points it at a node_exporter (or other exporter) — worth deciding whether `circa config init` should default to scraping `localhost:9100` (assuming a co-located node_exporter) or stay genuinely empty and require an explicit first target.
