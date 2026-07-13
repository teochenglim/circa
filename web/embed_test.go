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
	if !strings.Contains(rec.Body.String(), "<title>Circa</title>") {
		t.Errorf("index page missing expected title, body: %s", rec.Body.String())
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	for _, path := range []string{
		"/static/js/uPlot.iife.min.js",
		"/static/js/app.js",
		"/static/css/uPlot.min.css",
		"/static/css/app.css",
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
