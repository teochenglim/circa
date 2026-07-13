package collect

// Pure parsing functions for every macOS command-output/text format this
// package reads — no build tag, so they're testable without actually
// exec'ing anything. darwin.go is the thin build-tagged layer that runs
// these commands (top, vm_stat, sysctl, netstat, route) and calls into
// here; the syscall-based bits (statfs via Getfsstat, uname) don't need
// text parsing at all and live entirely in darwin.go.
//
// macOS has no /proc or Mach host_statistics access without cgo, and this
// repo's build is CGO_ENABLED=0 (see Makefile) — so unlike Linux, several
// of these are best-effort readings off stable, long-standing CLI tool
// output rather than raw kernel counters. Each one says so at the point it
// diverges from Linux's semantics; see RELEASE/v0.5.0.md.

import (
	"regexp"
	"strconv"
	"strings"
)

var topCPURe = regexp.MustCompile(`CPU usage:\s+([0-9.]+)%\s+user,\s+([0-9.]+)%\s+sys,\s+([0-9.]+)%\s+idle`)

// parseTopCPU reads `top -l 1 -n 0`'s "CPU usage: X% user, Y% sys, Z% idle"
// line. Unlike Linux's node_cpu_seconds_total (a monotonic counter in
// seconds since boot), this is an instantaneous percentage — macOS has no
// no-cgo path to Mach's host_statistics64, which is what a true cumulative
// counter would need. Exposed as node_cpu_usage_percent, a deliberately
// different metric name so it's never mistaken for the Linux counter.
func parseTopCPU(data []byte) (user, system, idle float64, ok bool) {
	m := topCPURe.FindSubmatch(data)
	if m == nil {
		return 0, 0, 0, false
	}
	user, err1 := strconv.ParseFloat(string(m[1]), 64)
	system, err2 := strconv.ParseFloat(string(m[2]), 64)
	idle, err3 := strconv.ParseFloat(string(m[3]), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return user, system, idle, true
}

var topDisksRe = regexp.MustCompile(`Disks:\s+(\d+)/([0-9.]+[KMGTP]?)\s+read,\s+(\d+)/([0-9.]+[KMGTP]?)\s+written`)

// parseTopDisks reads `top`'s "Disks: <n>/<bytes> read, <n>/<bytes>
// written." line — a system-wide aggregate since boot, not per-device
// (macOS per-device I/O counters need IOKit, which needs cgo). Treated as
// a counter (node_disk_*_total{device="total"}) since it only grows across
// a boot, unlike the CPU percentage above.
func parseTopDisks(data []byte) (reads, readBytes, writes, writtenBytes float64, ok bool) {
	m := topDisksRe.FindSubmatch(data)
	if m == nil {
		return 0, 0, 0, 0, false
	}
	reads, err1 := strconv.ParseFloat(string(m[1]), 64)
	rb, err2 := parseHumanBytes(string(m[2]))
	writes, err3 := strconv.ParseFloat(string(m[3]), 64)
	wb, err4 := parseHumanBytes(string(m[4]))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return 0, 0, 0, 0, false
	}
	return reads, rb, writes, wb, true
}

var humanBytesRe = regexp.MustCompile(`^([0-9.]+)([KMGTP]?)$`)

// parseHumanBytes parses top's binary-unit-suffixed numbers ("1898G",
// "7154M") into a raw byte count. 1024-based, matching top/Activity
// Monitor's own convention.
func parseHumanBytes(s string) (float64, error) {
	m := humanBytesRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, strconv.ErrSyntax
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	const ki = 1024.0
	switch m[2] {
	case "K":
		return n * ki, nil
	case "M":
		return n * ki * ki, nil
	case "G":
		return n * ki * ki * ki, nil
	case "T":
		return n * ki * ki * ki * ki, nil
	case "P":
		return n * ki * ki * ki * ki * ki, nil
	default:
		return n, nil
	}
}

var vmStatPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)

// vmStatFields maps the vm_stat key this package surfaces to the
// node_memory_<key>_bytes metric name suffix — a curated subset chosen to
// roughly parallel Linux's MemFree/Active/Inactive shape, using macOS's own
// vocabulary (macOS has no single "free" number in the Linux sense; "wired"
// and "compressed" are macOS-specific concepts with no Linux equivalent).
var vmStatFields = map[string]string{
	"Pages free":                 "free",
	"Pages active":               "active",
	"Pages inactive":             "inactive",
	"Pages wired down":           "wired",
	"Pages stored in compressor": "compressed",
}

