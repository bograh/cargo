# App Metrics (live + 48h history) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each deployed app a Metrics tab showing live and last-48h CPU, memory, network, request rate, latency (p50/p95), and error rate.

**Architecture:** A River periodic job (15s) samples every running app: resource metrics from `docker stats --no-stream` (via the reconciler `Docker` provider) and traffic metrics scraped/parsed from Traefik's Prometheus endpoint. Each sample is written to a new `app_metrics` Postgres table (pruned to 48h) and published to the in-memory `events.Hub` on topic `metrics:<appID>`. The frontend Metrics page loads history via a JSON endpoint and appends live samples via SSE (`EventSource`), rendering small inline SVG charts.

**Tech Stack:** Go, chi, River (job queue), pgx/sqlc, goose migrations, Docker CLI, Traefik v3 Prometheus metrics, React 19 + TanStack Query + Tailwind v4, native `EventSource`.

## Global Constraints

- Platform stays at **exactly 3 containers** (NFR-3): Traefik, controlplane, Postgres. Do NOT add Prometheus/Grafana/cAdvisor. Traefik metrics are scraped by the controlplane over the internal `cargo-proxy` network; the metrics port is NOT published to the host.
- Docker access is via the **Docker CLI** (`os/exec`), never the moby SDK — mirror `internal/reconciler/provider.go` helpers `run`/`output`.
- Sampling interval: **15s**. History retention: **48h**.
- Backend URL param is `{appID}`; frontend route param is `:appId`.
- sqlc queries live in `internal/db/queries/*.sql`; regenerate with `sqlc generate` from repo root. Migrations are goose files in `internal/db/migrations/000NN_*.sql`, embedded and auto-run at startup.
- Role gating: reading metrics requires `viewer+` (use `s.apps.Get`, which gates viewer), same as app logs.
- Run backend tests with the project's normal `go test ./...` (integration tests use testcontainers Postgres + real Docker; they are not `-short`).

---

### Task 1: `app_metrics` table + sqlc queries

**Files:**
- Create: `internal/db/migrations/00012_app_metrics.sql`
- Modify: `internal/db/queries/apps.sql` (append queries)
- Regenerate: `internal/db/sqlc/*` via `sqlc generate`

**Interfaces:**
- Produces (generated sqlc):
  - `sqlc.AppMetric` struct with fields `AppID pgtype.UUID`, `CreatedAt pgtype.Timestamptz`, `CpuPct float64`, `MemBytes int64`, `MemLimitBytes int64`, `NetRxBytes int64`, `NetTxBytes int64`, `ReqRate float64`, `ErrRate float64`, `P50Ms float64`, `P95Ms float64`
  - `Queries.CreateAppMetric(ctx, CreateAppMetricParams) error`
  - `Queries.ListAppMetricsSince(ctx, ListAppMetricsSinceParams) ([]AppMetric, error)` — params `{AppID pgtype.UUID; CreatedAt pgtype.Timestamptz}`, ordered oldest→newest
  - `Queries.LatestAppMetric(ctx, appID pgtype.UUID) (AppMetric, error)`
  - `Queries.PurgeAppMetrics(ctx) (int64, error)`

- [ ] **Step 1: Write the migration**

Create `internal/db/migrations/00012_app_metrics.sql`:

```sql
-- +goose Up
-- One row per app per sample tick (~15s). Traffic columns are 0 when the app
-- has received no requests in the interval (or Traefik metrics are off).
CREATE TABLE app_metrics (
    app_id          UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    cpu_pct         DOUBLE PRECISION NOT NULL DEFAULT 0,
    mem_bytes       BIGINT NOT NULL DEFAULT 0,
    mem_limit_bytes BIGINT NOT NULL DEFAULT 0,
    net_rx_bytes    BIGINT NOT NULL DEFAULT 0,
    net_tx_bytes    BIGINT NOT NULL DEFAULT 0,
    req_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
    err_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
    p50_ms          DOUBLE PRECISION NOT NULL DEFAULT 0,
    p95_ms          DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX app_metrics_app_time_idx ON app_metrics (app_id, created_at DESC);

-- +goose Down
DROP TABLE app_metrics;
```

- [ ] **Step 2: Append sqlc queries**

Append to `internal/db/queries/apps.sql`:

```sql
-- name: CreateAppMetric :exec
INSERT INTO app_metrics (
    app_id, cpu_pct, mem_bytes, mem_limit_bytes,
    net_rx_bytes, net_tx_bytes, req_rate, err_rate, p50_ms, p95_ms
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10);

-- name: ListAppMetricsSince :many
SELECT * FROM app_metrics
WHERE app_id = $1 AND created_at >= $2
ORDER BY created_at;

-- name: LatestAppMetric :one
SELECT * FROM app_metrics
WHERE app_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: PurgeAppMetrics :execrows
DELETE FROM app_metrics WHERE created_at < now() - interval '48 hours';
```

- [ ] **Step 3: Regenerate sqlc and build**

Run: `sqlc generate && go build ./...`
Expected: no output from sqlc, clean build. Verify: `grep -n "CreateAppMetric\|AppMetric struct\|PurgeAppMetrics" internal/db/sqlc/*.go` shows the generated method + struct.

- [ ] **Step 4: Verify migration applies (via an existing integration test)**

