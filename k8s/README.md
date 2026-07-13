# k8s/ manifests

Circa self-monitors: each pod reads its own node's `/proc` (via a read-only `HOST_PROC=/host/proc` bind mount, see `20-daemonset.yaml`) using the built-in collector (`internal/collect`, v0.5.0+) — zero-config, no co-located node_exporter required. `ingest.scrape.targets` in `10-configmap.yaml` is still there for *additional* exporters, it's just no longer a prerequisite for a pod to report anything. It's still a per-node local agent, so it deploys as a **DaemonSet** (one pod per node), the same shape `node_exporter`'s own official Helm chart uses, not a `Deployment` behind a `Service`/`Ingress` the way a stateless multi-replica web app would. See [../ARCHITECTURE.md](../ARCHITECTURE.md) "Deployment shape" and [../DESIGN/05_design_ui.md](../DESIGN/05_design_ui.md) (per-node local dashboard, not a centralized multi-host view).

Apply in order (or `kubectl apply -f k8s/` applies all at once, numbering just controls readability):

| File | Purpose |
| :--- | :--- |
| `10-configmap.yaml` | Non-secret `config.yaml` content, mounted into the pod |
| `11-secret.example.yaml` | **Example only** — copy to `11-secret.yaml`, fill in real bcrypt hashes / webhook tokens, keep out of git (see `k8s/.gitignore`) |
| `20-daemonset.yaml` | One Circa pod per node: `hostNetwork`, a read-only `/proc` bind mount for self-collection, Downward API env vars (`POD_NAME`/`POD_NAMESPACE`/`NODE_NAME`) tagging every self-collected sample with its origin, unprivileged `securityContext` |
| `30-service.yaml` | Headless `ClusterIP: None` service — DNS-based per-pod discovery for a central backup agent (pull-mode CDC export, see [DESIGN/07](../DESIGN/07_design_backup.md) §7.3) or a Prometheus scrape config, not a load-balanced frontend |

There's deliberately no `Ingress`/`HPA` here: a DaemonSet has exactly one pod per node by design (no horizontal scaling dimension to autoscale), and each pod's dashboard is a *per-node* view, not something worth fronting behind a single load-balanced hostname — visit each node's pod directly, or point Prometheus/Grafana at all of them via the headless service.

If you'd rather manage this as a chart, see [../helm/circa/](../helm/circa/README.md) instead — same shape, templated and values-driven.
