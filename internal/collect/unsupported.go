//go:build !linux && !darwin

package collect

// Every other GOOS — Windows most notably — is deferred to v1.1.0 (see
// RELEASE/v1.1.0.md and RELEASE/v0.5.0.md's platform-scope note). Supported
// reports false here so cmd/circa can log a clear "not available on this
// platform" message instead of a config default silently collecting
// nothing, the way an empty scrape target list does.

import (
	"fmt"
	"runtime"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

func Supported() bool { return false }

func collectAll(now time.Time, interval time.Duration) ([]ingest.Sample, error) {
	return nil, fmt.Errorf("built-in system collection is not implemented for GOOS=%s (planned for v1.1.0)", runtime.GOOS)
}
