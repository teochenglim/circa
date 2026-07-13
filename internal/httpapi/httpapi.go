// Package httpapi wires circa's HTTP routes: /api/v1/query_range,
// /api/v1/series, /api/v1/alerts, /api/v1/anomalies, /api/v1/selfmetrics,
// /metrics, /status, /healthz, /readyz, the dashboard (/, /static/*) from
// the web package, and — when enabled — the remote-write receiver and
// (pull-mode backup) /api/v1/backup_range.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/teochenglim/circa/internal/alert"
	"github.com/teochenglim/circa/internal/auth"
	"github.com/teochenglim/circa/internal/config"
	"github.com/teochenglim/circa/internal/query"
	"github.com/teochenglim/circa/internal/storage"
	"github.com/teochenglim/circa/web"
)

// defaultAnomalyWindow is how far back GET /api/v1/anomalies looks by
// default — recent enough to mean "right now" (DESIGN/06 §6.2's "what's
// unusual right now"), overridable per-request with ?window=<seconds>.
const defaultAnomalyWindow = 10 * time.Minute

// Options configures the optional parts of the router beyond the always-on
// query/series/status/healthz/dashboard routes.
type Options struct {
	// Config is the effective, merged config — used to render /status
	// (redacted, per DESIGN/08 §8.3) and to gate auth. Zero value behaves
	// like config.Default() with no auth users, matching config.Default().
	Config config.Config
	// WriteReceiver serves POST <Config.Push.Receive.Path> when non-nil —
	// wire it up only when features.push_receive is on (DESIGN/04 §4.4.1).
	WriteReceiver http.Handler
	// AlertEngine backs GET /api/v1/alerts when non-nil (features.alerts
	// on); nil means the endpoint always returns an empty list.
	AlertEngine *alert.Engine
	// NodeID and Hostname identify this node in GET /api/v1/backup_range's
	// output (DESIGN/07 §7.4) — only meaningful, and only registered, when
	// Config.Features.Backup and Config.Backup.Mode == "pull".
	NodeID   string
	Hostname string
}

// NewRouter builds the HTTP handler for circa's API surface and dashboard.
// /healthz and /readyz are always served unauthenticated (liveness/readiness
// probes shouldn't need credentials); everything else is wrapped in basic
// auth per opts.Config.Auth.Users — an empty/nil map disables auth entirely,
// matching the no-auth default (DESIGN/08 §8.2.1).
func NewRouter(engine *query.Engine, opts Options) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/v1/query_range", queryRangeHandler(engine))
	protected.HandleFunc("GET /api/v1/series", seriesHandler(engine))
	protected.HandleFunc("GET /api/v1/alerts", alertsHandler(opts.AlertEngine))
	protected.HandleFunc("GET /api/v1/anomalies", anomaliesHandler(engine))
	protected.HandleFunc("GET /metrics", metricsHandler(engine))
	protected.HandleFunc("GET /api/v1/selfmetrics", selfMetricsHandler())
	protected.HandleFunc("GET /status", statusHandler(opts.Config))
	if opts.WriteReceiver != nil {
		path := opts.Config.Push.Receive.Path
		if path == "" {
			path = config.DefaultPushReceivePath
		}
		protected.Handle("POST "+path, opts.WriteReceiver)
	}
	if opts.Config.Features.Backup && opts.Config.Backup.Mode == "pull" {
		protected.HandleFunc("GET /api/v1/backup_range", backupRangeHandler(engine, opts.NodeID, opts.Hostname))
	}
	protected.Handle("/", web.Handler())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler)
	mux.HandleFunc("GET /readyz", healthzHandler)
	mux.Handle("/", auth.Middleware(opts.Config.Auth.Users, protected))

	// instrumentHTTP wraps everything, including /healthz/readyz and
	// anything auth rejects, so circa's own RED self-metrics (selfmetrics.go)
	// reflect every request circa actually receives.
	return instrumentHTTP(mux)
}

// alertsHandler serves GET /api/v1/alerts — every currently-firing alert.
// A nil engine (features.alerts off) always returns an empty list rather
// than 404, so the UI's Alerts panel doesn't need to special-case the flag.
func alertsHandler(engine *alert.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var alerts []alert.Alert
		if engine != nil {
			alerts = engine.Alerts()
		}
		writeJSON(w, http.StatusOK, struct {
			Status string        `json:"status"`
			Data   []alert.Alert `json:"data"`
		}{Status: "success", Data: alerts})
	}
}

// anomaliesHandler serves GET /api/v1/anomalies?window=<seconds> — the
// ranked "what's unusual right now" list (DESIGN/06 §6.2). Empty whenever
// features.ml is off, since nothing will have set the anomaly bit.
func anomaliesHandler(engine *query.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := defaultAnomalyWindow
		if s := r.URL.Query().Get("window"); s != "" {
			sec, err := strconv.ParseFloat(s, 64)
			if err != nil || sec <= 0 {
				writeError(w, http.StatusBadRequest, "invalid window: "+s)
				return
			}
			window = time.Duration(sec * float64(time.Second))
		}
		ranks := engine.AnomalyRanking(window, time.Now())
		writeJSON(w, http.StatusOK, struct {
			Status string              `json:"status"`
			Data   []query.AnomalyRank `json:"data"`
		}{Status: "success", Data: ranks})
	}
}

