package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teochenglim/circa/internal/alert"
)

func TestWebhookPostsJSON(t *testing.T) {
	var received alert.Alert
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w := NewWebhook(server.URL)
	a := alert.Alert{RuleName: "high_cpu", Metric: "cpu", Severity: alert.SeverityWarning, Value: 95, Firing: true}
	if err := w.Notify(a); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if received.RuleName != "high_cpu" || received.Value != 95 {
		t.Errorf("received = %+v", received)
	}
}

func TestWebhookReturnsErrorOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	w := NewWebhook(server.URL)
	if err := w.Notify(alert.Alert{}); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestSlackFormatsMessage(t *testing.T) {
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := NewSlack(server.URL)
	a := alert.Alert{
		RuleName: "high_cpu",
		Metric:   "cpu",
		Labels:   map[string]string{"host": "node1"},
		Severity: alert.SeverityCritical,
		Value:    99.5,
		Since:    time.Now(),
		Firing:   true,
	}
	if err := s.Notify(a); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	text := payload["text"]
	if !strings.Contains(text, "FIRING") || !strings.Contains(text, "cpu") || !strings.Contains(text, "host=node1") {
		t.Errorf("unexpected slack message: %q", text)
	}

	a.Firing = false
	if err := s.Notify(a); err != nil {
		t.Fatalf("Notify (resolved): %v", err)
	}
	if !strings.Contains(payload["text"], "RESOLVED") {
		t.Errorf("expected RESOLVED in resolved message, got %q", payload["text"])
	}
}
