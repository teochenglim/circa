package collect

// Fixtures below are captured verbatim from this repo's own dev machine
// (macOS/arm64) via `top -l 1 -n 0`, `vm_stat`, `sysctl -n vm.loadavg`,
// `netstat -ib`, and `route -n get default` — not hand-typed guesses. See
// RELEASE/v0.5.0.md's "Dev/test loop" note for why fixture-based tests
// matter here specifically (this package's own tests run on the same OS
// its darwin.go targets, unlike the Linux side).

import "testing"

var topFixture = []byte(`Processes: 704 total, 3 running, 701 sleeping, 7168 threads
2026/07/13 15:15:06
Load Avg: 4.02, 5.48, 5.95
CPU usage: 5.74% user, 8.76% sys, 85.48% idle
SharedLibs: 511M resident, 113M data, 89M linkedit.
MemRegions: 2228653 total, 7904M resident, 107M private, 3947M shared.
PhysMem: 35G used (3744M wired, 17G compressor), 350M unused.
VM: 495T vsize, 6144M framework vsize, 10448043(0) swapins, 12717164(0) swapouts.
Networks: packets: 15745388/13G in, 10510673/7154M out.
Disks: 64462499/1898G read, 17415127/1394G written.
`)

func TestParseTopCPU(t *testing.T) {
	user, system, idle, ok := parseTopCPU(topFixture)
	if !ok {
		t.Fatal("parseTopCPU: not ok")
	}
	if user != 5.74 || system != 8.76 || idle != 85.48 {
		t.Errorf("got user=%v system=%v idle=%v, want 5.74/8.76/85.48", user, system, idle)
	}
}

func TestParseTopDisks(t *testing.T) {
	reads, readBytes, writes, writtenBytes, ok := parseTopDisks(topFixture)
	if !ok {
		t.Fatal("parseTopDisks: not ok")
	}
	if reads != 64462499 || writes != 17415127 {
		t.Errorf("got reads=%v writes=%v, want 64462499/17415127", reads, writes)
	}
	wantRead := 1898.0 * 1024 * 1024 * 1024
	wantWritten := 1394.0 * 1024 * 1024 * 1024
	if readBytes != wantRead {
		t.Errorf("readBytes = %v, want %v", readBytes, wantRead)
	}
	if writtenBytes != wantWritten {
		t.Errorf("writtenBytes = %v, want %v", writtenBytes, wantWritten)
	}
}

func TestParseHumanBytes(t *testing.T) {
	cases := map[string]float64{
		"1898G": 1898 * 1024 * 1024 * 1024,
		"7154M": 7154 * 1024 * 1024,
		"13G":   13 * 1024 * 1024 * 1024,
		"512":   512,
	}
	for in, want := range cases {
		got, err := parseHumanBytes(in)
		if err != nil {
			t.Errorf("parseHumanBytes(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseHumanBytes(%q) = %v, want %v", in, got, want)
		}
	}
}

var vmStatFixture = []byte(`Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                     7996.
Pages active:                                 458341.
Pages inactive:                               447088.
Pages speculative:                             15851.
Pages throttled:                                   0.
Pages wired down:                             242405.
Pages purgeable:                                4664.
"Translation faults":                     3410145120.
Pages copy-on-write:                       212526657.
Pages zero filled:                        1092152620.
Pages reactivated:                         649555304.
Pages purged:                               96824948.
File-backed pages:                            264323.
Anonymous pages:                              656957.
Pages stored in compressor:                  4230981.
Pages occupied by compressor:                1130899.
Decompressions:                            759060949.
Compressions:                              787795910.
Pageins:                                   299419175.
`)

func TestParseVMStat(t *testing.T) {
	pageSize, counts := parseVMStat(vmStatFixture)
	if pageSize != 16384 {
		t.Errorf("pageSize = %v, want 16384", pageSize)
	}
	want := map[string]uint64{
		"free": 7996, "active": 458341, "inactive": 447088,
		"wired": 242405, "compressed": 4230981,
	}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("counts[%q] = %v, want %v", k, counts[k], v)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("got %d counts, want %d (unmapped vm_stat keys shouldn't leak in): %+v", len(counts), len(want), counts)
	}
}

func TestParseLoadavgSysctl(t *testing.T) {
	l1, l5, l15, ok := parseLoadavgSysctl([]byte("{ 5.55 5.89 6.12 }\n"))
	if !ok {
		t.Fatal("not ok")
	}
	if l1 != 5.55 || l5 != 5.89 || l15 != 6.12 {
		t.Errorf("got %v/%v/%v, want 5.55/5.89/6.12", l1, l5, l15)
	}
}

var routeDefaultFixture = []byte(`   route to: default
destination: default
       mask: default
    gateway: 192.168.50.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500         0
`)

func TestParseDefaultRouteIfaceDarwin(t *testing.T) {
	if iface := parseDefaultRouteIfaceDarwin(routeDefaultFixture); iface != "en0" {
		t.Errorf("got %q, want en0", iface)
	}
}

// netstatIBFixture mixes an interface with a MAC address (en0 — this is
// the "eth0/Wi-Fi" case the project cares about, per-column-shifted from
// gif0 below since Address is present) and one without (gif0 — no MAC,
// Address column entirely absent, shifting every later field left by one).
// Also includes the non-Link inet/inet6 rows netstat prints for the same
// interface, which must be skipped, not double-counted.
var netstatIBFixture = []byte(`Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
lo0        16384 <Link#1>                       3605027     0 1686760961  3605027     0 1686760961     0
lo0        16384 127           localhost        3605027     - 1686760961  3605027     - 1686760961     -
gif0*      1280  <Link#2>                             0     0          0        0     0          0     0
en0        1500  <Link#14>   4a:af:db:f9:a0:de 12141528     0 12732501290  6902897     0 5817118821     0
`)

func TestParseNetstatIB(t *testing.T) {
	got := parseNetstatIB(netstatIBFixture)

	byName := make(map[string]netIfaceCounters)
	for _, c := range got {
		byName[c.Name] = c
	}

	// lo0's non-Link row (with "-" placeholders) must not produce a second
	// entry or corrupt the Link row's parsed values.
	if len(got) != 3 {
		t.Fatalf("got %d interfaces, want 3 (lo0, gif0, en0): %+v", len(got), got)
	}

	lo0 := byName["lo0"]
	if lo0.RxBytes != 1686760961 || lo0.RxPackets != 3605027 {
		t.Errorf("lo0 = %+v", lo0)
	}

	gif0 := byName["gif0"] // "*" suffix (down interface) must be stripped
	if gif0.RxBytes != 0 {
		t.Errorf("gif0 = %+v, want all-zero", gif0)
	}

	en0 := byName["en0"]
	if en0.RxBytes != 12732501290 || en0.RxPackets != 12141528 {
		t.Errorf("en0 rx = %+v", en0)
	}
	if en0.TxBytes != 5817118821 || en0.TxPackets != 6902897 {
		t.Errorf("en0 tx = %+v", en0)
	}
}