// parseVMStat reads `vm_stat`'s page-count output, returning the page size
// (bytes, from the header line) and the raw page counts for the keys in
// vmStatFields. darwin.go multiplies counts by page size and by hw.memsize
// (fetched separately via sysctl, not part of vm_stat's own output) to
// build node_memory_*_bytes samples.
func parseVMStat(data []byte) (pageSize uint64, counts map[string]uint64) {
	counts = make(map[string]uint64)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 {
			if m := vmStatPageSizeRe.FindStringSubmatch(line); m != nil {
				pageSize, _ = strconv.ParseUint(m[1], 10, 64)
			}
			continue
		}
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"`)
		suffix, present := vmStatFields[key]
		if !present {
			continue
		}
		valStr := strings.TrimSuffix(strings.TrimSpace(rest), ".")
		v, err := strconv.ParseUint(valStr, 10, 64)
		if err != nil {
			continue
		}
		counts[suffix] = v
	}
	return pageSize, counts
}

// parseLoadavgSysctl reads `sysctl -n vm.loadavg`'s "{ 5.55 5.89 6.12 }"
// output.
func parseLoadavgSysctl(data []byte) (load1, load5, load15 float64, ok bool) {
	s := strings.TrimSpace(string(data))
	s = strings.Trim(s, "{}")
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return 0, 0, 0, false
	}
	var err1, err2, err3 error
	load1, err1 = strconv.ParseFloat(fields[0], 64)
	load5, err2 = strconv.ParseFloat(fields[1], 64)
	load15, err3 = strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return load1, load5, load15, true
}

// parseDefaultRouteIfaceDarwin reads `route -n get default`'s "interface:
// en0" line — the macOS equivalent of Linux's /proc/net/route default-route
// row (see parseDefaultRouteIfaceLinux), used to tag whichever interface
// (Wi-Fi's en0, a USB Ethernet adapter's enN, whatever's actually routing
// traffic — not guessed by name) as node_network_primary_info.
func parseDefaultRouteIfaceDarwin(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if name, iface, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "interface" {
			return strings.TrimSpace(iface)
		}
	}
	return ""
}

// netIfaceCounters is one interface's byte/packet counters, parsed from
// `netstat -ib`.
type netIfaceCounters struct {
	Name      string
	RxBytes   uint64
	RxPackets uint64
	TxBytes   uint64
	TxPackets uint64
}

// parseNetstatIB parses `netstat -ib`'s table, keeping only each
// interface's link-layer row (the one whose Network column is "<LinkN>") —
// the same interface otherwise gets one extra row per address family
// (inet/inet6) with "-" placeholders instead of real counters, which this
// intentionally skips.
//
// The Address column (a MAC, e.g. "76:02:53:68:b1:3d") is present on some
// rows and entirely absent on others (loopback, tunnel interfaces) with no
// placeholder — which shifts every later column left by one. Rather than
// track that per-row, the trailing 7 fields (Ipkts Ierrs Ibytes Opkts Oerrs
// Obytes Coll) are always present and always numeric on a link-layer row,
// so they're read from the end of the line instead of a fixed offset from
// the start.
func parseNetstatIB(data []byte) []netIfaceCounters {
	var out []netIfaceCounters
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		if fields[0] == "Name" {
			continue // header row
		}
		isLink := false
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "<Link") {
				isLink = true
				break
			}
		}
		if !isLink {
			continue
		}

		n := len(fields)
		rxPackets, err1 := strconv.ParseUint(fields[n-7], 10, 64)
		rxBytes, err2 := strconv.ParseUint(fields[n-5], 10, 64)
		txPackets, err3 := strconv.ParseUint(fields[n-4], 10, 64)
		txBytes, err4 := strconv.ParseUint(fields[n-2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		name := strings.TrimSuffix(fields[0], "*") // "gif0*" etc. marks a down interface
		out = append(out, netIfaceCounters{
			Name: name, RxBytes: rxBytes, RxPackets: rxPackets, TxBytes: txBytes, TxPackets: txPackets,
		})
	}
	return out
}
