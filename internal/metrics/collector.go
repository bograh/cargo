package metrics

import (
	"context"
	"encoding/json"
	"math"
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

	runMu sync.Mutex // guards against overlapping CollectOnce ticks

	// host, when set, adds a whole-server sample to each tick. Optional so the
	// collector still works in tests with no /proc or docker socket.
	host *HostCollector
}

// WithHost enables whole-server sampling on each tick.
func (c *Collector) WithHost(h *HostCollector) *Collector {
	c.host = h
	return c
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
	return finite(reqRate), finite(errRate), finite(p50), finite(p95)
}

// finite clamps non-finite floats to 0 so bad upstream data (e.g. an
// inconsistent +Inf histogram bucket) can never be persisted or published.
func finite(x float64) float64 {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return 0
	}
	return x
}

// CollectOnce samples every app, writes rows, and publishes live samples.
func (c *Collector) CollectOnce(ctx context.Context) error {
	if !c.runMu.TryLock() {
		return nil // a previous tick is still running; skip this one
	}
	defer c.runMu.Unlock()

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

	runningApps := 0
	for _, a := range apps {
		appID := uuidString(a.ID)
		rs, running, err := c.stats.AppStats(ctx, appID)
		if err != nil || !running {
			continue // not running or transient docker error — skip this tick
		}
		runningApps++
		svc := "app-" + a.Slug + "@docker"
		var reqRate, errRate, p50, p95 float64
		if prev, ok := prevAll[svc]; ok {
			reqRate, errRate, p50, p95 = c.sampleTraffic(prev, traffic[svc], dt)
		}

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

	if c.host != nil {
		hs := c.host.Sample(ctx)
		hs.RunningApps = runningApps
		if err := q.CreateHostMetric(ctx, sqlc.CreateHostMetricParams{
			CpuPct:       hs.CPUPercent,
			MemUsedBytes: hs.MemUsedBytes, MemTotalBytes: hs.MemTotalBytes,
			DiskFreeBytes: hs.DiskFreeBytes, DiskTotalBytes: hs.DiskTotalBytes,
			Containers: int32(hs.Containers), RunningApps: int32(hs.RunningApps),
		}); err != nil {
			return err
		}
		if b, err := json.Marshal(hs); err == nil {
			c.hub.Publish(HostTopic, b)
		}
	}
	return nil
}

func uuidString(id pgtype.UUID) string {
	v, _ := id.Value()
	s, _ := v.(string)
	return s
}
