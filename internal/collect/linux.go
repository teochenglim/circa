//go:build linux

package collect

// The build-tagged half of Linux collection: reads the /proc files and
// calls unix.Statfs/unix.Uname, then hands raw bytes/results to the pure
// parsers in linux_parse.go. Kept deliberately thin — anything that can be
// tested without a real /proc lives in that file instead.

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/teochenglim/circa/internal/ingest"
)

// Supported reports that Linux is fully implemented as of v0.5.0.
func Supported() bool { return true }

// procRoot returns the /proc mount to read stat/meminfo/diskstats/net/
// loadavg/net/route from — "/proc" normally, or HOST_PROC (e.g.
// "/host/proc") when circa runs in a container with the host's real /proc
// bind-mounted in read-only, so a DaemonSet pod reports the *node's* stats
// rather than its own container's (see k8s/20-daemonset.yaml and
// helm/circa's daemonset.yaml, which set this). filesystemSamplesLinux
// deliberately does not use this — see its own doc comment for why.
func procRoot() string {
	if v := os.Getenv("HOST_PROC"); v != "" {
		return v
	}
	return "/proc"
}

func collectAll(now time.Time, interval time.Duration) ([]ingest.Sample, error) {
	var out []ingest.Sample
	root := procRoot()

	if data, err := os.ReadFile(root + "/stat"); err == nil {
		out = append(out, parseProcStat(data, now, interval)...)
	}
	if data, err := os.ReadFile(root + "/meminfo"); err == nil {
		out = append(out, parseMemInfo(data, now, interval)...)
	}
	if data, err := os.ReadFile(root + "/diskstats"); err == nil {
		out = append(out, parseDiskstats(data, now, interval)...)
	}
	if data, err := os.ReadFile(root + "/net/dev"); err == nil {
		out = append(out, parseNetDev(data, now, interval)...)
	}
	if data, err := os.ReadFile(root + "/loadavg"); err == nil {
		out = append(out, parseLoadavg(data, now, interval)...)
	}
	if data, err := os.ReadFile(root + "/net/route"); err == nil {
		if iface := parseDefaultRouteIfaceLinux(data); iface != "" {
			out = append(out, sample("node_network_primary_info", map[string]string{"device": iface}, 1, now, interval))
		}
	}
	out = append(out, filesystemSamplesLinux(now, interval)...)
	out = append(out, unameSampleLinux(now, interval))

	if len(out) == 0 {
		return nil, fmt.Errorf("no /proc data could be read")
	}
	return out, nil
}

// filesystemSamplesLinux always reads the container's own /proc/mounts and
// statfs()'s within its own mount namespace — deliberately ignoring
// HOST_PROC, unlike every other reader in this file. /proc/mounts's
// *paths* are only statfs-able from a mount namespace that actually has
// them mounted: a bind-mounted host /proc/mounts would list host-side
// mountpoints (e.g. /var/lib/kubelet/pods/...) this container doesn't have,
// and naively statfs-ing them would either fail (harmless, already
// tolerated below) or silently resolve to an unrelated same-named path
// inside the container's own rootfs — mislabeling it with a host device/
// fstype string, which is worse than just not reporting it. Full
// host-filesystem visibility from inside a container needs a full
// host-root bind mount with mount propagation (node_exporter's own
// --path.rootfs trick) — a bigger deployment lift than this milestone
// scopes; in practice this still correctly reports every filesystem
// actually mounted into the container (its own rootfs, the config/data
// volumes), just not the full host mount table.
func filesystemSamplesLinux(now time.Time, interval time.Duration) []ingest.Sample {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}

	var out []ingest.Sample
	for _, m := range parseMounts(data) {
		var stat unix.Statfs_t
		if err := unix.Statfs(m.Mountpoint, &stat); err != nil {
			continue
		}
		labels := map[string]string{"device": m.Device, "mountpoint": m.Mountpoint, "fstype": m.Fstype}
		bsize := uint64(stat.Bsize)
		out = append(out,
			sample("node_filesystem_size_bytes", labels, float64(stat.Blocks*bsize), now, interval),
			sample("node_filesystem_free_bytes", labels, float64(stat.Bfree*bsize), now, interval),
			sample("node_filesystem_avail_bytes", labels, float64(stat.Bavail*bsize), now, interval),
		)
	}
	return out
}

func unameSampleLinux(now time.Time, interval time.Duration) ingest.Sample {
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
