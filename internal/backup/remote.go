package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RemoteSource is the pull-mode DeltaSource: the central backup agent
// (cmd/circa's `backup-agent` role) polls GET <BaseURL>/api/v1/backup_range
// on each configured node instead of reading a local query.Engine —
// DESIGN/07 §7.3's "only the central backup agent needs Iceberg/S3
// credentials," individual nodes need only be reachable inbound.
type RemoteSource struct {
	BaseURL  string
	Username string // optional, for a node with auth.users set
	Password string
	Client   *http.Client
}

func NewRemoteSource(baseURL, username, password string) *RemoteSource {
	return &RemoteSource{
		BaseURL:  baseURL,
		Username: username,
		Password: password,
		Client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *RemoteSource) DeltaRange(ctx context.Context, since time.Time) ([]Row, time.Time, error) {
	u := s.BaseURL + "/api/v1/backup_range?since=" + url.QueryEscape(strconv.FormatInt(since.Unix(), 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, since, fmt.Errorf("building request: %w", err)
	}
	if s.Username != "" {
		req.SetBasicAuth(s.Username, s.Password)
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, since, fmt.Errorf("GET %s: %w", s.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, since, fmt.Errorf("GET %s: unexpected status %s", s.BaseURL, resp.Status)
	}

	var out DeltaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, since, fmt.Errorf("decoding response from %s: %w", s.BaseURL, err)
	}
	return out.Rows, out.Watermark, nil
}
