# Circa — Design 10: Netdata's ML, and what circa replicates

This file exists to answer one question precisely: **"Netdata has 17/18 ways to detect anomalies — how many does circa implement?"** Short answer: **circa implements one algorithm** — the same one Netdata implements. "18" isn't a count of different detection algorithms; it's Netdata's own `number of models per dimension` config default (an ensemble size), and DESIGN/06 §6.2 already named this as the model to copy. This file was written by reading Netdata's actual source (`netdata/src/ml/`, gitignored, a real clone kept locally for reference — see `.gitignore`) rather than relying on secondhand summaries, to make sure circa's implementation is faithful to what Netdata really does, not just to DESIGN/06's paraphrase of it.

## 1. What Netdata actually does: one algorithm, not seventeen

Every metric ("dimension" in Netdata's terminology) gets its own **unsupervised k-means, k=2** model — full stop. There is no second algorithm, no ensemble of *different* algorithms (no isolation forest, no ARIMA, no Prophet-style seasonal decomposition, nothing supervised). The relevant source:

- `src/ml/ml_kmeans.cc` — `ml_kmeans_train` (fits k=2 via `dlib::find_clusters_using_kmeans`), `ml_kmeans_anomaly_score` (scores a point).
- `src/ml/ml_features.cc` — the preprocessing pipeline every point goes through before it reaches k-means.
- `src/ml/ml_config.cc` — every tunable, with real defaults (not just docs — read directly from the `inicfg_get_*` calls).
- `src/ml/ml.cc` — `ml_dimension_predict` (per-point scoring loop) and `ml_dimension_update_models` (the ensemble's FIFO rotation).
- `src/ml/notebooks/netdata_anomaly_detection_deepdive.ipynb` — Netdata's own didactic Python re-implementation, useful for intuition but **not** 1:1 with the C++ (e.g. its `abs()` preprocessing step and `train_every=3600` default aren't in the real agent — the C++ source and `ml_config.cc` defaults are the ground truth this document and circa's implementation were checked against).

What actually varies per metric is **not the algorithm** — it's an **ensemble of up to 18 independently-trained instances of that same algorithm**, each trained at a different point in time and retained (not overwritten) until the ensemble is full, then rotated FIFO-style (`ml.cc`'s `ml_dimension_update_models`, `dim->km_contexts`). A point only counts as anomalous if **every currently-trained model in the ensemble** flags it (`ml_dimension_predict`'s loop: the first model that scores *below* threshold short-circuits the whole check to "not anomalous"). That's the real shape of "18 ways" — 18 temporally-diverse snapshots of one algorithm voting unanimously, not 18 different techniques.

## 2. The full pipeline, stage by stage

| Stage | What Netdata does | Source |
| :--- | :--- | :--- |
| **Feature preprocessing** | Difference (order `diff_n`, default 1) → smooth (rolling mean, window `max_samples_to_smooth`, default 3) → lag (include `lag_n` previous steps in one feature vector, default 5, so each vector has 6 values) | `ml_features.cc`: `ml_features_diff`, `ml_features_smooth`, `ml_features_lag` |
| **Model** | k-means, k=2, `dlib::pick_initial_centers` + `dlib::find_clusters_using_kmeans`, up to `maximum number of k-means iterations` (default 1000) | `ml_kmeans.cc`: `ml_kmeans_train` |
| **Per-model score** | For a new vector, mean Euclidean distance to *both* centroids, then min-max normalized against the min/max of that same mean-distance metric observed during training: `100 * |mean_dist − min_dist| / (max_dist − min_dist)`, clamped to `[0,100]`. Undefined range (`max_dist == min_dist`, i.e. a perfectly constant training set) → score `0`, not "everything is anomalous." | `ml_kmeans.cc`: `ml_kmeans_anomaly_score` |
| **Per-model verdict** | `score >= dimension_anomaly_score_threshold * 100` (threshold default `0.99`) | `ml.cc`: `ml_dimension_predict` |
| **Ensemble verdict** | Unanimous: *every* currently-trained model must flag it (any one model below threshold vetoes) | `ml.cc`: `ml_dimension_predict`'s loop |
| **Ensemble composition** | Up to `number of models per dimension` (default **18**) models, trained one at a time every `train every` (default 3h) on the last `training window` (default 6h) of data, retained (not replaced) until full, then FIFO-rotated — with a fast path to swap out a redundant middle model if a newer one's window already supersedes it | `ml.cc`: `ml_dimension_update_models` |
| **Training scheduling** | A shared worker pool trains whatever's due; the time budget for one `train_every` window is divided across however many models are currently queued, spreading CPU cost rather than bursting | `ml.cc`: `ml_dimension_train_model`, the `allotted_ut` division near the queue-processing loop |
| **Anomaly bit storage** | A per-point bit, aggregated into an `anomaly_rate` chart per metric — not literally stolen from the value's float bits the way circa does it (Netdata has its own tiered rollup/chart format to carry an extra bit; circa doesn't, so it uses the LSB trick DESIGN/06 §6.2 explicitly recommends as *the* technique for a system shaped like circa's) | `ml.cc`, `ad_charts.cc` |
| **Node-level aggregation** | Average the `anomaly_rate` chart across every trained dimension over a rolling window (`anomaly detection grouping duration`, default 5m); if that average crosses `host_anomaly_rate_threshold` (default 1.0%), fire a host-wide "anomaly event" | `ad_charts.cc` (the `above_threshold` / `new_anomaly_event` logic) |
| **Per-dimension suppression** | If a dimension's been anomalous too often within a rolling window (`dimension anomaly rate suppression window`/`...threshold`, default 900s / half that), stop flagging it ("silenced") until the rate drops — keeps one perpetually-noisy metric from dominating the anomaly view forever | `ml.cc`: `dim->suppression_anomaly_counter`, `TRAINING_STATUS_SILENCED` |
| **Constant-metric shortcut** | A dimension whose value hasn't changed since the last tick is marked `METRIC_TYPE_CONSTANT` and skips training that cycle — no point re-fitting k-means on a flat line | `ml.cc`: `ml_dimension_finalize_constant_state` |
| **Scope controls** | `hosts to skip from training` / `charts to skip from training` (simple-pattern match, e.g. exclude `netdata.*` internal self-metrics by default) | `ml_config.cc` |
| **Model persistence** | Trained models are serialized to JSON and flushed to Netdata's local SQLite metadata DB (`ml_kmeans_serialize`/`deserialize`), and can stream to/from a parent node in a Netdata cluster (`from_downstream` in `ml_dimension_update_models`) | `ml_kmeans.cc`, `ml.cc` |

That table is the honest "17/18" — not seventeen algorithms, but roughly twenty distinct *mechanisms* wrapped around one algorithm: preprocessing, ensemble scheduling, scoring, node aggregation, suppression, scope filtering, and persistence. If someone said "17 ways to detect anomalies," they were very likely recalling this list (or the `number of models per dimension` default of 18) rather than a literal count of distinct algorithms.

## 3. What circa v0.4.0 implements

Circa replicates the *algorithmic core* faithfully — preprocessing, scoring formula, and unanimous-ensemble voting are line-for-line equivalent in intent to Netdata's C++ — and deliberately omits the *operational* mechanisms that exist because Netdata runs as a long-lived multi-tenant daemon streaming across a cluster, which don't have an equivalent need in circa's single-process, single-node, no-external-DB design.

| Netdata mechanism | Circa v0.4.0 | Where |
| :--- | :--- | :--- |
| Diff → smooth → lag preprocessing | **Implemented**, same order, same math | `internal/anomaly/model.go`: `Preprocess`, `diff`, `movingAverage`, `SlidingWindows` |
| k-means, k=2, deterministic init | **Implemented** (Netdata's `dlib::pick_initial_centers` is itself a variant of k-means++; circa's `initCentroids` picks a comparably-spread deterministic pair rather than dlib's exact algorithm — same goal, different implementation, since a from-scratch Go implementation was the deliberate choice for this milestone, not a `dlib` binding) | `internal/anomaly/model.go`: `Train`, `initCentroids` |
| Mean-distance-to-both-centroids, min-max normalized to `[0,100]` | **Implemented**, same formula, same degenerate-case handling (`MinDist == MaxDist` → score 0) | `internal/anomaly/model.go`: `Model.Score` |
| Per-model threshold verdict | **Implemented** (`ScoreThreshold`, default 99, matching Netdata's `0.99 * 100`) | `internal/anomaly/model.go`: `Model.IsAnomalous` |
| Unanimous ensemble voting (any model below threshold vetoes) | **Implemented**, same short-circuit logic | `internal/anomaly/detector.go`: `Detector.Score` |
| Ensemble of up to N models (default 18), FIFO-retained across genuinely different training times | **Implemented** | `internal/anomaly/detector.go`: `retrainOne`'s `append`+evict-oldest |
| Staggered training to avoid a CPU burst | **Implemented**, differently: circa fans series out across `RetrainInterval` in `retrainFanout` (12) sub-ticks, a simpler mechanism suited to a single Go process rather than Netdata's shared worker-pool queue-pacing | `internal/anomaly/detector.go`: `Run`, `retrainDueSeries` |
| Anomaly bit storage | **Implemented**, via the LSB-embedding technique DESIGN/06 §6.2 itself calls for (Netdata's own chart-tier format doesn't need this trick; circa's does) | `internal/storage/anomalybit.go` |
| Node-level anomaly-rate aggregation | **Implemented as a ranked list**, not a single host-wide threshold event: `query.Engine.AnomalyRanking` averages the anomaly bit per series over a window and ranks series by rate — DESIGN/06 §6.2's "ranked list... rather than forcing per-metric inspection." A single aggregate host-wide "anomaly event" (Netdata's `host_anomaly_rate_threshold`) is not implemented — the ranked list already serves the same triage purpose for a single-node dashboard. | `internal/query/query.go`: `AnomalyRanking` |
| Per-dimension suppression/silencing after sustained anomalies | **Not implemented.** A perpetually-noisy series will keep appearing in the ranked list indefinitely. Worth adding if this proves annoying in practice — it's a bounded, well-specified addition (`ml.cc`'s `suppression_anomaly_counter`/`TRAINING_STATUS_SILENCED` is the reference). | — |
| Constant-metric training shortcut | **Not implemented.** Circa always attempts training; `Train` still succeeds on constant data (see `Model.Score`'s `MinDist == MaxDist` handling) — it's a minor wasted training cycle, not a correctness bug, but Netdata's shortcut is cheap and worth adding later. | — |
| `hosts to skip` / `charts to skip` pattern filters | **Not implemented** — every series with `features.ml` on gets scored. Circa is single-node and has no host dimension to filter; a per-series-name exclude pattern would be the equivalent if this becomes a real need (e.g. excluding a known-noisy metric). | — |
| Model persistence to disk / cluster streaming | **Not implemented.** Circa's ensembles are in-memory only — a restart loses all trained models and starts cold (documented in `RELEASE/v0.4.0.md`). Netdata persists to SQLite and can stream models between cluster nodes; circa has neither a metadata DB nor a cluster concept in scope. | — |
| `max_training_vectors` sampling cap (limit training set size for very fine-grained data) | **Not implemented.** Circa trains on every vector `TrainingWindow` produces; fine at typical scrape intervals (15s–1m), but a sub-second-interval series with a long training window could make one retrain slow. Worth adding only if that's a real deployment shape. | — |

## 4. Config mapping

| Netdata config key (default) | Circa equivalent (default) |
| :--- | :--- |
| `training window` (6h) | `anomaly.training_window` (6h) |
| `train every` (3h) | `anomaly.retrain_interval` (3h) |
| `number of models per dimension` (18) | `anomaly.model_count` (18) |
| `num samples to diff` (1) | `anomaly.diff_n` (1) |
| `max samples to smooth` (3) | `anomaly.smooth_n` (3) |
| `num samples to lag` (5) | `anomaly.lag_n` (5) |
| `dimension anomaly score threshold` (0.99) | `anomaly.score_threshold` (99, already on the 0-100 scale `Model.Score` returns) |
| `min training window`, `max training vectors`, `delete models older than`, `host anomaly rate threshold`, `anomaly detection grouping *`, `num training threads`, `flush models batch size`, suppression window/threshold, `hosts/charts to skip` | Not exposed — either not applicable to circa's architecture (persistence, clustering, thread pool sizing) or deferred per §3 above |

## 5. Alerting is a separate, complementary mechanism

Worth stating plainly since it's easy to conflate: DESIGN/06 §6.1's rule engine (`internal/alert`) is **not** anomaly detection. Threshold and rate-of-change conditions are independent, deterministic checks against raw values; the `anomaly` condition type is the *only* place alerting touches this package, and it does so simply by reading the bit `internal/anomaly` already set (`ingest.Sample.Anomalous` → `alert.AnomalyCondition`). Netdata keeps the same separation (ML sets the anomaly bit; Netdata's own alerting/health system can reference it, but is a distinct subsystem).
