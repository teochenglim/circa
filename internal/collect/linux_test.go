package collect

// Fixtures below are captured verbatim from a real Linux container
// (`docker run --rm alpine:latest cat /proc/...`), not hand-typed guesses —
// except procDiskstatsFixture, which is synthesized with plausible nonzero
// values on the real field layout (the container's own loop devices were
// all-zero, not a useful test case).

import (
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

func TestParseProcStat(t *testing.T) {
	data := []byte(`cpu  1915763 0 1002874 66545958 11757 0 36039 0 0 0
cpu0 239982 0 125952 8304139 1871 0 14262 0 0 0
cpu1 239296 0 125781 8315832 1834 0 5981 0 0 0
`)
	samples := parseProcStat(data, fixedNow, time.Second)

	// Aggregate "cpu " line skipped; 2 CPUs x 8 modes = 16 samples.
	if len(samples) != 16 {
		t.Fatalf("got %d samples, want 16", len(samples))
	}
	for _, s := range samples {
		if s.Name != "node_cpu_seconds_total" {
			t.Errorf("unexpected metric name %q", s.Name)
		}
		if s.Labels["cpu"] == "" {
			t.Error("missing cpu label")
		}
	}

	// cpu0 user = 239982 ticks / 100 HZ = 2399.82s
	found := false
	for _, s := range samples {
		if s.Labels["cpu"] == "0" && s.Labels["mode"] == "user" {
			found = true
			if s.Value != 2399.82 {
				t.Errorf("cpu0 user = %v, want 2399.82", s.Value)
			}
		}
	}
	if !found {
		t.Fatal("cpu0 user sample not found")
	}
}

func TestParseMemInfo(t *testing.T) {
	data := []byte(`MemTotal:       24556968 kB
MemFree:          992904 kB
MemAvailable:   22086740 kB
Buffers:          867468 kB
Cached:         19103028 kB
SwapCached:            0 kB
Active:          3903744 kB
Inactive:       17905268 kB
Active(anon):    1839776 kB
Inactive(anon):       32 kB
`)
	samples := parseMemInfo(data, fixedNow, time.Second)

	byName := make(map[string]float64)
	for _, s := range samples {
		byName[s.Name] = s.Value
	}
	if want := 24556968.0 * 1024; byName["node_memory_MemTotal_bytes"] != want {
		t.Errorf("MemTotal = %v, want %v", byName["node_memory_MemTotal_bytes"], want)
	}
	if _, ok := byName["node_memory_SwapTotal_bytes"]; ok {
		t.Error("SwapTotal wasn't in the fixture, shouldn't produce a sample")
	}
	// Active(anon) isn't in meminfoFields (only bare "Active" is) — the
	// substring match must not accidentally pick it up.
	if v := byName["node_memory_Active_bytes"]; v != 3903744*1024 {
		t.Errorf("Active = %v, want %v", v, 3903744*1024)
	}
}

var procDiskstatsFixture = []byte(`   7       0 loop0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
   8       0 sda 15234 320 987654 41200 9821 512 654321 33100 0 28900 74300 0 0 0 0 0
`)

func TestParseDiskstats(t *testing.T) {
	samples := parseDiskstats(procDiskstatsFixture, fixedNow, time.Second)

	// loop0 skipped, sda produces 4 samples.
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4 (loop0 should be skipped)", len(samples))
	}
	byName := make(map[string]float64)
	for _, s := range samples {
		if s.Labels["device"] != "sda" {
			t.Errorf("unexpected device %q", s.Labels["device"])
		}
		byName[s.Name] = s.Value
	}
	if byName["node_disk_reads_completed_total"] != 15234 {
		t.Errorf("reads_completed = %v, want 15234", byName["node_disk_reads_completed_total"])
	}
	if want := 987654.0 * 512; byName["node_disk_read_bytes_total"] != want {
		t.Errorf("read_bytes = %v, want %v", byName["node_disk_read_bytes_total"], want)
	}
}

func TestParseNetDev(t *testing.T) {
	data := []byte(`Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0:     200       2    0    0    0     0          0         0       42       1    0    0    0     0       0          0
`)
	samples := parseNetDev(data, fixedNow, time.Second)

	// 2 interfaces x 4 metrics = 8.
	if len(samples) != 8 {
		t.Fatalf("got %d samples, want 8", len(samples))
	}
	for _, s := range samples {
		if s.Labels["device"] == "eth0" && s.Name == "node_network_receive_bytes_total" && s.Value != 200 {
			t.Errorf("eth0 receive_bytes = %v, want 200", s.Value)
		}
	}
}

func TestParseLoadavg(t *testing.T) {
	samples := parseLoadavg([]byte("0.07 0.21 0.32 2/439 9\n"), fixedNow, time.Second)
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3", len(samples))
	}
	if samples[0].Name != "node_load1" || samples[0].Value != 0.07 {
		t.Errorf("node_load1 = %+v", samples[0])
	}
	if samples[2].Name != "node_load15" || samples[2].Value != 0.32 {
		t.Errorf("node_load15 = %+v", samples[2])
	}
}

func TestParseDefaultRouteIfaceLinux(t *testing.T) {
	data := []byte("Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0205A8C0\t0003\t0\t0\t200\t00000000\t0\t0\t0\n" +
		"cni0\t00002A0A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n")

	iface := parseDefaultRouteIfaceLinux(data)
	if iface != "eth0" {
		t.Errorf("got %q, want eth0", iface)
	}
}

func TestParseDefaultRouteIfaceLinux_NoDefault(t *testing.T) {
	data := []byte("Iface\tDestination\tGateway\n" +
		"cni0\t00002A0A\t00000000\n")
	if iface := parseDefaultRouteIfaceLinux(data); iface != "" {
		t.Errorf("got %q, want empty (no default route in fixture)", iface)
	}
}

func TestParseMounts(t *testing.T) {
	data := []byte(`overlay / overlay rw,relatime 0 0
proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
tmpfs /dev tmpfs rw,nosuid,size=65536k,mode=755,inode64 0 0
sysfs /sys sysfs ro,nosuid,nodev,noexec,relatime 0 0
cgroup /sys/fs/cgroup cgroup2 ro,nosuid,nodev,noexec,relatime 0 0
`)
	mounts := parseMounts(data)

	// proc/sysfs/cgroup2 filtered out; overlay is also filtered (virtual
	// container root), tmpfs is kept (real capacity-bounded storage).
	if len(mounts) != 1 {
		t.Fatalf("got %d mounts, want 1 (tmpfs only): %+v", len(mounts), mounts)
	}
	if mounts[0].Mountpoint != "/dev" || mounts[0].Fstype != "tmpfs" {
		t.Errorf("got %+v", mounts[0])
	}
}
