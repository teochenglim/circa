package collect

// Pure parsing functions for every /proc file this package reads — no
// build tag, no file I/O, so they're testable with inline fixtures from any
// dev machine (this repo's primary dev loop is macOS; see
// RELEASE/v0.5.0.md's "Dev/test loop" note). linux.go is the thin
// build-tagged layer that actually reads these files and calls into here.
//
// Field layouts checked against a real kernel's /proc output (via a local
// Linux container, not guessed) and against node_exporter's *_linux.go for
// edge cases — see this package's doc comment for the reuse policy.

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/teochenglim/circa/internal/ingest"
)

// linuxClockTicksPerSecond is Linux's USER_HZ — the unit /proc/stat's CPU
// counters are expressed in. It's compile-time-fixed at 100 on every
// mainstream Linux build (x86, arm, arm64); node_exporter itself hardcodes
// the same assumption rather than calling sysconf(_SC_CLK_TCK) per read.
const linuxClockTicksPerSecond = 100

// cpuStatModes is the fixed column order after the "cpuN" field in
// /proc/stat. guest/guest_nice (columns 9-10, present on newer kernels) are
// deliberately not exposed as separate modes — the kernel already counts
// them inside user/nice, so surfacing them too would double-count time in
// any mode-summed chart.
var cpuStatModes = []string{"user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal"}

// parseProcStat turns /proc/stat's per-CPU lines into node_cpu_seconds_total
// counters. The aggregate "cpu " line (no number) is skipped — it's a sum
// of the per-cpu lines a chart/query can compute itself.
func parseProcStat(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	var out []ingest.Sample
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 1+len(cpuStatModes) {
			continue
		}
		cpu := fields[0]
		if !strings.HasPrefix(cpu, "cpu") || cpu == "cpu" {
			continue // "cpu" (no suffix) is the aggregate line; anything else isn't a CPU line at all
		}
		cpuLabel := strings.TrimPrefix(cpu, "cpu")

		for i, mode := range cpuStatModes {
			ticks, err := strconv.ParseUint(fields[1+i], 10, 64)
			if err != nil {
				continue
			}
			seconds := float64(ticks) / linuxClockTicksPerSecond
			out = append(out, sample("node_cpu_seconds_total",
				map[string]string{"cpu": cpuLabel, "mode": mode}, seconds, now, interval))
		}
	}
	return out
}

// meminfoFields maps the /proc/meminfo keys this package surfaces to the
// node_memory_<Key>_bytes metric name suffix — a curated subset of
// node_exporter's ~50 fields, easy to extend later, not an attempt at full
// parity on day one.
var meminfoFields = []string{
	"MemTotal", "MemFree", "MemAvailable", "Buffers", "Cached",
	"SwapTotal", "SwapFree", "Active", "Inactive",
}

// parseMemInfo turns /proc/meminfo's "Key:   12345 kB" lines into
// node_memory_<Key>_bytes gauges for the fields in meminfoFields.
func parseMemInfo(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[key] = kb
	}

	var out []ingest.Sample
	for _, key := range meminfoFields {
		kb, ok := values[key]
		if !ok {
			continue
		}
		out = append(out, sample("node_memory_"+key+"_bytes", nil, float64(kb)*1024, now, interval))
	}
	return out
}

// diskstatsSkipPrefix excludes virtual block devices with no real I/O of
// their own — loopback-mounted images and ramdisks — matching the spirit
// (not the exact regex) of node_exporter's default ignored-devices filter.
func diskstatsSkipPrefix(name string) bool {
	return strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram")
}

// diskSectorBytes is the kernel's fixed sector size for /proc/diskstats
// accounting purposes — always 512, regardless of the device's actual
// physical block size (a well-documented /proc/diskstats quirk).
const diskSectorBytes = 512

