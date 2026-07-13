# Circa — Design 06: Alerting and AI anomaly detection

## 6.1 Alerting

- Rule = metric selector (name + label matchers) + condition (threshold, rate-of-change, or "anomaly bit is set") + duration/hysteresis (must hold for N evaluations before firing, to avoid flapping) + severity.
- Evaluate rules on the same tick as ingestion against tier-0 (raw) data for responsiveness.
- Notification dispatch as a pluggable interface: start with generic webhook + Slack incoming-webhook, add email and PagerDuty-Events-API-compatible senders next. Keep the rule engine decoupled from the transport so new channels don't touch alerting logic.
- Consider supporting Prometheus-style Alertmanager-compatible webhook payloads so this binary's alerts can be routed into an existing Alertmanager if a user already has one, rather than forcing a fork in tooling.
- Feature-flagged (`features.alerts`), default off — a pure metrics-and-dashboard deployment shouldn't pay the (small, but nonzero) cost of a rule evaluator it doesn't use.

## 6.2 AI: anomaly detection

Adopt Netdata's model closely, since it's specifically designed for exactly this constraint set (no GPU, one model per metric, cheap to run continuously):

- **Algorithm**: unsupervised k-means, k=2, run per metric. Netdata avoids deep learning models to maintain lightweight operation on any Linux system, and instead trains an unsupervised k-means model per metric on the last several hours of data, working with preprocessed feature vectors rather than raw values.
- **Scoring**: anomaly score is the Euclidean distance between the recent pattern and the learned cluster centers; if the distance exceeds a threshold based on the 99th percentile of training data, the point is flagged anomalous.
- **False-positive control**: rather than trusting one model, run multiple independent k-means models per metric trained on different time windows, and only flag an anomaly when all models agree — Netdata's production config uses 18 models per dimension, retrained every 3 hours on windows up to ~6 hours, with a 0.99 anomaly-score threshold. For a v1, fewer models (e.g. 3–5) trained on staggered windows is a reasonable starting point to keep CPU cost down, with the model count exposed as a config knob to tune later.
- **Storage cost**: don't store anomaly scores as a separate time series — steal Netdata's trick of embedding the anomaly flag as a bit inside the existing floating point storage format, so there's no additional storage overhead and the query engine can compute anomaly rates without extra queries.
- **Aggregation for triage**: compute a node-level (or subsystem-level) anomaly rate by averaging the anomaly bit across metrics in a time window, so the UI can surface "what's unusual right now" as a ranked list rather than forcing per-metric inspection — this is the pattern behind Netdata's Anomaly Advisor.
- **Known limitations to document up front** (so users calibrate trust correctly): this class of detector works best for sudden changes and struggles with gradually evolving issues where each increment looks normal, and produces no signal for a stopped service since no data means no anomalies.
- Library choice: Netdata uses `dlib` (C++). For Go, either implement k-means directly (it's a small, well-understood algorithm — a few hundred lines) or use an existing Go ML package (e.g. `gonum` for the linear algebra primitives). Given the model is intentionally simple, a from-scratch implementation is realistic and avoids a heavy dependency.
- **Feature-flagged (`features.ml`), default off.** This is the single most CPU-hungry optional subsystem (N models × M metrics, retrained periodically) — it should never run unless explicitly turned on, and the docs should state its rough CPU/RAM cost per 1,000 metrics so users can make an informed call rather than discovering the cost after the fact.