Run: `go test ./internal/apps/ -run TestSetDesiredState -v 2>&1 | grep -E "00012|migrated to version"`
Expected: shows `OK   00012_app_metrics.sql` and `migrated to version: 12` (the test's testcontainers Postgres runs all migrations).

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/00012_app_metrics.sql internal/db/queries/apps.sql internal/db/sqlc/
git commit -m "feat(metrics): app_metrics table and queries"
```

---

### Task 2: Resource stats from `docker stats`

**Files:**
- Create: `internal/reconciler/stats.go`
- Test: `internal/reconciler/stats_test.go`
- Modify: `internal/reconciler/provider.go` (add `AppStats` method on `*Docker`)

**Interfaces:**
- Produces:
  - `type ContainerStats struct { CPUPercent float64; MemBytes int64; MemLimitBytes int64; NetRxBytes int64; NetTxBytes int64 }`
  - `func (d *Docker) AppStats(ctx context.Context, appID string) (ContainerStats, bool, error)` — the bool is `running` (false + zero value when the app has no running container; nil error). Mirrors `AppLogs(appID)` addressing by appID.
  - `func parseDockerStats(jsonLine string) (ContainerStats, error)` (unexported, tested directly)
  - `func parseSize(s string) (int64, error)` (unexported, tested directly) — parses docker's size strings ("12.5MiB", "1.1GB", "0B", "1.05kB")

- [ ] **Step 1: Write failing tests**

Create `internal/reconciler/stats_test.go`:

```go
package reconciler

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"0B":       0,
		"1B":       1,
		"1kB":      1000,
		"1.5kB":    1500,
		"12MiB":    12 * 1024 * 1024,
		"1GiB":     1024 * 1024 * 1024,
		"1.05GB":   1_050_000_000,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil {
			t.Fatalf("parseSize(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDockerStats(t *testing.T) {
	line := `{"BlockIO":"0B / 0B","CPUPerc":"12.50%","Container":"abc","ID":"abc","MemPerc":"1.23%","MemUsage":"64MiB / 512MiB","Name":"cargo-app-web-app-1","NetIO":"1.5kB / 2kB","PIDs":"7"}`
	s, err := parseDockerStats(line)
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 12.5 {
		t.Errorf("cpu = %v, want 12.5", s.CPUPercent)
	}
	if s.MemBytes != 64*1024*1024 || s.MemLimitBytes != 512*1024*1024 {
		t.Errorf("mem = %d/%d", s.MemBytes, s.MemLimitBytes)
	}
	if s.NetRxBytes != 1500 || s.NetTxBytes != 2000 {
		t.Errorf("net = %d/%d, want 1500/2000", s.NetRxBytes, s.NetTxBytes)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/reconciler/ -run 'TestParseSize|TestParseDockerStats' 2>&1 | tail -5`
Expected: FAIL — `undefined: parseSize` / `parseDockerStats`.

- [ ] **Step 3: Implement the parsers**

Create `internal/reconciler/stats.go`:

```go
package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
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
	"B": 1,
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
```

Add the imports `os`, `path/filepath` to `stats.go` (they are used by `AppStats`). Full import block:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/reconciler/ -run 'TestParseSize|TestParseDockerStats' -v 2>&1 | tail -8`
Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add internal/reconciler/stats.go internal/reconciler/stats_test.go
git commit -m "feat(metrics): docker stats resource sampling"
```

---

### Task 3: Traefik Prometheus scraper + parser

**Files:**
- Create: `internal/metrics/traefik.go`
- Test: `internal/metrics/traefik_test.go`

**Interfaces:**
- Produces:
  - `type TrafficCounters struct { Requests float64; Errors float64; DurationSum float64; DurationCount float64; Buckets map[float64]float64 }` — cumulative counters for one Traefik service
  - `func parseTraefikMetrics(body string) map[string]TrafficCounters` — keyed by service label value (e.g. `app-web@docker`)
  - `func histogramQuantile(q float64, buckets map[float64]float64, total float64) float64` — returns seconds (0 if no data)
  - `func ScrapeTraefik(ctx context.Context, client *http.Client, url string) (map[string]TrafficCounters, error)`

- [ ] **Step 1: Write failing tests**

Create `internal/metrics/traefik_test.go`:

```go
package metrics

import (
	"math"
	"testing"
)

const sampleMetrics = `
# HELP traefik_service_requests_total How many HTTP requests processed on a service.
# TYPE traefik_service_requests_total counter
traefik_service_requests_total{code="200",method="GET",protocol="http",service="app-web@docker"} 90
traefik_service_requests_total{code="500",method="GET",protocol="http",service="app-web@docker"} 10
traefik_service_requests_total{code="200",method="GET",protocol="http",service="app-api@docker"} 5
# HELP traefik_service_request_duration_seconds request duration
# TYPE traefik_service_request_duration_seconds histogram
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="0.1"} 50
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="0.5"} 95
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="1"} 100
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="+Inf"} 100
traefik_service_request_duration_seconds_sum{service="app-web@docker"} 20
traefik_service_request_duration_seconds_count{service="app-web@docker"} 100
`

func TestParseTraefikMetrics(t *testing.T) {
	m := parseTraefikMetrics(sampleMetrics)
	web, ok := m["app-web@docker"]
	if !ok {
		t.Fatal("app-web@docker missing")
	}
	if web.Requests != 100 {
		t.Errorf("requests = %v, want 100", web.Requests)
	}
	if web.Errors != 10 {
		t.Errorf("errors = %v, want 10 (5xx)", web.Errors)
	}
	if web.DurationCount != 100 || web.DurationSum != 20 {
		t.Errorf("duration sum/count = %v/%v", web.DurationSum, web.DurationCount)
	}
	if m["app-api@docker"].Requests != 5 {
		t.Errorf("api requests = %v, want 5", m["app-api@docker"].Requests)
	}
}

func TestHistogramQuantile(t *testing.T) {
	web := parseTraefikMetrics(sampleMetrics)["app-web@docker"]
	// p50 falls in the (0.1,0.5] bucket: 50 already ≤0.1, need 50th of 100,
	// bucket 0.5 holds up to 95 → interpolate within [0.1,0.5].
	p50 := histogramQuantile(0.5, web.Buckets, web.DurationCount)
	if p50 < 0.1 || p50 > 0.5 {
		t.Errorf("p50 = %v, want within (0.1,0.5]", p50)
	}
	p95 := histogramQuantile(0.95, web.Buckets, web.DurationCount)
	if math.Abs(p95-0.5) > 1e-9 {
		t.Errorf("p95 = %v, want 0.5 (cumulative reaches 95 exactly at le=0.5)", p95)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/metrics/ 2>&1 | tail -5`
Expected: FAIL — package/functions undefined.

- [ ] **Step 3: Implement the parser**

Create `internal/metrics/traefik.go`:

```go
// Package metrics collects per-app resource and traffic metrics.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// TrafficCounters holds cumulative Traefik counters for one service.
type TrafficCounters struct {
	Requests      float64            // total requests (all codes)
	Errors        float64            // 5xx requests
	DurationSum   float64            // seconds
	DurationCount float64            // observations
	Buckets       map[float64]float64 // le -> cumulative count (+Inf as math.Inf)
}

// parseLabels extracts the label map from a `{a="1",b="2"}` fragment.
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	for _, part := range splitTopLevel(s) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
	}
	return out
}

// splitTopLevel splits on commas that are not inside quotes.
func splitTopLevel(s string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// parseTraefikMetrics parses the Prometheus exposition text into per-service
// counters. Non-traefik-service lines are ignored.
func parseTraefikMetrics(body string) map[string]TrafficCounters {
	out := map[string]TrafficCounters{}
	get := func(svc string) TrafficCounters {
		c := out[svc]
		if c.Buckets == nil {
			c.Buckets = map[float64]float64{}
		}
		return c
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		brace := strings.IndexByte(line, '{')
		if brace < 0 {
			continue
		}
		name := line[:brace]
		close := strings.LastIndexByte(line, '}')
		if close < 0 {
			continue
		}
		labels := parseLabels(line[brace : close+1])
		svc := labels["service"]
		if svc == "" {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(line[close+1:]), 64)
		if err != nil {
			continue
		}
		switch name {
		case "traefik_service_requests_total":
			c := get(svc)
			c.Requests += val
			if code := labels["code"]; strings.HasPrefix(code, "5") {
				c.Errors += val
			}
			out[svc] = c
		case "traefik_service_request_duration_seconds_sum":
			c := get(svc)
			c.DurationSum = val
			out[svc] = c
		case "traefik_service_request_duration_seconds_count":
			c := get(svc)
			c.DurationCount = val
			out[svc] = c
		case "traefik_service_request_duration_seconds_bucket":
			c := get(svc)
			le, err := strconv.ParseFloat(labels["le"], 64) // "+Inf" -> +Inf
			if err == nil {
				c.Buckets[le] = val
			}
			out[svc] = c
		}
	}
	return out
}

// histogramQuantile estimates the q-quantile (0..1) from cumulative buckets,
// returning seconds. Linear interpolation within the target bucket. Returns 0
// when there is no data.
func histogramQuantile(q float64, buckets map[float64]float64, total float64) float64 {
	if total <= 0 || len(buckets) == 0 {
		return 0
	}
	les := make([]float64, 0, len(buckets))
	for le := range buckets {
		les = append(les, le)
	}
	sort.Float64s(les)
	rank := q * total
	prevLe, prevCount := 0.0, 0.0
	for _, le := range les {
		count := buckets[le]
		if count >= rank {
			if le == prevLe || count == prevCount {
				return le
			}
			// interpolate within (prevLe, le]
			frac := (rank - prevCount) / (count - prevCount)
			if le > 1e300 { // +Inf bucket: no upper bound, return prevLe
				return prevLe
			}
			return prevLe + frac*(le-prevLe)
		}
		prevLe, prevCount = le, count
	}
	return les[len(les)-1]
}

// ScrapeTraefik fetches and parses Traefik's Prometheus endpoint.
func ScrapeTraefik(ctx context.Context, client *http.Client, url string) (map[string]TrafficCounters, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("traefik metrics: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseTraefikMetrics(string(body)), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/metrics/ -v 2>&1 | tail -10`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/metrics/traefik.go internal/metrics/traefik_test.go
git commit -m "feat(metrics): traefik prometheus scraper and parser"
```

---

### Task 4: Collector — combine, persist, publish; River job + wiring

**Files:**
- Create: `internal/metrics/collector.go`
- Test: `internal/metrics/collector_test.go`
- Create: `internal/jobs/metrics.go` (River worker)
- Modify: `internal/jobs/client.go` (register worker + 15s periodic job; extend `NewClient` signature)
- Modify: `cmd/server/main.go:81` (pass `hub` and `provider` to `NewClient`)

**Interfaces:**
- Consumes: `sqlc.Queries` (Task 1), `reconciler.Docker.AppStats` (Task 2), `metrics.ScrapeTraefik`/`TrafficCounters` (Task 3), `events.Hub.Publish` (`events/hub.go:35`).
- Produces:
  - `type Sample struct { AppID string; TS time.Time; CPUPercent float64; MemBytes, MemLimitBytes, NetRxBytes, NetTxBytes int64; ReqRate, ErrRate, P50Ms, P95Ms float64 }` (json-tagged snake_case)
  - `type StatSource interface { AppStats(ctx context.Context, appID string) (reconciler.ContainerStats, bool, error) }`
  - `func NewCollector(pool *pgxpool.Pool, hub *events.Hub, stats StatSource, traefikURL string) *Collector`
  - `func (c *Collector) CollectOnce(ctx context.Context) error` — samples all apps once
  - `func (c *Collector) sampleTraffic(prev, cur metrics.TrafficCounters, dt float64) (reqRate, errRate, p50, p95 float64)` (unexported, tested)

- [ ] **Step 1: Write failing test (traffic delta math)**

Create `internal/metrics/collector_test.go`:

```go
package metrics

import (
	"math"
	"testing"
)

func TestSampleTrafficRates(t *testing.T) {
	c := &Collector{}
	prev := TrafficCounters{Requests: 100, Errors: 5, DurationSum: 10, DurationCount: 100}
	cur := TrafficCounters{
		Requests: 130, Errors: 11, DurationSum: 13, DurationCount: 130,
		Buckets: map[float64]float64{0.1: 60, 0.5: 125, math.Inf(1): 130},
	}
	reqRate, errRate, _, p95 := c.sampleTraffic(prev, cur, 15)
	if math.Abs(reqRate-2.0) > 1e-9 { // (130-100)/15
		t.Errorf("reqRate = %v, want 2", reqRate)
	}
	if math.Abs(errRate-(6.0/30.0)) > 1e-9 { // 6 new errors / 30 new reqs
		t.Errorf("errRate = %v, want 0.2", errRate)
	}
	if p95 <= 0 {
		t.Errorf("p95 = %v, want > 0", p95)
	}
}

func TestSampleTrafficNoTraffic(t *testing.T) {
	c := &Collector{}
	prev := TrafficCounters{Requests: 50}
	cur := TrafficCounters{Requests: 50}
	reqRate, errRate, p50, p95 := c.sampleTraffic(prev, cur, 15)
	if reqRate != 0 || errRate != 0 || p50 != 0 || p95 != 0 {
		t.Errorf("expected all zero, got %v %v %v %v", reqRate, errRate, p50, p95)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/metrics/ -run TestSampleTraffic 2>&1 | tail -5`
Expected: FAIL — `Collector` / `sampleTraffic` undefined.

- [ ] **Step 3: Implement the collector**

Create `internal/metrics/collector.go`:

```go
package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StatSource yields a resource sample for one app (satisfied by *reconciler.Docker).
type StatSource interface {
	AppStats(ctx context.Context, appID string) (reconciler.ContainerStats, bool, error)
}

// Sample is a single app's combined resource+traffic reading, published live.
type Sample struct {
	AppID         string    `json:"app_id"`
	TS            time.Time `json:"ts"`
	CPUPercent    float64   `json:"cpu_pct"`
	MemBytes      int64     `json:"mem_bytes"`
	MemLimitBytes int64     `json:"mem_limit_bytes"`
	NetRxBytes    int64     `json:"net_rx_bytes"`
	NetTxBytes    int64     `json:"net_tx_bytes"`
	ReqRate       float64   `json:"req_rate"`
	ErrRate       float64   `json:"err_rate"`
	P50Ms         float64   `json:"p50_ms"`
	P95Ms         float64   `json:"p95_ms"`
}

// Collector samples all running apps on demand, persisting history and
// publishing live samples to the hub on topic "metrics:<appID>".
type Collector struct {
	pool       *pgxpool.Pool
	hub        *events.Hub
	stats      StatSource
	traefikURL string
	client     *http.Client

	mu   sync.Mutex
	prev map[string]TrafficCounters // service label -> last counters
	last time.Time
}

func NewCollector(pool *pgxpool.Pool, hub *events.Hub, stats StatSource, traefikURL string) *Collector {
	return &Collector{
		pool: pool, hub: hub, stats: stats, traefikURL: traefikURL,
		client: &http.Client{Timeout: 3 * time.Second},
		prev:   map[string]TrafficCounters{},
	}
}

// MetricsTopic is the hub topic for one app's live samples.
func MetricsTopic(appID string) string { return "metrics:" + appID }

// sampleTraffic converts cumulative counters into per-interval rates.
func (c *Collector) sampleTraffic(prev, cur TrafficCounters, dt float64) (reqRate, errRate, p50, p95 float64) {
	if dt <= 0 {
		return 0, 0, 0, 0
	}
	dReq := cur.Requests - prev.Requests
	if dReq < 0 {
		dReq = cur.Requests // counter reset (traefik restart)
	}
	dErr := cur.Errors - prev.Errors
	if dErr < 0 {
		dErr = cur.Errors
	}
	reqRate = dReq / dt
	if dReq > 0 {
		errRate = dErr / dReq
	}
	if dReq > 0 && len(cur.Buckets) > 0 {
		p50 = histogramQuantile(0.5, cur.Buckets, cur.DurationCount) * 1000
		p95 = histogramQuantile(0.95, cur.Buckets, cur.DurationCount) * 1000
	}
	return reqRate, errRate, p50, p95
}

// CollectOnce samples every app, writes rows, and publishes live samples.
func (c *Collector) CollectOnce(ctx context.Context) error {
	q := sqlc.New(c.pool)
	apps, err := q.ListAllAppIDs(ctx)
	if err != nil {
		return err
	}

	// Scrape Traefik once for the whole tick (best-effort: traffic is 0 if down).
	traffic, _ := ScrapeTraefik(ctx, c.client, c.traefikURL)

	c.mu.Lock()
	now := time.Now()
	dt := 15.0
	if !c.last.IsZero() {
		dt = now.Sub(c.last).Seconds()
	}
	prevAll := c.prev
	c.prev = traffic
	c.last = now
	c.mu.Unlock()

	for _, a := range apps {
		appID := uuidString(a.ID)
		rs, running, err := c.stats.AppStats(ctx, appID)
		if err != nil || !running {
			continue // not running or transient docker error — skip this tick
		}
		svc := "app-" + a.Slug + "@docker"
		reqRate, errRate, p50, p95 := c.sampleTraffic(prevAll[svc], traffic[svc], dt)

		if err := q.CreateAppMetric(ctx, sqlc.CreateAppMetricParams{
			AppID: a.ID, CpuPct: rs.CPUPercent,
			MemBytes: rs.MemBytes, MemLimitBytes: rs.MemLimitBytes,
			NetRxBytes: rs.NetRxBytes, NetTxBytes: rs.NetTxBytes,
			ReqRate: reqRate, ErrRate: errRate, P50Ms: p50, P95Ms: p95,
		}); err != nil {
			return err
		}
		s := Sample{
			AppID: appID, TS: now, CPUPercent: rs.CPUPercent,
			MemBytes: rs.MemBytes, MemLimitBytes: rs.MemLimitBytes,
			NetRxBytes: rs.NetRxBytes, NetTxBytes: rs.NetTxBytes,
			ReqRate: reqRate, ErrRate: errRate, P50Ms: p50, P95Ms: p95,
		}
		if b, err := json.Marshal(s); err == nil {
			c.hub.Publish(MetricsTopic(appID), b)
		}
	}
	return nil
}

func uuidString(id pgtype.UUID) string {
	v, _ := id.Value()
	s, _ := v.(string)
	return s
}
```

Note: this needs a new sqlc query `ListAllAppIDs`. Add to `internal/db/queries/apps.sql`:

```sql
-- name: ListAllAppIDs :many
SELECT id, slug FROM applications;
```

Then run `sqlc generate` (produces `ListAllAppIDsRow{ID pgtype.UUID; Slug string}`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `sqlc generate && go test ./internal/metrics/ -run TestSampleTraffic -v 2>&1 | tail -8`
Expected: PASS for both.

- [ ] **Step 5: Create the River worker**

Create `internal/jobs/metrics.go`:

```go
package jobs

import (
	"context"

	"github.com/bograh/cargo/internal/metrics"
	"github.com/riverqueue/river"
)

type MetricsArgs struct{}

func (MetricsArgs) Kind() string { return "collect_metrics" }

type MetricsWorker struct {
	river.WorkerDefaults[MetricsArgs]
	Collector *metrics.Collector
}

func (w *MetricsWorker) Work(ctx context.Context, _ *river.Job[MetricsArgs]) error {
	return w.Collector.CollectOnce(ctx)
}
```

- [ ] **Step 6: Register worker + periodic job; extend NewClient**

In `internal/jobs/client.go`, change the `NewClient` signature and body. Replace the signature line and add the worker + periodic job:

```go
// NewClient builds the River client. collector may be nil (metrics disabled).
func NewClient(pool *pgxpool.Pool, p *Pipeline, dbSvc *databases.Service, collector *metrics.Collector) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &DeployWorker{P: p})
	river.AddWorker(workers, &PruneWorker{Pool: pool, Deps: p.Deployments})
	river.AddWorker(workers, &DomainCheckWorker{Pool: pool})
	river.AddWorker(workers, &HousekeepingWorker{Pool: pool})
	river.AddWorker(workers, &DBProvisionWorker{Databases: dbSvc})
	if collector != nil {
		river.AddWorker(workers, &MetricsWorker{Collector: collector})
	}
	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return PruneArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(10*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return DomainCheckArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return HousekeepingArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
	if collector != nil {
		periodic = append(periodic, river.NewPeriodicJob(
			river.PeriodicInterval(15*time.Second),
			func() (river.JobArgs, *river.InsertOpts) { return MetricsArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		))
	}
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			"deploy":           {MaxWorkers: 2},
			river.QueueDefault: {MaxWorkers: 2},
		},
		Workers:      workers,
		PeriodicJobs: periodic,
	})
}
```

Add the import `"github.com/bograh/cargo/internal/metrics"` to `client.go`.

- [ ] **Step 7: Wire the collector in main.go**

In `cmd/server/main.go`, after `provider := reconciler.NewDocker(cfg.DataDir)` (line 66) and before `jobs.NewClient`, add:

```go
	traefikURL := getenvDefault("CARGO_TRAEFIK_METRICS_URL", "http://traefik:8082/metrics")
	collector := metrics.NewCollector(pool, hub, provider, traefikURL)
```

Change the `NewClient` call (line 81) to:

```go
	client, err := jobs.NewClient(pool, pipeline, dbSvc, collector)
```

Add imports `"github.com/bograh/cargo/internal/metrics"` and (if not present) a small helper. If `getenvDefault` does not already exist in the package, add it to `cmd/server/main.go`:

```go
func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

(Confirm with `grep -n "getenvDefault\|\"os\"" cmd/server/main.go` before adding, to avoid a duplicate or missing import.)

- [ ] **Step 8: Build and run metrics tests**

Run: `go build ./... && go test ./internal/metrics/ 2>&1 | tail -5`
Expected: clean build; metrics unit tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/metrics/collector.go internal/metrics/collector_test.go internal/jobs/metrics.go internal/jobs/client.go internal/db/queries/apps.sql internal/db/sqlc/ cmd/server/main.go
git commit -m "feat(metrics): collector, river job, and wiring"
```

---

### Task 5: 48h retention prune

**Files:**
- Modify: `internal/jobs/housekeeping.go` (call `PurgeAppMetrics` in `RunHousekeeping`)
- Modify: `internal/jobs/housekeeping_test.go` (extend assertion if present, else add a row+purge test)

**Interfaces:**
- Consumes: `sqlc.Queries.PurgeAppMetrics(ctx) (int64, error)` (Task 1).

- [ ] **Step 1: Update RunHousekeeping**

In `internal/jobs/housekeeping.go`, extend `RunHousekeeping` to also purge metrics. Replace the function:

```go
// RunHousekeeping deletes expired/stale sessions, dead invites, and metrics
// older than the 48h retention window.
func RunHousekeeping(ctx context.Context, pool *pgxpool.Pool) (sessions, invites int64, err error) {
	q := sqlc.New(pool)
	sessions, err = q.PurgeSessions(ctx)
	if err != nil {
		return 0, 0, err
	}
	invites, err = q.PurgeInvites(ctx)
	if err != nil {
		return sessions, invites, err
	}
	if _, err = q.PurgeAppMetrics(ctx); err != nil {
		return sessions, invites, err
	}
	return sessions, invites, nil
}
```

- [ ] **Step 2: Add a retention test**

Append to `internal/jobs/housekeeping_test.go` (uses the package's existing `startPool` testcontainers helper — confirm its name with `grep -n "func startPool\|func setup" internal/jobs/*_test.go`; adjust the pool acquisition line to match the existing pattern in that file):

```go
func TestPurgeAppMetrics(t *testing.T) {
	pool := startPool(t)
	ctx := context.Background()
	q := sqlc.New(pool)
	// Seed an org+app to satisfy the FK.
	var appID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		WITH o AS (INSERT INTO organizations (name) VALUES ('m') RETURNING id)
		INSERT INTO applications (org_id, name, slug, source_type, image_ref, exposed_port)
		SELECT id, 'a', 'a-metrics', 'image', 'nginx', 80 FROM o RETURNING id`).Scan(&appID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// One fresh row, one 49h-old row.
	if err := q.CreateAppMetric(ctx, sqlc.CreateAppMetricParams{AppID: appID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_metrics (app_id, created_at) VALUES ($1, now() - interval '49 hours')`, appID); err != nil {
		t.Fatal(err)
	}
	n, err := q.PurgeAppMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1", n)
	}
}
```

Add imports as needed (`context`, `github.com/bograh/cargo/internal/db/sqlc`, `github.com/jackc/pgx/v5/pgtype`). If the `organizations` insert columns differ, adjust to the real schema (`grep -n "CREATE TABLE organizations" internal/db/migrations/*.sql`).

- [ ] **Step 3: Run the test**

Run: `go test ./internal/jobs/ -run TestPurgeAppMetrics -v 2>&1 | tail -8`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/jobs/housekeeping.go internal/jobs/housekeeping_test.go
git commit -m "feat(metrics): purge metrics older than 48h in housekeeping"
```

---

### Task 6: HTTP endpoints (history JSON + live SSE)

**Files:**
- Create: `internal/api/metrics.go`
- Test: `internal/api/metrics_test.go`
- Modify: `internal/api/router.go` (add routes under `/apps/{appID}`)
- Modify: `internal/api/server.go` (add `MetricService` seam + field + wire)

**Interfaces:**
- Consumes: `events.Hub` (already on `Server` as `s.hub`), `metrics.MetricsTopic`, `sqlc` metrics queries, `s.apps.Get` (role gate).
- Produces:
  - `GET /apps/{appID}/metrics?window=6h|24h|48h` → `200 [{ts, cpu_pct, mem_bytes, ...}]` (same JSON shape as `metrics.Sample`, oldest→newest)
  - `GET /apps/{appID}/metrics/stream` → SSE `data: {sample json}` frames (latest row replayed first, then live)

- [ ] **Step 1: Add a metrics store seam to the Server**

In `internal/api/server.go`, add near the other interfaces:

```go
type MetricStore interface {
	ListSince(ctx context.Context, appID pgtype.UUID, since time.Time) ([]sqlc.AppMetric, error)
	Latest(ctx context.Context, appID pgtype.UUID) (sqlc.AppMetric, error)
}
```

Add a field to `Server`: `metrics MetricStore` and a setter (or extend an existing `Wire...`). Add:

```go
// WireMetrics attaches the metrics store used by the metrics endpoints.
func (s *Server) WireMetrics(m MetricStore) { s.metrics = m }
```

Implement the store as a thin sqlc wrapper. Create it in `internal/metrics/store.go`:

```go
package metrics

import (
	"context"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ q *sqlc.Queries }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: sqlc.New(pool)} }

func (s *Store) ListSince(ctx context.Context, appID pgtype.UUID, since time.Time) ([]sqlc.AppMetric, error) {
	return s.q.ListAppMetricsSince(ctx, sqlc.ListAppMetricsSinceParams{
		AppID: appID, CreatedAt: pgtype.Timestamptz{Time: since, Valid: true},
	})
}

func (s *Store) Latest(ctx context.Context, appID pgtype.UUID) (sqlc.AppMetric, error) {
	return s.q.LatestAppMetric(ctx, appID)
}
```

Wire it in `cmd/server/main.go` after `srv.WireDeployments(...)` (line 98):

```go
	srv.WireMetrics(metrics.NewStore(pool))
```

- [ ] **Step 2: Write the handlers**

Create `internal/api/metrics.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	appmetrics "github.com/bograh/cargo/internal/metrics"
	"github.com/jackc/pgx/v5"
)

func metricJSON(m sqlc.AppMetric) map[string]any {
	return map[string]any{
		"ts":              m.CreatedAt.Time,
		"cpu_pct":         m.CpuPct,
		"mem_bytes":       m.MemBytes,
		"mem_limit_bytes": m.MemLimitBytes,
		"net_rx_bytes":    m.NetRxBytes,
		"net_tx_bytes":    m.NetTxBytes,
		"req_rate":        m.ReqRate,
		"err_rate":        m.ErrRate,
		"p50_ms":          m.P50Ms,
		"p95_ms":          m.P95Ms,
	}
}

func windowFrom(r *http.Request) time.Duration {
	switch r.URL.Query().Get("window") {
	case "6h":
		return 6 * time.Hour
	case "48h":
		return 48 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// handleAppMetrics returns the app's metric history within the requested window.
func (s *Server) handleAppMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	if _, err := s.apps.Get(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		appError(w, err)
		return
	}
	rows, err := s.metrics.ListSince(r.Context(), id, time.Now().Add(-windowFrom(r)))
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, metricJSON(m))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAppMetricsStream streams live samples over SSE (latest row first).
func (s *Server) handleAppMetricsStream(w http.ResponseWriter, r *http.Request) {
	id, ok := appIDParam(w, r)
	if !ok {
		return
	}
	if _, err := s.apps.Get(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		appError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		Error(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	appIDStr := uuidString(id)
	ch, cancel := s.hub.Subscribe(appmetrics.MetricsTopic(appIDStr))
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	send := func(b []byte) { _, _ = fmt.Fprintf(w, "data: %s\n\n", b); flusher.Flush() }

	// Replay the latest stored sample so the chart shows data immediately.
	if m, err := s.metrics.Latest(r.Context(), id); err == nil {
		if b, err := json.Marshal(metricJSON(m)); err == nil {
			send(b)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		// non-fatal; continue to live stream
		_ = err
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			send(msg)
		}
	}
}

// ensure context import is used even if the compiler prunes (handlers use r.Context()).
var _ = context.Background
```

Remove the trailing `var _ = context.Background` line if `context` is otherwise used; it's a guard against an unused import — delete it and the `context` import together if `goimports`/build complains.

- [ ] **Step 3: Register routes**

In `internal/api/router.go`, inside `r.Route("/apps/{appID}", ...)` after `r.Get("/logs", s.handleAppLogs)` (line 73):

```go
				r.Get("/metrics", s.handleAppMetrics)
				r.Get("/metrics/stream", s.handleAppMetricsStream)
```

- [ ] **Step 4: Write a handler test (history endpoint, stubbed store)**

Create `internal/api/metrics_test.go`. Mirror the existing `apps_test.go` server harness (`appServer(...)`), adding a stub `MetricStore`. Inspect `apps_test.go` for the exact harness helper name/signature first (`grep -n "func appServer\|func newTestServer\|WireDeployments" internal/api/apps_test.go`), then:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubMetrics struct{ rows []sqlc.AppMetric }

func (s stubMetrics) ListSince(_ context.Context, _ pgtype.UUID, _ time.Time) ([]sqlc.AppMetric, error) {
	return s.rows, nil
}
func (s stubMetrics) Latest(_ context.Context, _ pgtype.UUID) (sqlc.AppMetric, error) {
	if len(s.rows) == 0 {
		return sqlc.AppMetric{}, nil
	}
	return s.rows[len(s.rows)-1], nil
}

func TestHandleAppMetrics(t *testing.T) {
	// Build the server via the same harness apps_test.go uses; then:
	//   srv.WireMetrics(stubMetrics{rows: []sqlc.AppMetric{{CpuPct: 5}}})
	// and set srv.apps to a stub returning an app for the actor (see stubApps).
	// This skeleton documents intent; fill in using appServer(stubApps{app: ...}).
	srv := appServer(stubApps{app: sqlc.Application{}})
	srv.WireMetrics(stubMetrics{rows: []sqlc.AppMetric{{CpuPct: 5}}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/00000000-0000-0000-0000-000000000000/metrics?window=6h", nil)
	req = withTestUser(req) // use whatever the harness uses to inject an authed user
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["cpu_pct"].(float64) != 5 {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
```

IMPORTANT: `appServer`, `withTestUser`, and `srv.Router()` are placeholders — replace with the real harness names found in `apps_test.go`/`server.go`. The test's job is: stubbed apps (auth passes) + stubbed metrics → assert JSON. If the harness routes require a real chi mux getter, use it; otherwise call the handler through the mounted router the harness exposes.

- [ ] **Step 5: Run tests + build**

Run: `go build ./... && go test ./internal/api/ -run TestHandleAppMetrics -v 2>&1 | tail -10`
Expected: clean build; test PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go internal/api/router.go internal/api/server.go internal/metrics/store.go cmd/server/main.go
git commit -m "feat(metrics): history and live SSE endpoints"
```

---

### Task 7: Frontend — types, icon, route, nav tab

**Files:**
- Modify: `web/src/lib/types.ts` (add `AppMetric` type)
- Modify: `web/src/components/ui/Icon.tsx` (add `activity` icon)
- Modify: `web/src/App.tsx` (import + route)
- Modify: `web/src/components/layout/AppScopeNav.tsx` (nav item)

**Interfaces:**
- Produces: `AppMetric` TS type; `/apps/:appId/metrics` route; sidebar "Metrics" tab.

- [ ] **Step 1: Add the type**

In `web/src/lib/types.ts`, add:

```ts
export interface AppMetric {
  ts: string;
  cpu_pct: number;
  mem_bytes: number;
  mem_limit_bytes: number;
  net_rx_bytes: number;
  net_tx_bytes: number;
  req_rate: number;
  err_rate: number;
  p50_ms: number;
  p95_ms: number;
}
```

- [ ] **Step 2: Add an `activity` icon**

In `web/src/components/ui/Icon.tsx`, add to the `ICONS` object (before the closing `}`):

```tsx
  activity: (<polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />),
```

- [ ] **Step 3: Add the route**

In `web/src/App.tsx`, add the import near line 14 (`import AppLogs from "./pages/AppLogs";`):

```tsx
import AppMetrics from "./pages/AppMetrics";
```

And the route after the logs route (line 40):

```tsx
        <Route path="/apps/:appId/metrics" element={<AppMetrics />} />
```

- [ ] **Step 4: Add the nav tab**

In `web/src/components/layout/AppScopeNav.tsx`, after the Logs `NavItem` (line 51):

```tsx
        <NavItem to={`/apps/${appId}/metrics`} icon="activity" label="Metrics" />
```

- [ ] **Step 5: Typecheck (page not created yet — expect the missing-module error, confirming wiring)**

Run: `cd web && npx tsc -b 2>&1 | head -5`
Expected: error `Cannot find module './pages/AppMetrics'` — proves the route/import is wired; resolved by Task 8. (Do not commit yet; commit with Task 8.)

---

### Task 8: Frontend — AppMetrics page (history + live charts)

**Files:**
- Create: `web/src/components/ui/Sparkline.tsx` (tiny inline SVG chart)
- Test: `web/src/components/ui/Sparkline.test.tsx`
- Create: `web/src/pages/AppMetrics.tsx`
- Test: `web/src/pages/AppMetrics.test.tsx`

**Interfaces:**
- Consumes: `AppMetric` (Task 7), `api` helper, `EventSource` (`/api/v1/apps/:appId/metrics/stream`), history `GET /apps/:appId/metrics?window=`.
- Produces: `Sparkline({ points, ...})` component and the `AppMetrics` default-export page.

- [ ] **Step 1: Write the Sparkline test**

Create `web/src/components/ui/Sparkline.test.tsx`:

```tsx
import { render } from "@testing-library/react";
import { Sparkline } from "./Sparkline";

it("renders a polyline for the given points", () => {
  const { container } = render(<Sparkline points={[0, 5, 2, 8]} width={100} height={20} />);
  const poly = container.querySelector("polyline");
  expect(poly).toBeTruthy();
  // 4 points -> 4 "x,y" pairs
  expect(poly!.getAttribute("points")!.trim().split(" ").length).toBe(4);
});

it("renders nothing meaningful for empty data without crashing", () => {
  const { container } = render(<Sparkline points={[]} width={100} height={20} />);
  expect(container.querySelector("svg")).toBeTruthy();
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npx vitest run Sparkline 2>&1 | tail -8`
Expected: FAIL — cannot find `./Sparkline`.

- [ ] **Step 3: Implement Sparkline**

Create `web/src/components/ui/Sparkline.tsx`:

```tsx
export function Sparkline({
  points,
  width = 240,
  height = 48,
  className,
}: {
  points: number[];
  width?: number;
  height?: number;
  className?: string;
}) {
  const pad = 2;
  const max = Math.max(1, ...points);
  const min = Math.min(0, ...points);
  const span = max - min || 1;
  const stepX = points.length > 1 ? (width - pad * 2) / (points.length - 1) : 0;
  const coords = points.map((v, i) => {
    const x = pad + i * stepX;
    const y = height - pad - ((v - min) / span) * (height - pad * 2);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className={className} preserveAspectRatio="none">
      {coords.length > 1 && (
        <polyline
          points={coords.join(" ")}
          fill="none"
          stroke="currentColor"
          strokeWidth={1.5}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
      )}
    </svg>
  );
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npx vitest run Sparkline 2>&1 | tail -8`
Expected: PASS (2 tests).

- [ ] **Step 5: Write the page test**

Create `web/src/pages/AppMetrics.test.tsx`:

```tsx
import { screen } from "@testing-library/react";
import { mockApi, renderPage } from "../test/utils";
import AppMetrics from "./AppMetrics";

const APP = {
  id: "a1", org_id: "o1", name: "web", slug: "web", source_type: "image",
  image_ref: "nginx", exposed_port: 80, healthcheck_path: "/", auto_deploy: true,
  builder: "auto", git_repo_url: "", git_branch: "", build_context: ".", dockerfile_path: "Dockerfile",
  has_registry_credentials: false, desired_state: "running",
};

it("shows metric tiles from history", async () => {
  mockApi({
    "GET /auth/me": { status: 200, body: { id: "u1", email: "a@b.c", is_instance_admin: false } },
    "GET /apps/a1": { status: 200, body: APP },
    "GET /apps/a1/metrics": {
      status: 200,
      body: [
        { ts: "2026-07-22T00:00:00Z", cpu_pct: 12.5, mem_bytes: 67108864, mem_limit_bytes: 536870912, net_rx_bytes: 0, net_tx_bytes: 0, req_rate: 3, err_rate: 0.1, p50_ms: 20, p95_ms: 80 },
      ],
    },
  });
  renderPage(<AppMetrics />, { path: "/apps/:appId/metrics", route: "/apps/a1/metrics" });
  expect(await screen.findByRole("heading", { name: "Metrics" })).toBeInTheDocument();
  expect(await screen.findByText(/CPU/i)).toBeInTheDocument();
});
```

Note: `EventSource` is not implemented in jsdom. Add a minimal stub in `web/src/test/setup.ts` if not already present (check first with `grep -n EventSource web/src/test/setup.ts`):

```ts
class MockEventSource {
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {}
  close() {}
}
// @ts-expect-error jsdom has no EventSource
globalThis.EventSource = globalThis.EventSource ?? MockEventSource;
```

- [ ] **Step 6: Run to verify it fails**

Run: `cd web && npx vitest run AppMetrics 2>&1 | tail -8`
Expected: FAIL — cannot find `./AppMetrics`.

- [ ] **Step 7: Implement the page**

Create `web/src/pages/AppMetrics.tsx`:

```tsx
import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useApp } from "../lib/hooks";
import type { AppMetric } from "../lib/types";
import { Card, EmptyState, PageHeader, Skeleton, StatusDot } from "../components/ui";
import { Sparkline } from "../components/ui/Sparkline";

const MAX_POINTS = 240; // ~1h at 15s

function fmtBytes(n: number) {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${u[i]}`;
}

export default function AppMetrics() {
  const { appId } = useParams();
  const { data: app, isLoading, error } = useApp(appId);
  const [live, setLive] = useState<AppMetric[]>([]);
  const [connected, setConnected] = useState(false);
  const seeded = useRef(false);

  const { data: history } = useQuery({
    queryKey: ["metrics", appId],
    queryFn: () => api<AppMetric[]>(`/apps/${appId}/metrics?window=24h`),
    enabled: !!appId,
  });

  // Seed the live buffer from history once.
  useEffect(() => {
    if (history && !seeded.current) {
      setLive(history.slice(-MAX_POINTS));
      seeded.current = true;
    }
  }, [history]);

  useEffect(() => {
    if (!appId) return;
    const es = new EventSource(`/api/v1/apps/${appId}/metrics/stream`);
    es.onopen = () => setConnected(true);
    es.onmessage = (e) => {
      try {
        const m = JSON.parse(e.data) as AppMetric;
        setLive((prev) => [...prev, m].slice(-MAX_POINTS));
      } catch {
        /* ignore malformed frame */
      }
    };
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, [appId]);

  if (isLoading) return <Skeleton className="h-64" />;
  if (error || !app) return <EmptyState title="Application not found" />;

  const latest = live[live.length - 1];
  const series = (sel: (m: AppMetric) => number) => live.map(sel);

  const tiles = [
    { label: "CPU", value: latest ? `${latest.cpu_pct.toFixed(1)} %` : "—", points: series((m) => m.cpu_pct) },
    { label: "Memory", value: latest ? fmtBytes(latest.mem_bytes) : "—", points: series((m) => m.mem_bytes) },
    { label: "Requests/s", value: latest ? latest.req_rate.toFixed(2) : "—", points: series((m) => m.req_rate) },
    { label: "Errors", value: latest ? `${(latest.err_rate * 100).toFixed(1)} %` : "—", points: series((m) => m.err_rate) },
    { label: "Latency p50", value: latest ? `${latest.p50_ms.toFixed(0)} ms` : "—", points: series((m) => m.p50_ms) },
    { label: "Latency p95", value: latest ? `${latest.p95_ms.toFixed(0)} ms` : "—", points: series((m) => m.p95_ms) },
  ];

  return (
    <div>
      <PageHeader
        eyebrow="app"
        title="Metrics"
        back={{ to: `/apps/${app.id}`, label: app.name }}
        actions={
          <span className="flex items-center gap-2 text-xs text-muted">
            <StatusDot status={connected ? "live" : "failed"} />
            {connected ? "streaming" : "disconnected"}
          </span>
        }
      />
      {live.length === 0 ? (
        <EmptyState title="No metrics yet" hint="Metrics appear ~15s after the app is running." />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {tiles.map((t) => (
            <Card key={t.label}>
              <div className="flex items-baseline justify-between">
                <span className="font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">{t.label}</span>
                <span className="font-display text-lg font-semibold">{t.value}</span>
              </div>
              <Sparkline points={t.points} className="mt-3 w-full text-amber" height={48} />
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
```

Confirm `Card` is exported from `../components/ui` (`grep -n "Card" web/src/components/ui/index.ts`); it is used by `DatabasesTab.tsx`, so it exists.

- [ ] **Step 8: Run page + full frontend tests, typecheck, build**

Run: `cd web && npx vitest run AppMetrics Sparkline 2>&1 | tail -8`
Expected: PASS.

Run: `cd web && npx tsc -b 2>&1 | head -5 && npm run build 2>&1 | tail -3`
Expected: clean typecheck; successful build.

- [ ] **Step 9: Commit (Tasks 7 + 8 together — the page resolves the route added in Task 7)**

```bash
git add web/src/lib/types.ts web/src/components/ui/Icon.tsx web/src/App.tsx web/src/components/layout/AppScopeNav.tsx web/src/components/ui/Sparkline.tsx web/src/components/ui/Sparkline.test.tsx web/src/pages/AppMetrics.tsx web/src/pages/AppMetrics.test.tsx web/src/test/setup.ts internal/webui/dist/index.html
git commit -m "feat(metrics): Metrics tab with live charts and history"
```

---

### Task 9: Enable Traefik Prometheus metrics (deploy config)

**Files:**
- Modify: `deploy/docker-compose.yml` (Traefik `command:` flags)
- Modify: `deploy/docker-compose.tls.yml`, `deploy/docker-compose.dns01.yml`, `deploy/docker-compose.dev.yml` (only if they override Traefik's `command:` — compose replaces the list wholesale, so the metrics flags must be replicated there)

**Interfaces:**
- Produces: Traefik serving Prometheus metrics on internal `:8082`, reachable at `http://traefik:8082/metrics` from the controlplane (matches `CARGO_TRAEFIK_METRICS_URL` default from Task 4).

- [ ] **Step 1: Add metrics flags to the base compose**

In `deploy/docker-compose.yml`, append to the traefik `command:` list (after `--entrypoints.websecure.http.tls=true`, line 45):

```yaml
      - --entrypoints.metrics.address=:8082
      - --metrics.prometheus=true
      - --metrics.prometheus.entrypoint=metrics
      - --metrics.prometheus.addServicesLabels=true
```

Do NOT add `8082` to the `ports:` list — it stays internal to `cargo-proxy`.

- [ ] **Step 2: Replicate in any overlay that redefines Traefik `command:`**

Run: `grep -l "command:" deploy/docker-compose.tls.yml deploy/docker-compose.dns01.yml deploy/docker-compose.dev.yml`
For each file listed, add the same four flags to its traefik `command:` block (compose merges services but REPLACES list-valued keys like `command:` entirely, so the flags must be present in whichever `command:` wins). If a file does not define `command:` for traefik, it inherits the base — no change needed.

- [ ] **Step 3: Validate compose syntax**

Run: `docker compose -f deploy/docker-compose.yml config >/dev/null && echo OK`
Expected: `OK` (requires the `.env` keys; if `config` complains about missing env, run with a throwaway env: `CARGO_PLATFORM_DOMAIN=localhost CARGO_MASTER_KEY=x CARGO_DB_PASSWORD=x docker compose -f deploy/docker-compose.yml config >/dev/null && echo OK`).

- [ ] **Step 4: Commit**

```bash
git add deploy/docker-compose.yml deploy/docker-compose.tls.yml deploy/docker-compose.dns01.yml deploy/docker-compose.dev.yml
git commit -m "feat(metrics): enable traefik prometheus metrics on internal :8082"
```

---

### Task 10: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Full backend build + vet**

Run: `go build ./... && go vet ./... 2>&1 | grep -v '^#' | head`
Expected: clean.

- [ ] **Step 2: Backend tests for touched packages**

Run: `go test ./internal/metrics/ ./internal/reconciler/ ./internal/apps/ ./internal/api/ ./internal/jobs/ 2>&1 | tail -20`
Expected: all `ok`. (These spin testcontainers Postgres and, for reconciler, real Docker — allow a few minutes.)

- [ ] **Step 3: Full frontend suite + build**

Run: `cd web && npx tsc -b && npx vitest run 2>&1 | tail -6 && npm run build 2>&1 | tail -3`
Expected: typecheck clean, all tests pass, build succeeds.

- [ ] **Step 4: Manual smoke (optional, requires the running stack)**

Bring up the stack (`deploy/install.sh` or the dev compose), deploy an app, open its Metrics tab. Expect: within ~15s, tiles populate and the "streaming" dot goes live; generating traffic to the app's URL moves Requests/s, latency, and (on induced 5xx) the Errors tile.

---

## Self-Review

**Spec coverage:**
- Live streaming → Task 6 SSE endpoint + Task 8 `EventSource`. ✓
- History → Task 1 table + Task 6 history endpoint + Task 8 initial fetch. ✓
- 48h retention → Task 1 `PurgeAppMetrics` + Task 5 housekeeping call. ✓
- Resource metrics → Task 2 (`docker stats`). ✓
- Traffic metrics (req/s, p50/p95, errors) → Task 3 parser + Task 4 collector. ✓
- No new platform containers / internal-only Traefik scrape → Task 9. ✓
- UI tab in app scope → Task 7 + Task 8. ✓

**Placeholder scan:** The api handler test (Task 6 Step 4) and housekeeping test (Task 5 Step 2) intentionally reference harness/schema names that MUST be confirmed against the real files (`appServer`/`withTestUser`/`startPool`/`organizations` columns) — each such spot is called out inline with the exact `grep` to run. These are not silent placeholders; they are "verify-then-fill" points because the harness names could not be confirmed without reading those test files during planning.

**Type consistency:** `Sample`/`AppMetric` field names are snake_case JSON in Go (`internal/metrics/collector.go`) and match the TS `AppMetric` (Task 7) and `metricJSON` (Task 6). sqlc field names (`CpuPct`, `MemBytes`, …) match Task 1's declared struct and are used identically in Tasks 4 and 6. `MetricsTopic(appID)` is defined once (Task 4) and consumed in Task 6. `AppStats` signature is identical in Task 2 (definition), Task 4 (`StatSource`), and consumed by the collector.

**Known follow-ups (out of scope, note only):** downsampling for long windows (48h at 15s ≈ 11.5k points/app — the UI caps the live buffer at `MAX_POINTS`; the 48h history endpoint could return large arrays and may warrant server-side bucketing later); River job-table churn at 15s cadence (acceptable, auto-pruned by River) — revisit if it becomes noisy.
