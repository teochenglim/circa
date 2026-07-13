// Package notify holds concrete alert.Notifier implementations. Adding a
// channel means adding a file here that implements alert.Notifier — never
// touch internal/alert's rule-evaluation logic (ARCHITECTURE.md "Adding a
// new alert notifier").
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/teochenglim/circa/internal/alert"
)

// Webhook POSTs the Alert as JSON to a generic endpoint — the simplest,
// most interoperable notifier (DESIGN/06 §6.1).
type Webhook struct {
	URL    string
	Client *http.Client
}

func NewWebhook(url string) *Webhook {
	return &Webhook{URL: url, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (w *Webhook) Notify(a alert.Alert) error {
	body, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshaling alert: %w", err)
	}
	resp, err := w.Client.Post(w.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook %s returned %s", w.URL, resp.Status)
	}
	return nil
}
