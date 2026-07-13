package httpapi

import (
	"net/http"
	"time"

	"github.com/teochenglim/circa/internal/backup"
	"github.com/teochenglim/circa/internal/query"
)

// backupRangeHandler serves GET /api/v1/backup_range?since=<unix_ts> — the
// pull-mode delta endpoint (DESIGN/07 §7.3), only registered when
// features.backup and backup.mode=="pull" (see NewRouter). Reuses
// backup.CollectDelta, the exact function push mode's own Exporter calls
// in-process, so both modes agree on precisely what "everything since
// since" means. since is optional; an absent/empty value means "from the
// beginning" (a fresh pull-mode agent's very first poll of this node).
func backupRangeHandler(engine *query.Engine, nodeID, hostname string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			parsed, err := parseTime(s)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid since: "+err.Error())
				return
			}
			since = parsed
		}

		rows, watermark := backup.CollectDelta(engine, nodeID, hostname, since, time.Now())
		writeJSON(w, http.StatusOK, backup.DeltaResponse{Rows: rows, Watermark: watermark})
	}
}
