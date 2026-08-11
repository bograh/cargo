package metrics

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HostSample is a point-in-time reading of the whole server, as opposed to a
// single app's container.
//
// Note on running in a container: /proc/stat and /proc/meminfo are not
// namespaced by Docker, so the controlplane reads the *host's* CPU and memory
// even though it is itself containerised. That is exactly what this view wants
// — "is the server healthy", not "is this container healthy".
type HostSample struct {
	TS             time.Time `json:"ts"`
	CPUPercent     float64   `json:"cpu_pct"`
	MemUsedBytes   int64     `json:"mem_used_bytes"`
	MemTotalBytes  int64     `json:"mem_total_bytes"`
	DiskFreeBytes  int64     `json:"disk_free_bytes"`
	DiskTotalBytes int64     `json:"disk_total_bytes"`
	Containers     int       `json:"containers"`
	RunningApps    int       `json:"running_apps"`
}

// HostTopic is the hub topic carrying live host samples.
const HostTopic = "metrics:host"

// cpuTimes is the aggregate jiffy counters from /proc/stat's "cpu" line.
// CPU utilisation is only meaningful as a delta between two readings.
type cpuTimes struct {
	total uint64
	idle  uint64
}

// readCPUTimes parses the aggregate "cpu" line of /proc/stat.
func readCPUTimes(procRoot string) (cpuTimes, error) {
	f, err := os.Open(filepath.Join(procRoot, "stat"))
	if err != nil {
		return cpuTimes{}, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var ct cpuTimes
		for i, raw := range fields[1:] {
			v, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("parse /proc/stat field %d: %w", i, err)
			}
			ct.total += v
			// Fields are user, nice, system, idle, iowait, ... — idle and
			// iowait both count as "not doing work".
			if i == 3 || i == 4 {
				ct.idle += v
			}
		}
		return ct, nil
	}
	if err := scanner.Err(); err != nil {
		return cpuTimes{}, err
	}
	return cpuTimes{}, fmt.Errorf("no cpu line in %s/stat", procRoot)
}

// cpuPercent converts two readings into a utilisation percentage. A zero or
// backwards delta (first sample, or counters reset) reports 0 rather than a
// spike.
func cpuPercent(prev, cur cpuTimes) float64 {
	dTotal := float64(cur.total) - float64(prev.total)
	dIdle := float64(cur.idle) - float64(prev.idle)
	if dTotal <= 0 || dIdle < 0 {
		return 0
	}
	pct := (1 - dIdle/dTotal) * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// readMemInfo returns used and total memory in bytes. "Used" is derived from
// MemAvailable, which accounts for reclaimable page cache — MemFree alone
// makes a healthy server look nearly out of memory.
func readMemInfo(procRoot string) (used, total int64, err error) {
	f, err := os.Open(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()

	var memTotal, memAvailable int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotal = v * 1024 // reported in kB
		case "MemAvailable:":
			memAvailable = v * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if memTotal == 0 {
		return 0, 0, fmt.Errorf("no MemTotal in %s/meminfo", procRoot)
	}
	return memTotal - memAvailable, memTotal, nil
}

// diskUsage reports free/total bytes of the filesystem backing path.
func diskUsage(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return int64(st.Bavail * bsize), int64(st.Blocks * bsize), nil
}

// ContainerCounter reports how many containers are running on the host.
// Satisfied by *reconciler.Docker; a seam so tests need no daemon.
type ContainerCounter interface {
	RunningContainers(ctx context.Context) (int, error)
}

// HostCollector samples the server. It is stateful: CPU needs the previous
// reading, so successive calls to Sample must come from the same instance.
type HostCollector struct {
	procRoot string
	dataDir  string
	counter  ContainerCounter
	prev     cpuTimes
	havePrev bool
}

func NewHostCollector(dataDir string, counter ContainerCounter) *HostCollector {
	return &HostCollector{procRoot: "/proc", dataDir: dataDir, counter: counter}
}

// Sample reads the current host state. Individual sources degrade to zero
// rather than failing the whole sample: a missing docker socket or an
// unreadable /proc must not cost us the CPU and memory series too.
func (h *HostCollector) Sample(ctx context.Context) HostSample {
	s := HostSample{TS: time.Now()}

	if cur, err := readCPUTimes(h.procRoot); err == nil {
		if h.havePrev {
			s.CPUPercent = cpuPercent(h.prev, cur)
		}
		h.prev, h.havePrev = cur, true
	}
	if used, total, err := readMemInfo(h.procRoot); err == nil {
		s.MemUsedBytes, s.MemTotalBytes = used, total
	}
	if free, total, err := diskUsage(h.dataDir); err == nil {
		s.DiskFreeBytes, s.DiskTotalBytes = free, total
	}
	if h.counter != nil {
		if n, err := h.counter.RunningContainers(ctx); err == nil {
			s.Containers = n
		}
	}
	return s
}
