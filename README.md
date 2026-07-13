<p align="center"><img src="images/favicon.svg" width="64" height="64" alt="Circa"></p>

# Circa

A single-binary metrics aggregator with embedded storage, a built-in dashboard, alerting, and lightweight anomaly detection — no external database, no separate UI deploy, no Alertmanager required.

Circa doesn't collect metrics itself. It ingests them from whatever's already producing them — scraping any Prometheus-exposition-format exporter (node_exporter, postgres_exporter, a custom app's `/metrics`, or an existing Prometheus server's `/federate`), accepting Telegraf via InfluxDB line protocol, and accepting/sending Prometheus remote-write — stores them locally in a fixed-size round-robin structure (constant disk footprint, Gorilla-style compression), and serves a static-HTML dashboard directly from the binary. Everything past ingestion + storage + `/metrics` is optional and feature-flagged off by default: alerting, k-means anomaly detection, and scheduled backup into an Iceberg lake.

See [DESIGN.md](DESIGN.md) for the full design, [ARCHITECTURE.md](ARCHITECTURE.md) for how the code is (intended to be) organized, and [RELEASE.md](RELEASE.md) for what's actually shipped so far.

> **Status**: v0.1.0 shipped — scrape ingestion, tier-0 storage, and `/api/v1/query_range` work end to end; the Quickstart below runs against real code. UI, compression, config CLI, auth, push/pull ingestion, alerting, anomaly detection, and backup are still planned — see [RELEASE.md](RELEASE.md) for what's shipped vs. upcoming per version.

## Quickstart

### Option 1 — go run (zero setup)

```bash
make config-init            # copies config.example.yaml -> config.yaml
go run ./cmd/circa -config config.yaml
```

Visit `http://localhost:9100`. Metrics are stored locally under `storage.path` from your config (default `/var/lib/circa/data`, override it in `config.yaml` for local runs).

### Option 2 — Docker Compose

```bash
make up      # docker-compose.yaml: single container, local volume for RRD data
```

### Option 3 — Kubernetes (DaemonSet)

Circa is a per-node agent, not a stateless web app, so it deploys as a **DaemonSet** — one pod per node, scraping that node's co-located exporter(s) (e.g. node_exporter) over `localhost`. See [k8s/README.md](k8s/README.md) for the manifests, or use the Helm chart instead:

```bash
helm upgrade --install circa helm/circa
```

See [helm/circa/README.md](helm/circa/README.md) for chart values.

Run `make` with no arguments to see every available target.

## Configuration

Everything lives in one YAML file (env vars aren't the primary interface — see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.1 for why). Start from [config.example.yaml](config.example.yaml):

```bash
cp config.example.yaml config.yaml
circa -config config.yaml
```

| Section | Controls |
| :--- | :--- |
| `server` | Listen address, optional TLS |
| `features` | Master on/off switches: `ml`, `alerts`, `backup`, `push_receive`, `push_send` — all off by default |
| `ingest` | Scrape target list, InfluxDB line-protocol receiver settings — only read if the matching `features.*` flag is true (scrape itself always runs) |
| `storage` | Local data path, per-tier retention (`raw`/`minute`/`hour`) |
| `alerting` | Rules + notifiers — only read if `features.alerts` is true |
| `backup` | Iceberg catalog/warehouse, push vs. pull mode — only read if `features.backup` is true |
| `push` | Remote-write receive/send endpoints — only read if the matching `features.push_*` is true |
| `auth` | Bcrypt-hashed users for basic auth; empty/absent = no auth |

Full field-by-field rationale is in [DESIGN/08_design_config_auth_ops.md](DESIGN/08_design_config_auth_ops.md).

## Development

```bash
make test          # go test ./... -race -cover
make vet            # go vet ./...
make fmt             # gofmt
make build           # binary in ./bin
```

`pre-commit install` wires up local Semgrep + Trivy scans (see `.pre-commit-config.yaml`) so security findings surface before you push, not just in CI.

## Feature highlights (see [DESIGN.md](DESIGN.md) for full rationale on each)

- Ingestion via existing wire protocols (Prometheus scrape, InfluxDB line protocol, Prometheus remote-write) — no `/proc`/`/sys` collection code to maintain, and any exporter or Telegraf agent works unmodified.
- Fixed-size round-robin storage (RRD-style tiers) with Gorilla-style delta+XOR compression — constant disk footprint regardless of how long Circa runs.
- Embedded static dashboard (uPlot charts, `go:embed`) — no Node.js, no separate frontend deploy.
- Prometheus remote-write push **and** pull ingestion, both feature-flagged off by default.
- Rule-based alerting with pluggable notifiers (webhook, Slack, email, PagerDuty-compatible).
- Per-metric k-means anomaly detection, anomaly bit embedded in storage (no extra time series).
- Watermark-driven CDC export into an Iceberg lake, push or pull mode, so cross-node "federation" is just a SQL query against a shared table.

## Security scanning

`.github/workflows/security.yml` runs on every `v*` tag push and weekly on a schedule:

- **Semgrep** (`p/golang`, `p/sql-injection`, `p/secrets`, `p/owasp-top-ten`) — SAST.
- **Trivy** — filesystem scan (Go module CVEs) and a container image scan of the built Dockerfile. Both fail the build on CRITICAL/HIGH findings; results also upload as SARIF to the repo's Security tab.

## Releasing

```bash
make release VERSION=0.2.0   # bumps VERSION + helm/k8s image tags, pushes, tags, pushes tag
```

Pushing a `v*` tag triggers `.github/workflows/release.yml`: tests gate the build, then a cross-platform binary matrix (linux/darwin/windows × amd64/arm64) is built and attached to a GitHub Release, and a multi-arch (amd64/arm64) Docker image is built and pushed to GHCR.

## License

[Apache 2.0](LICENSE).
