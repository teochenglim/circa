// Package httpapi wires circa's HTTP routes. v0.1.0 only serves
// /api/v1/query_range plus /healthz and /readyz (the k8s DaemonSet manifests
// already probe /healthz, so it ships now rather than waiting for a later
// milestone). /metrics, the dashboard, /status, and the write receivers all
// arrive in later milestones per RELEASE.md.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/teochenglim/circa/internal/query"
)

// NewRouter builds the HTTP handler for circa's API surface.
func NewRouter(engine *query.Engine) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/query_range", queryRangeHandler(engine))
	mux.HandleFunc("GET /healthz", healthzHandler)
	mux.HandleFunc("GET /readyz", healthzHandler)
	return mux
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

type apiSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
}

// queryRangeHandler serves GET /api/v1/query_range?metric=<name>&labels=k=v,k=v&start=<ts>&end=<ts>.
// Naming and response shape deliberately echo Prometheus's own HTTP API
// (DESIGN/05 §5) though matching is exact-label-equality only for now — no
// PromQL, no regex matchers, no step-based downsampling (tier-1/2 arrive in
// v0.2.0).
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

		results, err := engine.QueryRange(metric, match, query.Range{Start: start, End: end})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		data := apiData{ResultType: "matrix", Result: make([]apiSeries, 0, len(results))}
		for _, res := range results {
			values := make([][2]any, 0, len(res.Points))
			for _, p := range res.Points {
				values = append(values, [2]any{
					float64(p.Time.UnixNano()) / 1e9,
					strconv.FormatFloat(p.Value, 'g', -1, 64),
				})
			}
			data.Result = append(data.Result, apiSeries{Metric: res.Metric, Values: values})
		}

		writeJSON(w, http.StatusOK, apiResponse{Status: "success", Data: &data})
	}
}

// parseTime accepts a Unix timestamp (seconds, may be fractional) or RFC3339
// — the same two formats Prometheus's own HTTP API accepts.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, &timeParseError{"start/end is required"}
	}
	if sec, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Unix(0, int64(sec*float64(time.Second))), nil
	}
	return time.Parse(time.RFC3339, s)
}

type timeParseError struct{ msg string }

func (e *timeParseError) Error() string { return e.msg }

// parseLabels parses "k1=v1,k2=v2" into an exact-match filter map.
func parseLabels(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	match := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, &timeParseError{"expected k=v, got " + pair}
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
