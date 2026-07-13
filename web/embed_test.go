package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIndexPageServesHTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<title>Circa — Overall</title>") {
		t.Errorf("index page missing expected title, body: %s", rec.Body.String())
	}
}

// TestEveryPageServesHTML covers the v0.6.0 per-category pages (CPU,
// Memory, Network, Disk, Filesystem, Load) and the Metrics page — each is
// its own route/template (see embed.go's pages map), not a client-side tab.
func TestEveryPageServesHTML(t *testing.T) {
	for path, wantTitle := range map[string]string{
		"/":           "Circa — Overall",
		"/cpu":        "Circa — CPU",
		"/memory":     "Circa — Memory",
		"/network":    "Circa — Network",
		"/disk":       "Circa — Disk",
		"/filesystem": "Circa — Filesystem",
		"/load":       "Circa — Load",
		"/metrics":    "Circa — Metrics",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "<title>"+wantTitle+"</title>") {
			t.Errorf("%s: missing title %q, body: %s", path, wantTitle, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `<nav class="tabs">`) {
			t.Errorf("%s: missing shared nav", path)
		}
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	for _, path := range []string{
		"/static/js/uPlot.iife.min.js",
		"/static/js/circa-chart.js",
		"/static/js/circa-data.js",
		"/static/js/app.js",
		"/static/js/overview.js",
		"/static/js/detail.js",
		"/static/css/uPlot.min.css",
		"/static/css/app.css",
		"/static/img/favicon.svg",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}

func TestUnknownStaticPathReturns404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/does-not-exist.js", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
