// Package backup implements DESIGN/07's CDC-style delta export into an
// Iceberg lake — v0.7.0. It's watermark-driven (not WAL-tailing or a real
// CDC connector): each run reads every series' tier-1 points since the
// last watermark, batches them into an Arrow record, and appends that as a
// new Iceberg snapshot via github.com/apache/iceberg-go, advancing the
// watermark only after the commit succeeds.
//
// Push and pull mode (DESIGN/07 §7.3) share this same mechanism — the only
// difference is which side initiates the Iceberg write: in push mode,
// Exporter calls CollectDelta directly against the local query.Engine; in
// pull mode, a node instead serves CollectDelta's own output over
// GET /api/v1/backup_range (internal/httpapi), and a separately-run
// Exporter (cmd/circa's `backup-agent` role) polls that endpoint via
// remoteSource (remote.go) instead of a local one. Either way, Exporter
// only ever sees a DeltaSource — it doesn't know or care which mode
// produced it.
package backup

import (
	"context"
	"encoding/json"
	"time"
)

// Row is one exported sample. Mirrors DESIGN/07 §7.4's schema, with two
// deliberate departures:
//   - No env/region columns — nothing in Circa's config populates those
//     today; adding speculative columns nobody writes to isn't worth the
//     schema complexity. Add them if a real need shows up.
//   - Labels travel as a JSON string (see iceberg.go's icebergSchema), not
//     a native Iceberg map column — simpler to build with Arrow's builder
//     API and just as queryable via DuckDB/Trino's JSON functions, without
//     depending on iceberg-go's map-type write support.
type Row struct {
	NodeID     string            `json:"node_id"`
	Hostname   string            `json:"hostname"`
	MetricName string            `json:"metric_name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Time       time.Time         `json:"time"`
	Value      float64           `json:"value"`
	// Anomalous is always false — tier-1 rollups have no single anomaly
	// verdict of their own (see internal/storage/anomalybit.go and
	// ARCHITECTURE.md), and DESIGN/07 §7.2 recommends exporting tier-1
	// specifically for lower churn. The column is kept in the schema
	// per §7.4 rather than dropped, since a future raw-tier export path
	// could populate it meaningfully.
	Anomalous bool `json:"anomalous"`
}

// DeltaResponse is GET /api/v1/backup_range's JSON wire shape — kept in
// this package (not internal/httpapi) so the pull-mode HTTP handler and
// remoteSource's client always agree on one Go type instead of two
// independently hand-written JSON shapes that could drift apart.
type DeltaResponse struct {
	Rows      []Row     `json:"rows"`
	Watermark time.Time `json:"watermark"`
}

// DeltaSource produces every Row observed after since, plus the new
// watermark to persist if the read succeeds (normally the latest Row's
// timestamp, or since unchanged if there were no new rows) — decoupling
// Exporter from whether that delta came from a local query.Engine (push
// mode) or an HTTP pull against another node (pull mode).
type DeltaSource interface {
	DeltaRange(ctx context.Context, since time.Time) ([]Row, time.Time, error)
}

// labelsJSON always returns a JSON object, never the literal "null" —
// json.Marshal(map[string]string(nil)) encodes a nil map as "null", which
// would otherwise make an unlabeled series' labels_json column ambiguous
// with a real encoding failure.
func labelsJSON(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return "{}"
	}
	return string(b)
}
