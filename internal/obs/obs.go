// Package obs exposes control-plane self-metrics (Prometheus). It is a leaf
// package so both internal/jobs (which records deploy outcomes) and cmd/server
// (which serves the endpoint) can depend on it without an import cycle. Metrics
// are served on a separate internal-only listener, never the public API port.
package obs

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var registry = prometheus.NewRegistry()

var (
	deploysTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cargo_deploys_total",
		Help: "Deployments by terminal result.",
	}, []string{"result"})
	deployDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cargo_deploy_duration_seconds",
		Help:    "Deployment wall-clock duration in seconds.",
		Buckets: prometheus.ExponentialBuckets(2, 2, 9), // 2s … ~512s
	})
)

func init() {
	registry.MustRegister(deploysTotal, deployDuration)
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// RecordDeploy records a terminal deployment outcome ("live" or "failed").
func RecordDeploy(result string, d time.Duration) {
	deploysTotal.WithLabelValues(result).Inc()
	if d > 0 {
		deployDuration.Observe(d.Seconds())
	}
}

// RegisterDB adds pgx-pool and River job-queue collectors. Call once at startup.
func RegisterDB(pool *pgxpool.Pool) {
	registry.MustRegister(&dbCollector{pool: pool})
}

// Handler serves the metrics in Prometheus text format.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

var (
	descPoolTotal    = prometheus.NewDesc("cargo_db_pool_total_conns", "Total pgx pool connections.", nil, nil)
	descPoolIdle     = prometheus.NewDesc("cargo_db_pool_idle_conns", "Idle pgx pool connections.", nil, nil)
	descPoolAcquired = prometheus.NewDesc("cargo_db_pool_acquired_conns", "In-use pgx pool connections.", nil, nil)
	descRiverJobs    = prometheus.NewDesc("cargo_river_jobs", "River queue jobs by state.", []string{"state"}, nil)
)

// dbCollector pulls live pool stats and River queue depth on each scrape.
type dbCollector struct{ pool *pgxpool.Pool }

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descPoolTotal
	ch <- descPoolIdle
	ch <- descPoolAcquired
	ch <- descRiverJobs
}

func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(descPoolTotal, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(descPoolIdle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(descPoolAcquired, prometheus.GaugeValue, float64(s.AcquiredConns()))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := c.pool.Query(ctx, "SELECT state, count(*) FROM river_job GROUP BY state")
	if err != nil {
		return // River table may not exist yet; skip queue metrics this scrape.
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return
		}
		ch <- prometheus.MustNewConstMetric(descRiverJobs, prometheus.GaugeValue, float64(n), state)
	}
}
