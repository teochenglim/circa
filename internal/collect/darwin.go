//go:build darwin

package collect

// The build-tagged half of macOS collection. Two real syscalls (Getfsstat,
// Uname, both via golang.org/x/sys/unix — already a transitive dependency,
// see linux.go's own use of it) plus five well-established, stable-output
// CLI tools shelled out to for everything Mach's host_statistics would
// otherwise provide, since this repo builds with CGO_ENABLED=0 (Makefile)
// and Mach calls aren't reachable without cgo. See darwin_parse.go's doc
// comment and RELEASE/v0.5.0.md for what that trades away.

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"

	"github.com/teochenglim/circa/internal/ingest"
)

// Supported reports that macOS is fully implemented as of v0.5.0.
func Supported() bool { return true }

// execTimeout bounds every shelled-out command — none of top/vm_stat/
// sysctl/netstat/route should ever take more than a second or two, and a
// hung subprocess shouldn't be able to stall collection indefinitely.
const execTimeout = 3 * time.Second

func runCommand(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func collectAll(now time.Time, interval time.Duration) ([]ingest.Sample, error) {
	var out []ingest.Sample

	if data, err := runCommand("top", "-l", "1", "-n", "0"); err == nil {
		out = append(out, cpuSamplesDarwin(data, now, interval)...)
		out = append(out, diskSamplesDarwin(data, now, interval)...)
	}
	if data, err := runCommand("vm_stat"); err == nil {
		out = append(out, memorySamplesDarwin(data, now, interval)...)
	}
	if data, err := runCommand("sysctl", "-n", "vm.loadavg"); err == nil {
		out = append(out, loadavgSamplesDarwin(data, now, interval)...)
	}
	if data, err := runCommand("netstat", "-ib"); err == nil {
		out = append(out, networkSamplesDarwin(data, now, interval)...)
	}
	if data, err := runCommand("route", "-n", "get", "default"); err == nil {
		if iface := parseDefaultRouteIfaceDarwin(data); iface != "" {
			out = append(out, sample("node_network_primary_info", map[string]string{"device": iface}, 1, now, interval))
		}
	}
	out = append(out, filesystemSamplesDarwin(now, interval)...)
	out = append(out, unameSampleDarwin(now, interval))

	if len(out) == 0 {
		return nil, fmt.Errorf("no macOS system data could be collected")
	}
	return out, nil
}

func cpuSamplesDarwin(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	user, system, idle, ok := parseTopCPU(data)
	if !ok {
		return nil
	}
	return []ingest.Sample{
		sample("node_cpu_usage_percent", map[string]string{"mode": "user"}, user, now, interval),
		sample("node_cpu_usage_percent", map[string]string{"mode": "system"}, system, now, interval),
		sample("node_cpu_usage_percent", map[string]string{"mode": "idle"}, idle, now, interval),
	}
}

func diskSamplesDarwin(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	reads, readBytes, writes, writtenBytes, ok := parseTopDisks(data)
	if !ok {
		return nil
	}
	labels := map[string]string{"device": "total"} // system-wide aggregate — see darwin_parse.go
	return []ingest.Sample{
		sample("node_disk_reads_completed_total", labels, reads, now, interval),
		sample("node_disk_read_bytes_total", labels, readBytes, now, interval),
		sample("node_disk_writes_completed_total", labels, writes, now, interval),
		sample("node_disk_written_bytes_total", labels, writtenBytes, now, interval),
	}
}

func memorySamplesDarwin(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	pageSize, counts := parseVMStat(data)
	if pageSize == 0 {
		return nil
	}
	var out []ingest.Sample
	for suffix, pages := range counts {
		out = append(out, sample("node_memory_"+suffix+"_bytes", nil, float64(pages*pageSize), now, interval))
	}
	if total, err := unix.SysctlUint64("hw.memsize"); err == nil {
		out = append(out, sample("node_memory_total_bytes", nil, float64(total), now, interval))
	}
	return out
}

func loadavgSamplesDarwin(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	l1, l5, l15, ok := parseLoadavgSysctl(data)
	if !ok {
		return nil
	}
	return []ingest.Sample{
		sample("node_load1", nil, l1, now, interval),
		sample("node_load5", nil, l5, now, interval),
		sample("node_load15", nil, l15, now, interval),
	}
}

func networkSamplesDarwin(data []byte, now time.Time, interval time.Duration) []ingest.Sample {
	var out []ingest.Sample
	for _, c := range parseNetstatIB(data) {
		labels := map[string]string{"device": c.Name}
		out = append(out,
			sample("node_network_receive_bytes_total", labels, float64(c.RxBytes), now, interval),
			sample("node_network_receive_packets_total", labels, float64(c.RxPackets), now, interval),
			sample("node_network_transmit_bytes_total", labels, float64(c.TxBytes), now, interval),
			sample("node_network_transmit_packets_total", labels, float64(c.TxPackets), now, interval),
		)
	}
	return out
}

// fstypeSkipDarwin excludes macOS's own virtual filesystems — devfs (the
// /dev device-node filesystem) and autofs automount maps have no
// meaningful "how full is this" answer, the same reasoning as
// mountFstypeSkip's Linux denylist.
var fstypeSkipDarwin = map[string]bool{"devfs": true, "autofs": true}

// filesystemSamplesDarwin uses Getfsstat — a single syscall that enumerates
// every mounted volume with its statfs_t already filled in — instead of
// parsing `df`/`mount` output and stat-ing each path separately, the way
// linux.go has to (Linux's statfs(2) takes a path, not "give me every
// mount"; Getfsstat is macOS/BSD-specific and gives all of them at once).
func filesystemSamplesDarwin(now time.Time, interval time.Duration) []ingest.Sample {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return nil
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil
	}
	buf = buf[:n]

	var out []ingest.Sample
	for _, stat := range buf {
		fstype := charsToString(stat.Fstypename[:])
		if fstypeSkipDarwin[fstype] {
			continue
		}
		labels := map[string]string{
			"device":     charsToString(stat.Mntfromname[:]),
			"mountpoint": charsToString(stat.Mntonname[:]),
			"fstype":     fstype,
		}
		bsize := uint64(stat.Bsize)
		out = append(out,
			sample("node_filesystem_size_bytes", labels, float64(stat.Blocks*bsize), now, interval),
			sample("node_filesystem_free_bytes", labels, float64(stat.Bfree*bsize), now, interval),
			sample("node_filesystem_avail_bytes", labels, float64(stat.Bavail*bsize), now, interval),
		)
	}
	return out
}

func unameSampleDarwin(now time.Time, interval time.Duration) ingest.Sample {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return sample("node_uname_info", nil, 1, now, interval)
	}
	labels := map[string]string{
		"sysname":  charsToString(uts.Sysname[:]),
		"release":  charsToString(uts.Release[:]),
		"version":  charsToString(uts.Version[:]),
		"machine":  charsToString(uts.Machine[:]),
		"nodename": charsToString(uts.Nodename[:]),
	}
	return sample("node_uname_info", labels, 1, now, interval)
}
