<p align="center"><img src="images/favicon.svg" width="64" height="64" alt="Circa"></p>

# Circa

A single-binary metrics aggregator with embedded storage, a built-in dashboard, alerting, and lightweight anomaly detection — no external database, no separate UI deploy, no Alertmanager required.

Circa doesn't collect metrics itself. It ingests them from whatever's already producing them — scraping any Prometheus-exposition-format exporter (node_exporter, postgres_exporter, a custom app's `/metrics`, or an existing Prometheus server's `/federate`), accepting Telegraf via InfluxDB line protocol, and accepting/sending Prometheus remote-write — stores them locally in a fixed-size round-robin structure (constant disk footprint, Gorilla-style compression), and serves a static-HTML dashboard directly from the binary. Everything past ingestion + storage + `/metrics` is optional and feature-flagged off by default: alerting, k-means anomaly detection, and scheduled backup into an Iceberg lake.

See [DESIGN.md](DESIGN.md) for the full design, [ARCHITECTURE.md](ARCHITECTURE.md) for how the code is (intended to be) organized, and [RELEASE.md](RELEASE.md) for what's actually shipped so far.

> **Status**: the Quickstart below runs against real code, not a future promise — see [RELEASE.md](RELEASE.md) for exactly what's shipped vs. still planned per version.

## Quickstart

### Option 1 — go run (zero setup)

```bash
make config-init            # copies config.example.yaml -> config.yaml
go run ./cmd/circa -config config.yaml
```

Visit `http://localhost:9100` for the dashboard. Metrics are stored locally under `storage.path` from your config (default `/var/lib/circa/data`, override it in `config.yaml` for local runs).

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

Or generate one instead of copying the example: `circa config init` (add `--profile full` to turn every feature flag on for a demo/eval run, or `--hostname`/`--listen`/`--retention.*` for targeted overrides). Validate a config before rolling it out with `circa config check config.yaml` — it reports every problem found (bad cross-field combination, missing required field for an enabled feature), not just the first.

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

### Auth

No authentication by default. To require login, add a user — this bcrypt-hashes the password and writes it into `auth.users` in place (comments elsewhere in the file are preserved):

```bash
circa auth add-user admin -config config.yaml         # prompts for a password (not echoed)
circa auth reset-password admin -config config.yaml   # change an existing user's password
circa auth hash-password                               # just prints a bcrypt hash — paste it into auth.users yourself
```

`hash-password` doesn't touch any file — use it when you'd rather edit `config.yaml` by hand (or generate hashes for multiple config files, or in CI) instead of letting `add-user`/`reset-password` write to a specific file for you.

Every user in `auth.users` gets full access — this is authentication, not per-user authorization (see [DESIGN/08](DESIGN/08_design_config_auth_ops.md) §8.2.1). `GET /healthz`/`/readyz` stay open even with auth on, for liveness/readiness probes; everything else, including the dashboard and `GET /status` (the effective config, secrets redacted), requires it once any user exists.

### Alerting & anomaly detection

Both are feature-flagged off by default. Turn on `features.alerts` and add rules/notifiers under `alerting:` (see [config.example.yaml](config.example.yaml) for a worked example — threshold, rate-of-change, and anomaly-bit conditions, webhook and Slack notifiers). Turn on `features.ml` and tune `anomaly:` if you want something other than the defaults, which mirror [Netdata's own](DESIGN/10_ml_summary.md) (an 18-model k-means ensemble per metric, retrained every 3h on a 6h window).

```bash
circa config check config.yaml   # catches a rule referencing an unknown notifier, an
                                  # anomaly condition with features.ml off, bad bounds, etc.
```

The dashboard shows a live Alerts panel and a "what's unusual right now" ranked panel; both are also available as JSON via `GET /api/v1/alerts` and `GET /api/v1/anomalies?window=<seconds>` (default window: 10 minutes).

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
- Rule-based alerting (threshold, rate-of-change, anomaly-bit conditions) with pluggable notifiers — webhook and Slack today, more channels are a small addition behind the same interface.
- Per-metric k-means anomaly detection matched against Netdata's real implementation (not just its design docs — see [DESIGN/10](DESIGN/10_ml_summary.md)), anomaly bit embedded in storage (no extra time series).
- Watermark-driven CDC export into an Iceberg lake, push or pull mode, so cross-node "federation" is just a SQL query against a shared table.

## Security scanning

`.github/workflows/security.yml` runs on every `v*` tag push and weekly on a schedule:

- **Semgrep** (`p/golang`, `p/sql-injection`, `p/secrets`, `p/owasp-top-ten`) — SAST.
- **Trivy** — filesystem scan (Go module CVEs) and a container image scan of the built Dockerfile. Both fail the build on CRITICAL/HIGH findings; results also upload as SARIF to the repo's Security tab.

## Releasing

```bash
make release VERSION=0.3.0   # bumps VERSION + helm/k8s image tags, pushes, tags, pushes tag
```

Pushing a `v*` tag triggers `.github/workflows/release.yml`: tests gate the build, then a cross-platform binary matrix (linux/darwin/windows × amd64/arm64) is built and attached to a GitHub Release, and a multi-arch (amd64/arm64) Docker image is built and pushed to GHCR.

## License

[Apache 2.0](LICENSE).
