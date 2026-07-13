# helm/circa

Templated equivalent of the plain manifests in [../../k8s/](../../k8s/) — same DaemonSet shape (one Circa pod per node, self-monitoring via a read-only `/proc` bind mount, headless `Service`), values-driven instead of hand-edited. Each pod reads its own node's `/proc` through `HOST_PROC=/host/proc` (built-in collector, `internal/collect`, v0.5.0+) — zero-config, no co-located node_exporter required; `ingest.scrape.targets` is for *additional* exporters now, not a prerequisite. See [../../ARCHITECTURE.md](../../ARCHITECTURE.md) "Deployment shape" for why this is a DaemonSet and not a `Deployment`.

## Install

```bash
helm upgrade --install circa helm/circa
```

## Configuring

Override [values.yaml](values.yaml) rather than editing the chart. Two common overrides:

```bash
# Point at a real image tag once one is published (see RELEASE.md):
helm upgrade --install circa helm/circa --set image.tag=0.1.0

# Enable a feature flag:
helm upgrade --install circa helm/circa --set features.alerts=true
```

For secrets (alert webhook URLs, backup S3 credentials, bcrypt auth hashes), don't put real values in `values.yaml` or on the command line where they'd land in shell history — use a separate, gitignored file:

```bash
cp values.yaml values-secrets.yaml   # then edit values-secrets.yaml with real secrets.* / auth.users entries
helm upgrade --install circa helm/circa -f values-secrets.yaml
```

## Values reference

| Key | Default | Notes |
| :--- | :--- | :--- |
| `image.repository` / `image.tag` | `circa` / `latest` | Set `image.tag` to a real released version before using this outside a local demo |
| `listenAddress` | `:9100` | Same convention as node_exporter's default port, on the same node |
| `features.collect` | `true` | Built-in self-monitoring (v0.5.0+) — the one feature on by default, since circa is a self-monitoring single binary, not a scrape-and-store shell around some other exporter. See [../../RELEASE/v0.5.0.md](../../RELEASE/v0.5.0.md) |
| `features.*` (everything else) | all `false` | Mirrors `config.example.yaml`'s `features` block — see [../../DESIGN/08_design_config_auth_ops.md](../../DESIGN/08_design_config_auth_ops.md) §8.1.3 |
| `ingest.collect.interval` | `15s` | How often the built-in collector samples the local host |
| `ingest.scrape.targets` | `[]` | Empty by default — self-collection already covers this node. Add entries for *additional* co-located exporters or other hosts, see [../../DESIGN/04_design_collection_and_ingestion.md](../../DESIGN/04_design_collection_and_ingestion.md) §4.2 |
| `storage.hostPath` | `/var/lib/circa/data` | Node-local; survives pod restarts on the same node, lost if the node is replaced — see [../../DESIGN/07_design_backup.md](../../DESIGN/07_design_backup.md) for why long-term durability is the backup feature's job, not this volume's |
| `storage.retention.*` | `2h` / `7d` / `365d` | Per-tier retention, see [../../DESIGN/03_design_storage.md](../../DESIGN/03_design_storage.md) |
| `auth.users` | `{}` (no auth) | Map of `username: bcrypt-hash` — set via a secrets values file, never inline |
| `secrets.*` | empty | Alert webhook URL, backup S3 credentials — same rule as above |
| `hostNetwork` | `true` | So Circa can scrape a co-located exporter over `localhost` and its own `/metrics` is reachable at `<node-ip>:9100` without an extra hop |

## Linting / rendering locally

```bash
make helm-lint       # helm lint helm/circa
make helm-template    # helm template circa helm/circa  (no cluster needed)
```
