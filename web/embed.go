// Package web embeds circa's static dashboard (plain HTML/CSS/JS, uPlot
// charts) via go:embed, per DESIGN/05 — no Node.js runtime, no separate
// frontend deploy artifact.
//
// v0.6.0 split the dashboard from one page into several — Overall (index),
// one per built-in collector category (CPU/Memory/Network/Disk/Filesystem/
// Load), and Metrics (the original manual metric-picker chart + Alerts/
// anomalies panels) — a real Netdata-style tab-per-page layout rather than
// one page with JS-toggled sections, so each page only loads the JS/data it
// actually needs and each has its own bookmarkable URL. v1.0.0 added one
// more: Self-metrics, visualizing circa's own RED self-metrics (v0.7.0)
// polled live from GET /api/v1/selfmetrics rather than read back out of
// internal/storage — see web/static/js/selfmetrics.js's doc comment.
// nav.html's `{{define "nav"}}` block is shared across all of them.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

//go:embed template
var templateFS embed.FS

var pageTemplate = template.Must(template.ParseFS(templateFS, "template/*.html"))

// pageData is what every page template executes against — currently just
// which nav entry to mark active (see nav.html).
type pageData struct {
	Page string
}

// pages maps each route to its template file (see template/*.html) and the
// nav entry it should highlight.
var pages = map[string]string{
	"GET /{$}":          "index.html",
	"GET /cpu":          "cpu.html",
	"GET /memory":       "memory.html",
	"GET /network":      "network.html",
	"GET /disk":         "disk.html",
	"GET /filesystem":   "filesystem.html",
	"GET /load":         "load.html",
	"GET /metrics":      "metrics.html",
	"GET /self-metrics": "selfmetrics.html",
}

// pageName strips the .html suffix from a template file name for pageData.Page
// (e.g. "cpu.html" -> "cpu"); index.html is special-cased to "overall" since
// nav.html's link for it is "/", not "/index".
func pageName(templateFile string) string {
	name := templateFile[:len(templateFile)-len(".html")]
	if name == "index" {
		return "overall"
	}
	return name
}

// Handler serves the dashboard's static assets under /static/ and every
// dashboard page (see pages above).
func Handler() http.Handler {
	mux := http.NewServeMux()

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // static/ is embedded at build time; failing to find it is a build bug, not a runtime condition
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	for route, file := range pages {
		mux.HandleFunc(route, pageHandler(file))
	}

	return mux
}

func pageHandler(templateFile string) http.HandlerFunc {
	data := pageData{Page: pageName(templateFile)}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := pageTemplate.ExecuteTemplate(w, templateFile, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
