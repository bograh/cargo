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
	ctx, flusher, cleanup, ok := s.beginStream(w, r, func(ctx context.Context) error {
		u, err := s.revalidate(ctx, r)
		if err != nil {
			return err
		}
		_, err = s.apps.Get(ctx, id, u.ID)
		return err
	})
	if !ok {
		return
	}
	defer cleanup()

	appIDStr := uuidString(id)
	ch, cancel := s.hub.Subscribe(appmetrics.MetricsTopic(appIDStr))
	defer cancel()

	startSSE(w)
	send := func(b []byte) { _, _ = fmt.Fprintf(w, "data: %s\n\n", b); flusher.Flush() }

	// Replay the latest stored sample so the chart shows data immediately.
	if m, err := s.metrics.Latest(ctx, id); err == nil {
		if b, err := json.Marshal(metricJSON(m)); err == nil {
			send(b)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		// non-fatal; continue to live stream
		_ = err
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			send(msg)
		}
	}
}
