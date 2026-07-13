//go:build darwin

package collect

// Unlike darwin_test.go's fixture-based parser tests, this exercises the
// real top/vm_stat/sysctl/netstat/route subprocesses and the real
// Getfsstat/Uname syscalls in darwin.go — meaningful precisely because this
// repo's primary dev loop *is* macOS (see RELEASE/v0.5.0.md), so this is
// the one platform integration tests can validate live on every run.

import (
	"testing"
	"time"
)

func TestSupportedDarwin(t *testing.T) {
	if !Supported() {
		t.Error("Supported() should report true on darwin")
	}
}

func TestCollectAllDarwin(t *testing.T) {
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
		"node_cpu_usage_percent", "node_memory_total_bytes",
		"node_load1", "node_uname_info", "node_filesystem_size_bytes",
	} {
		if !names[want] {
			t.Errorf("missing expected metric %q among %d samples", want, len(samples))
		}
	}
}
