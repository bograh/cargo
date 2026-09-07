package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
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
// Container is the short id, which is how a batched sample is matched back to
// the container it came from.
type dockerStatsLine struct {
	Container string `json:"Container"`
	CPUPerc   string `json:"CPUPerc"`
	MemUsage  string `json:"MemUsage"`
	NetIO     string `json:"NetIO"`
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
	s, _, err := parseStatsLine(line)
	return s, err
}

// parseStatsLine also returns the short container id, which a batched call
// needs to attribute each line to an app.
func parseStatsLine(line string) (ContainerStats, string, error) {
	var d dockerStatsLine
	if err := json.Unmarshal([]byte(line), &d); err != nil {
		return ContainerStats{}, "", err
	}
	cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(d.CPUPerc), "%"), 64)
	if err != nil {
		return ContainerStats{}, "", fmt.Errorf("cpu %q: %w", d.CPUPerc, err)
	}
	memUsed, memLimit, err := parsePair(d.MemUsage)
	if err != nil {
		return ContainerStats{}, "", err
	}
	rx, tx, err := parsePair(d.NetIO)
	if err != nil {
		return ContainerStats{}, "", err
	}
	return ContainerStats{
		CPUPercent: cpu, MemBytes: memUsed, MemLimitBytes: memLimit,
		NetRxBytes: rx, NetTxBytes: tx,
	}, strings.TrimSpace(d.Container), nil
}

// ImageLabels reports the labels baked into an image. Docker merges these into
// every container started from the image, and Traefik's provider reads the
// merged set — so an image is as much a source of routing labels as a compose
// file is. A reference that is not present locally is pulled once before the
// second attempt, so both registry images and locally-built tags resolve.
//
// Like every other docker invocation in this package it goes through the
// receiver, so a provider bound to a worker host inspects the image on that
// host — which is the one that will run it.
func (d *Docker) ImageLabels(ctx context.Context, ref string) (map[string]string, error) {
	inspect := func() (string, error) {
		return d.output(ctx, "docker", "image", "inspect", "--format", "{{json .Config.Labels}}", ref)
	}
	out, err := inspect()
	if err != nil {
		if perr := d.run(ctx, io.Discard, "docker", "pull", ref); perr != nil {
			return nil, fmt.Errorf("inspect image %s: %w", ref, err)
		}
		if out, err = inspect(); err != nil {
			return nil, fmt.Errorf("inspect image %s: %w", ref, err)
		}
	}
	// An image with no labels inspects as the JSON literal null, not as {}.
	if strings.TrimSpace(out) == "null" {
		return map[string]string{}, nil
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(out), &labels); err != nil {
		return nil, fmt.Errorf("read labels of image %s: %w", ref, err)
	}
	return labels, nil
}

// statsResolveConcurrency bounds the `docker compose ps` calls issued while
// resolving containers. Each spawns a compose CLI process, so running them all
// at once on a host with many apps trades one bottleneck for another.
const statsResolveConcurrency = 8

// AppStatsBatch samples every app in one pass, keyed by app id. Apps with no
// running container are simply absent from the result.
//
// "Every app" means every app on this receiver's daemon. The collector holds
// the control plane's own provider, so apps placed on a worker host are not
// sampled — the same scope the per-app call had, not a narrowing.
//
// The shape matters more than it looks. `docker stats --no-stream` blocks for a
// sampling interval — it needs two CPU readings — and that cost is per *call*,
// not per container. Asking about one container at a time therefore multiplies
// the whole tick by the number of apps, and at around ten apps the tick stops
// fitting inside its own 15-second period. One call for every container costs
// what one app used to.
//
// Resolving which container belongs to which app still goes through `docker
// compose ps`, because that uses the exact `-f` files the app was deployed
// with and is authoritative about colors and compose-source projects. Those run
// concurrently, and a stale id — a container that stopped between resolution
// and sampling, which every deploy produces — is dropped rather than failing
// the tick, since `docker stats` errors on an id it cannot find.
func (d *Docker) AppStatsBatch(ctx context.Context, appIDs []string) (map[string]ContainerStats, error) {
	running, err := d.runningContainerIDs(ctx)
	if err != nil {
		return nil, err
	}
	byApp := d.resolveAppContainers(ctx, appIDs, running)
	if len(byApp) == 0 {
		return map[string]ContainerStats{}, nil
	}

	args := make([]string, 0, len(byApp)+4)
	args = append(args, "stats", "--no-stream", "--format", "{{json .}}")
	for _, cid := range byApp {
		args = append(args, cid)
	}
	out, err := d.output(ctx, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w", err)
	}

	// docker reports the short id; the resolved ids are full length.
	samples := map[string]ContainerStats{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s, short, err := parseStatsLine(line)
		if err != nil || short == "" {
			continue
		}
		samples[short] = s
	}

	return attributeStats(byApp, samples), nil
}

// attributeStats matches each app's container to the sample docker reported for
// it. docker stats identifies a container by its short id while compose returns
// the full one, so this is a prefix match rather than a lookup.
func attributeStats(byApp map[string]string, samples map[string]ContainerStats) map[string]ContainerStats {
	result := make(map[string]ContainerStats, len(byApp))
	for appID, cid := range byApp {
		for short, s := range samples {
			if short != "" && strings.HasPrefix(cid, short) {
				result[appID] = s
				break
			}
		}
	}
	return result
}

// resolveAppContainers maps app id to running container id, skipping apps that
// have no project on disk, no container, or a container that is no longer
// running.
func (d *Docker) resolveAppContainers(ctx context.Context, appIDs []string, running map[string]bool) map[string]string {
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		byApp = map[string]string{}
		sem   = make(chan struct{}, statsResolveConcurrency)
	)
	for _, appID := range appIDs {
		wg.Add(1)
		go func(appID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			args, service, err := d.activeProject(ctx, appID)
			if err != nil {
				return // never deployed
			}
			cid, err := d.outputCompose(ctx, args, "ps", "-q", service)
			cid = strings.TrimSpace(cid)
			if err != nil || cid == "" || !running[cid] {
				return
			}
			mu.Lock()
			byApp[appID] = cid
			mu.Unlock()
		}(appID)
	}
	wg.Wait()
	return byApp
}

// runningContainerIDs returns the full ids of every running container, used to
// drop resolutions that went stale before the sample.
func (d *Docker) runningContainerIDs(ctx context.Context) (map[string]bool, error) {
	out, err := d.output(ctx, "docker", "ps", "-q", "--no-trunc")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	ids := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids[id] = true
		}
	}
	return ids, nil
}