// parseDiskstats turns /proc/diskstats rows into node_disk_* counters.
// Only the first 11 fields (present since the format's introduction) are
// read; newer kernels append discard/flush fields this package doesn't
// surface yet.
func parseDiskstats(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	var out []ingest.Sample
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue // major minor name + at least 11 stat fields
		}
		name := fields[2]
		if diskstatsSkipPrefix(name) {
			continue
		}

		readsCompleted, err1 := strconv.ParseUint(fields[3], 10, 64)
		sectorsRead, err2 := strconv.ParseUint(fields[5], 10, 64)
		writesCompleted, err3 := strconv.ParseUint(fields[7], 10, 64)
		sectorsWritten, err4 := strconv.ParseUint(fields[9], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}

		labels := map[string]string{"device": name}
		out = append(out,
			sample("node_disk_reads_completed_total", labels, float64(readsCompleted), now, interval),
			sample("node_disk_read_bytes_total", labels, float64(sectorsRead*diskSectorBytes), now, interval),
			sample("node_disk_writes_completed_total", labels, float64(writesCompleted), now, interval),
			sample("node_disk_written_bytes_total", labels, float64(sectorsWritten*diskSectorBytes), now, interval),
		)
	}
	return out
}

// parseNetDev turns /proc/net/dev's two-line-header table into
// node_network_* counters, one set per interface (every interface,
// including loopback — this package doesn't filter network devices, unlike
// diskstats' loop/ram skip, since there's no equivalent "obviously not a
// real interface" heuristic for network devices).
func parseNetDev(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	var out []ingest.Sample
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // "Inter-|   Receive ..." / " face |bytes    packets ..." header
		}
		line := scanner.Text()
		iface, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		iface = strings.TrimSpace(iface)
		fields := strings.Fields(rest)
		if len(fields) < 10 {
			continue
		}
		rxBytes, err1 := strconv.ParseUint(fields[0], 10, 64)
		rxPackets, err2 := strconv.ParseUint(fields[1], 10, 64)
		txBytes, err3 := strconv.ParseUint(fields[8], 10, 64)
		txPackets, err4 := strconv.ParseUint(fields[9], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}

		labels := map[string]string{"device": iface}
		out = append(out,
			sample("node_network_receive_bytes_total", labels, float64(rxBytes), now, interval),
			sample("node_network_receive_packets_total", labels, float64(rxPackets), now, interval),
			sample("node_network_transmit_bytes_total", labels, float64(txBytes), now, interval),
			sample("node_network_transmit_packets_total", labels, float64(txPackets), now, interval),
		)
	}
	return out
}

// parseLoadavg turns /proc/loadavg's "0.07 0.21 0.32 2/439 9" into the
// three standard load-average gauges.
func parseLoadavg(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil
	}
	names := []string{"node_load1", "node_load5", "node_load15"}
	var out []ingest.Sample
	for i, name := range names {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			continue
		}
		out = append(out, sample(name, nil, v, now, interval))
	}
	return out
}

// parseDefaultRouteIfaceLinux reads /proc/net/route (tab-separated, one
// header line) and returns the Iface column of the row whose Destination is
// the all-zero default route — the same mechanism `ip route get` uses
// internally, just read straight from the source instead of shelling out.
// Returns "" if no default route is present (e.g. no network yet).
func parseDefaultRouteIfaceLinux(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum == 1 {
			continue // "Iface  Destination  Gateway ..." header
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		if fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

// mountEntry is one row of /proc/mounts this package considers worth
// reporting filesystem usage for.
type mountEntry struct {
	Device     string
	Mountpoint string
	Fstype     string
}

// mountFstypeSkip excludes virtual/pseudo filesystems with no meaningful
// "how full is this" answer — same spirit as node_exporter's default
// fstype-exclude list, written fresh rather than copied. tmpfs is
// deliberately kept: it's real, capacity-bounded RAM-backed storage.
var mountFstypeSkip = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "pstore": true, "bpf": true,
	"tracefs": true, "debugfs": true, "securityfs": true, "mqueue": true,
	"hugetlbfs": true, "autofs": true, "binfmt_misc": true,
	"configfs": true, "fusectl": true, "overlay": true, "squashfs": true,
	"nsfs": true, "rpc_pipefs": true, "efivarfs": true,
}

// parseMounts turns /proc/mounts into the list of mount points worth
// statfs-ing — filtering out virtual filesystems, not doing the statfs
// syscall itself (that's linux.go's job, since it needs the real
// filesystem, not just parsed text).
func parseMounts(data []byte) []mountEntry {
	var out []mountEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device, mountpoint, fstype := fields[0], fields[1], fields[2]
		if mountFstypeSkip[fstype] {
			continue
		}
		out = append(out, mountEntry{Device: device, Mountpoint: mountpoint, Fstype: fstype})
	}
	return out
}
