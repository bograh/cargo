package metrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeProc writes a /proc-shaped directory so the parsers can be tested
// without depending on the host's real counters.
func fakeProc(t *testing.T, stat, meminfo string) string {
	t.Helper()
	dir := t.TempDir()
	if stat != "" {
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if meminfo != "" {
		if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(meminfo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const sampleStat = `cpu  100 20 30 800 40 0 10 0 0 0
cpu0 50 10 15 400 20 0 5 0 0 0
intr 12345
ctxt 999
`

func TestReadCPUTimes(t *testing.T) {
	// total = 100+20+30+800+40+0+10 = 1000; idle = idle(800)+iowait(40) = 840.
	got, err := readCPUTimes(fakeProc(t, sampleStat, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got.total != 1000 || got.idle != 840 {
		t.Fatalf("got %+v, want total 1000 idle 840", got)
	}
}

// Only the aggregate "cpu" line counts — per-core "cpu0" lines would double
// the totals if matched by prefix.
func TestReadCPUTimesIgnoresPerCoreLines(t *testing.T) {
	got, err := readCPUTimes(fakeProc(t, "cpu0 1 1 1 1 1 1 1\ncpu  10 0 0 90 0 0 0\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got.total != 100 || got.idle != 90 {
		t.Fatalf("got %+v, want total 100 idle 90", got)
	}
}

func TestCPUPercent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		prev, cur  cpuTimes
		want       float64
		wantApprox bool
	}{
		{name: "half busy", prev: cpuTimes{total: 0, idle: 0}, cur: cpuTimes{total: 100, idle: 50}, want: 50},
		{name: "fully idle", prev: cpuTimes{total: 0, idle: 0}, cur: cpuTimes{total: 100, idle: 100}, want: 0},
		{name: "fully busy", prev: cpuTimes{total: 0, idle: 0}, cur: cpuTimes{total: 100, idle: 0}, want: 100},
		// No time elapsed: report 0 rather than dividing by zero.
		{name: "no delta", prev: cpuTimes{total: 100, idle: 50}, cur: cpuTimes{total: 100, idle: 50}, want: 0},
		// Counters going backwards (host reboot) must not produce a spike.
		{name: "counter reset", prev: cpuTimes{total: 500, idle: 400}, cur: cpuTimes{total: 100, idle: 50}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cpuPercent(tc.prev, tc.cur); got != tc.want {
				t.Fatalf("cpuPercent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadMemInfo(t *testing.T) {
	// Used is derived from MemAvailable, not MemFree: page cache is
	// reclaimable, and using MemFree would report a healthy server as full.
	meminfo := `MemTotal:       16384000 kB
MemFree:          512000 kB
MemAvailable:    8192000 kB
Buffers:          100000 kB
`
	used, total, err := readMemInfo(fakeProc(t, "", meminfo))
	if err != nil {
		t.Fatal(err)
	}
	if total != 16384000*1024 {
		t.Fatalf("total = %d", total)
	}
	if want := int64((16384000 - 8192000) * 1024); used != want {
		t.Fatalf("used = %d, want %d (derived from MemAvailable)", used, want)
	}
}

func TestReadMemInfoMissingTotal(t *testing.T) {
	if _, _, err := readMemInfo(fakeProc(t, "", "MemFree: 100 kB\n")); err == nil {
		t.Fatal("expected an error when MemTotal is absent")
	}
}

type stubCounter struct {
	n   int
	err error
}

func (s stubCounter) RunningContainers(context.Context) (int, error) { return s.n, s.err }

// The first sample has no previous reading to diff against, so CPU is 0; the
// second reports real utilisation.
func TestHostCollectorFirstSampleHasNoCPU(t *testing.T) {
	proc := fakeProc(t, "cpu  10 0 0 90 0 0 0\n", "MemTotal: 1000 kB\nMemAvailable: 400 kB\n")
	h := &HostCollector{procRoot: proc, dataDir: t.TempDir(), counter: stubCounter{n: 7}}

	first := h.Sample(context.Background())
	if first.CPUPercent != 0 {
		t.Fatalf("first sample CPU = %v, want 0 (no baseline yet)", first.CPUPercent)
	}
	if first.MemTotalBytes != 1000*1024 || first.MemUsedBytes != 600*1024 {
		t.Fatalf("memory = %d/%d", first.MemUsedBytes, first.MemTotalBytes)
	}
	if first.Containers != 7 {
		t.Fatalf("containers = %d, want 7", first.Containers)
	}
	if first.DiskTotalBytes <= 0 {
		t.Fatal("disk total not read")
	}

	// Advance the counters: +100 total, +50 idle → 50% busy.
	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte("cpu  60 0 0 140 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := h.Sample(context.Background())
	if second.CPUPercent != 50 {
		t.Fatalf("second sample CPU = %v, want 50", second.CPUPercent)
	}
}

// A missing docker socket or unreadable /proc must cost only that one field.
// Losing the whole sample would blank the charts exactly when something is
// wrong with the host.
func TestHostCollectorDegradesPerSource(t *testing.T) {
	h := &HostCollector{
		procRoot: filepath.Join(t.TempDir(), "nonexistent"),
		dataDir:  t.TempDir(),
		counter:  stubCounter{err: errors.New("docker socket unavailable")},
	}
	s := h.Sample(context.Background())
	if s.CPUPercent != 0 || s.MemTotalBytes != 0 || s.Containers != 0 {
		t.Fatalf("expected zeroed unavailable sources, got %+v", s)
	}
	// Disk still works, so the sample is not empty.
	if s.DiskTotalBytes <= 0 {
		t.Fatalf("disk should still be sampled, got %+v", s)
	}
	if s.TS.IsZero() {
		t.Fatal("sample has no timestamp")
	}
}
