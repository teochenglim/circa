//go:build linux

package collect

// Unlike linux_test.go's fixture-based parser tests (runnable from any
// dev machine), this exercises the real /proc/*, unix.Statfs, and
// unix.Uname calls in linux.go against whatever kernel is actually running
// — meaningful in CI (ubuntu-latest, see .github/workflows/ci.yml) and in
// a local Linux container, not from the primary macOS dev loop. See
// RELEASE/v0.5.0.md's "Dev/test loop" note.

import (
	"testing"
	"time"
)

func TestSupportedLinux(t *testing.T) {
	if !Supported() {
		t.Error("Supported() should report true on linux")
	}
}

func TestCollectAllLinux(t *testing.T) {
	samples, err := collectAll(time.Now(), 15*time.Second)
	if err != nil {
		t.Fatalf("collectAll: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("collectAll returned no samples")
	}

	names := make(map[string]bool, len(samples))
	for _, s := range samples {
		names[s.Name] = true
	}
	for _, want := range []string{
		"node_cpu_seconds_total", "node_memory_MemTotal_bytes",
		"node_load1", "node_uname_info", "node_network_receive_bytes_total",
	} {
		if !names[want] {
			t.Errorf("missing expected metric %q among %d samples", want, len(samples))
		}
	}
}
