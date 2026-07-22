package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ContainerStats is a point-in-time resource sample for one app container.
type ContainerStats struct {
	CPUPercent    float64
	MemBytes      int64
	MemLimitBytes int64
	NetRxBytes    int64
	NetTxBytes    int64
}

// dockerStatsLine is the shape of `docker stats --no-stream --format '{{json .}}'`.
type dockerStatsLine struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	NetIO    string `json:"NetIO"`
}

var sizeUnits = map[string]float64{
	"B":  1,
	"kB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40,
}

// parseSize parses a docker size string like "12.5MiB", "1.05GB", "0B".
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	i := 0
	for i < len(s) && (s[i] == '.' || s[i] == '-' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", s, err)
	}
	unit := strings.TrimSpace(s[i:])
	if unit == "" {
		unit = "B"
	}
	mult, ok := sizeUnits[unit]
	if !ok {
		return 0, fmt.Errorf("unknown size unit %q in %q", unit, s)
	}
	return int64(num * mult), nil
}

// parsePair splits "A / B" and parses both sides as sizes.
func parsePair(s string) (int64, int64, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected 'A / B', got %q", s)
	}
	a, err := parseSize(parts[0])
	if err != nil {
		return 0, 0, err
	}
	b, err := parseSize(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

func parseDockerStats(line string) (ContainerStats, error) {
	var d dockerStatsLine
	if err := json.Unmarshal([]byte(line), &d); err != nil {
		return ContainerStats{}, err
	}
	cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(d.CPUPerc), "%"), 64)
	if err != nil {
		return ContainerStats{}, fmt.Errorf("cpu %q: %w", d.CPUPerc, err)
	}
	memUsed, memLimit, err := parsePair(d.MemUsage)
	if err != nil {
		return ContainerStats{}, err
	}
	rx, tx, err := parsePair(d.NetIO)
	if err != nil {
		return ContainerStats{}, err
	}
	return ContainerStats{CPUPercent: cpu, MemBytes: memUsed, MemLimitBytes: memLimit, NetRxBytes: rx, NetTxBytes: tx}, nil
}

// AppStats returns a point-in-time resource sample for the app's container.
// running is false (with a zero sample and nil error) when the app has no
// running container. Addressed by appID, mirroring AppLogs.
func (d *Docker) AppStats(ctx context.Context, appID string) (ContainerStats, bool, error) {
	composePath := filepath.Join(d.projectDir(appID), "compose.yaml")
	if _, err := os.Stat(composePath); err != nil {
		return ContainerStats{}, false, nil
	}
	cid, err := output(ctx, "docker", "compose", "-f", composePath, "ps", "-q", "app")
	if err != nil || cid == "" {
		return ContainerStats{}, false, nil
	}
	out, err := output(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}", cid)
	if err != nil {
		return ContainerStats{}, false, fmt.Errorf("docker stats: %w", err)
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return ContainerStats{}, false, nil
	}
	s, err := parseDockerStats(line)
	if err != nil {
		return ContainerStats{}, false, err
	}
	return s, true, nil
}
