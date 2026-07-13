# Circa — Design 04: Metrics ingestion

## 4.1 A wrapper, not a fork — pull from any exporter, plus Telegraf, plus remote-write

Circa does not collect metrics itself and does not import or wrap `node_exporter/collector`. It sits **in front of** whatever's already producing metrics on a host — node_exporter, postgres_exporter, blackbox_exporter, cAdvisor, a custom app's `/metrics`, a Telegraf agent — and adds storage, UI, alerting, and ML on top. That's the same relationship Prometheus itself has to node_exporter today, generalized to more sources and built into one binary instead of split across Prometheus + Grafana + Alertmanager.

This is a deliberate reversal from earlier drafts of this design, which had Circa import `node_exporter/collector` directly to gather its own local metrics. Wrapping that package still would have meant taking on `/proc`/`/sys` parsing, platform-specific build tags, and a second copy of node_exporter's own release cadence as a dependency — for a benefit (avoiding one extra scrape hop) that's marginal once scraping localhost is nearly free. Treating every metrics source as "just another scrape target, line-protocol sender, or remote-write client" instead means:

- Circa's own code stays a thin target list plus three small protocol decoders, not a fork of any single agent's internals — it isn't tied to node_exporter's flags, its kingpin dependency, or its config shape at all.
- Anything that already speaks Prometheus exposition format, InfluxDB line protocol, or Prometheus remote-write works with Circa unmodified — no output-plugin change, no custom shim.
- Circa can bolt durable storage, a dashboard, and alerting onto infrastructure someone already runs (including an existing Prometheus server's own `/federate` endpoint) without touching that deployment.

All three ingestion paths below feed the same internal pipeline (RRD store, ML engine, alert engine) — one code path per protocol, fanned out to the same consumers, each consumer independently feature-flagged ([08](08_design_config_auth_ops.md) §8.1.3). Whatever Circa ingests is also re-exposed on its own `/metrics` in Prometheus exposition format, so a Circa instance can itself be scraped or federated from — chaining is a first-class shape, not a special case.

## 4.2 Scrape: pull from any Prometheus-exposition-format endpoint

- Configured as a target list (`ingest.scrape.targets`, each with a URL, interval, and optional labels) — the same shape as Prometheus's `scrape_configs`, deliberately, so anyone who's written a Prometheus config already knows this one.
- Parsed with `prometheus/common/expfmt`'s standard text parser — one code path handles node_exporter, postgres_exporter, blackbox_exporter, cAdvisor, a hand-rolled app `/metrics`, or Telegraf's own `outputs.prometheus_client` plugin. Circa never needs to know or care what's on the other end.
- Scraping an existing Prometheus server's `/federate` endpoint is just another target — this is how a Circa instance can front an existing Prometheus deployment without requiring it to change anything.
- This is the zero-config-mechanism, opt-in-by-content path: the scrape client itself always ships in the baseline binary (no feature flag gates the mechanism), but with an empty target list Circa collects nothing, matching Prometheus's own "empty `scrape_configs` does nothing" behavior rather than assuming a local node_exporter is always present.
- The node_exporter `textfile` collector convention — external scripts dropping `.prom` files into a directory for injection — isn't reimplemented; it's inherited for free by scraping a locally-run node_exporter (or any exporter) that already has `textfile` enabled. Circa doesn't need its own version of this mechanism.

## 4.3 InfluxDB line protocol: Telegraf without an output-plugin change

- Accept InfluxDB line protocol directly via a `/write` (and `/api/v2/write`) endpoint, for Telegraf fleets already pointed at an InfluxDB-compatible target via `outputs.influxdb` or `outputs.http` — repointing at Circa is a URL swap, not a Telegraf config rewrite.
- Map `measurement,tags fields` → `{measurement}_{field}` series with tags carried through as labels — the same convention VictoriaMetrics and `prometheus/influxdb_exporter` already use, so nothing new to learn if you've mapped line protocol to Prometheus-shaped series before.
- Sanitize field names into valid Prometheus metric names on ingest; drop (don't zero-coerce) non-numeric fields, since silently turning a string field into `0` would corrupt downstream storage, alerting, and anomaly detection without any signal that it happened.
- Feature-flagged (`features.influx_receive`), off by default — this is an opt-in ingestion path for Telegraf shops, not something a plain node_exporter/Prometheus user needs to think about.

## 4.4 Remote-write: receive and send

The default and simplest mode is scrape (§4.2), but supporting Prometheus remote-write as well is genuinely useful for nodes that can't be scraped inbound — behind NAT, in restricted/segmented networks (a real scenario for public-sector/DSTA-style deployments), or short-lived jobs that don't live long enough to be scraped.

### 4.4.1 Push receiver (accept incoming pushes)

- Expose an `/api/v1/write` endpoint implementing the **Prometheus remote-write wire format**: the request body is a Protocol Buffers message compressed with Snappy's block format, sent via HTTP POST, using either the original `prometheus.WriteRequest` message from the 1.0 spec (for backward compatibility) or the newer `io.prometheus.write.v2.Request`. This is the one thing node_exporter itself has never supported in either direction — every existing "push metrics out" setup today requires bolting on a separate Prometheus/Grafana Alloy/OTel Collector in front of it just to get a remote-write leg. Folding receive directly into Circa removes that extra hop entirely.
- Decode with `golang/snappy` + the `prompb`/`writev2` generated types (the same libraries Prometheus itself uses), feed decoded samples into the same ingest pipeline as scraped and line-protocol-received metrics. Any existing remote-write-compatible sender — Prometheus, Grafana Alloy, Vector, OpenTelemetry Collector — can push into Circa unmodified.
- Useful deployment shape: a small number of Circa instances run in "receiver" mode (feature-flagged on) on reachable hosts, acting as a lightweight aggregation point for many push-only agents elsewhere — similar in spirit to how Thanos Receive or Mimir's distributor accepts remote-write, but without adopting their full architecture.

### 4.4.2 Push sender (forward ingested metrics onward)

- Symmetric feature: Circa can periodically batch everything it has ingested (from scrape, line protocol, or its own receiver) and push it, in the same remote-write wire format, to an external Prometheus/Mimir/VictoriaMetrics/Thanos-receive endpoint — or to another Circa instance running in receiver mode. This is the escape hatch for topologies where a central system needs the data but can't reach out and scrape this instance itself.
- Both directions are independent feature flags (`features.push_receive`, `features.push_send`), default off — scraping remains the zero-config default, matching node_exporter's own pull-first posture and keeping the lean baseline intact.
