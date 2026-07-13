package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/teochenglim/circa/internal/alert"
)

// Slack posts to a Slack incoming-webhook URL (DESIGN/06 §6.1), formatting
// the Alert as the short one-line message Slack's `text` field expects.
type Slack struct {
	WebhookURL string
	Client     *http.Client
}

func NewSlack(webhookURL string) *Slack {
	return &Slack{WebhookURL: webhookURL, Client: &http.Client{Timeout: 10 * time.Second}}
}

func (s *Slack) Notify(a alert.Alert) error {
	payload := map[string]string{"text": formatMessage(a)}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling slack payload: %w", err)
	}
	resp, err := s.Client.Post(s.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack webhook returned %s", resp.Status)
	}
	return nil
}

func formatMessage(a alert.Alert) string {
	verb := "FIRING"
	emoji := "🔴"
	if !a.Firing {
		verb = "RESOLVED"
		emoji = "✅"
	}
	msg := fmt.Sprintf("%s *%s* [%s] %s%s = %g", emoji, verb, a.Severity, a.Metric, labelSuffix(a.Labels), a.Value)
	if a.Firing {
		msg += fmt.Sprintf(" (since %s)", a.Since.Format("15:04:05"))
	}
	return msg
}

func labelSuffix(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)
	s := "{"
	for i, k := range names {
		if i > 0 {
			s += ","
		}
		s += k + "=" + labels[k]
	}
	return s + "}"
}