// statusHandler serves GET /status — the effective merged config as
// read-only YAML, secrets redacted (DESIGN/08 §8.3).
func statusHandler(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := yaml.Marshal(cfg.Redacted())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(out)
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

type apiResponse struct {
	Status string   `json:"status"`
	Data   *apiData `json:"data,omitempty"`
	Error  string   `json:"error,omitempty"`
}

type apiData struct {
	ResultType string      `json:"resultType"`
	Result     []apiSeries `json:"result"`
}

// apiSeries mirrors Prometheus's matrix result shape for Values (raw/tier-0).
// Min/Max are only populated for tier=minute|hour, alongside Values holding
// the bucket average — so a tier-0 client that ignores unknown fields sees
// the same shape it always has.
type apiSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
	Min    [][2]any          `json:"min,omitempty"`
	Max    [][2]any          `json:"max,omitempty"`
}

// queryRangeHandler serves GET /api/v1/query_range?metric=<name>&labels=k=v,k=v&start=<ts>&end=<ts>&tier=raw|minute|hour.
// Naming and response shape deliberately echo Prometheus's own HTTP API
// (DESIGN/05 §5) though matching is exact-label-equality only for now — no
// PromQL, no regex matchers.
func queryRangeHandler(engine *query.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		metric := r.URL.Query().Get("metric")
		if metric == "" {
			writeError(w, http.StatusBadRequest, "metric is required")
			return
		}

		start, err := parseTime(r.URL.Query().Get("start"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid start: "+err.Error())
			return
		}
		end, err := parseTime(r.URL.Query().Get("end"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid end: "+err.Error())
			return
		}
		if end.Before(start) {
			writeError(w, http.StatusBadRequest, "end must not be before start")
			return
		}

		match, err := parseLabels(r.URL.Query().Get("labels"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid labels: "+err.Error())
			return
		}

		tier, err := parseTier(r.URL.Query().Get("tier"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		results, aggResults, err := engine.QueryRange(metric, match, tier, query.Range{Start: start, End: end})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		data := apiData{ResultType: "matrix"}
		if tier == storage.TierRaw {
			data.Result = make([]apiSeries, 0, len(results))
			for _, res := range results {
				data.Result = append(data.Result, apiSeries{Metric: res.Metric, Values: pointValues(res.Points)})
			}
		} else {
			data.Result = make([]apiSeries, 0, len(aggResults))
			for _, res := range aggResults {
				values := make([][2]any, 0, len(res.Points))
				mins := make([][2]any, 0, len(res.Points))
				maxes := make([][2]any, 0, len(res.Points))
				for _, p := range res.Points {
					ts := float64(p.Time.Unix())
					values = append(values, [2]any{ts, formatFloat(p.Avg)})
					mins = append(mins, [2]any{ts, formatFloat(p.Min)})
					maxes = append(maxes, [2]any{ts, formatFloat(p.Max)})
				}
				data.Result = append(data.Result, apiSeries{Metric: res.Metric, Values: values, Min: mins, Max: maxes})
			}
		}

		writeJSON(w, http.StatusOK, apiResponse{Status: "success", Data: &data})
	}
}

// seriesHandler serves GET /api/v1/series — every currently-known series'
// name and labels, so the dashboard can populate a metric picker without
// the caller needing to already know what exists.
func seriesHandler(engine *query.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type seriesEntry struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels,omitempty"`
		}
		keys := engine.Series()
		entries := make([]seriesEntry, 0, len(keys))
		for _, k := range keys {
			entries = append(entries, seriesEntry{Name: k.Name, Labels: k.Labels})
		}
		writeJSON(w, http.StatusOK, struct {
			Status string        `json:"status"`
			Data   []seriesEntry `json:"data"`
		}{Status: "success", Data: entries})
	}
}

func pointValues(points []storage.Point) [][2]any {
	values := make([][2]any, 0, len(points))
	for _, p := range points {
		values = append(values, [2]any{float64(p.Time.UnixNano()) / 1e9, formatFloat(p.Value)})
	}
	return values
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// parseTime accepts a Unix timestamp (seconds, may be fractional) or RFC3339
// — the same two formats Prometheus's own HTTP API accepts.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, &apiError{"start/end is required"}
	}
	if sec, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Unix(0, int64(sec*float64(time.Second))), nil
	}
	return time.Parse(time.RFC3339, s)
}

// parseTier maps the tier query param to a storage.Tier; empty defaults to raw.
func parseTier(s string) (storage.Tier, error) {
	switch s {
	case "", "raw":
		return storage.TierRaw, nil
	case "minute":
		return storage.TierMinute, nil
	case "hour":
		return storage.TierHour, nil
	default:
		return storage.TierRaw, &apiError{"invalid tier " + s + ": want raw, minute, or hour"}
	}
}

type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

// parseLabels parses "k1=v1,k2=v2" into an exact-match filter map.
func parseLabels(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	match := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, &apiError{"expected k=v, got " + pair}
		}
		match[k] = v
	}
	return match, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiResponse{Status: "error", Error: msg})
}
