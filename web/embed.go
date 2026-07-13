// Package web embeds circa's static dashboard (plain HTML/CSS/JS, uPlot
// charts) via go:embed, per DESIGN/05 — no Node.js runtime, no separate
// frontend deploy artifact.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

//go:embed template/index.html
var templateFS embed.FS

var indexTemplate = template.Must(template.ParseFS(templateFS, "template/index.html"))

// Handler serves the dashboard's static assets under /static/ and the
// index page at /.
func Handler() http.Handler {
	mux := http.NewServeMux()

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // static/ is embedded at build time; failing to find it is a build bug, not a runtime condition
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTemplate.Execute(w, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	return mux
}
