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

// errNotAdmin ends an instance-wide stream whose viewer has lost the role that
// opened it.
var errNotAdmin = errors.New("instance admin required")

func hostMetricJSON(m sqlc.HostMetric) map[string]any {
	return map[string]any{
		"ts":               m.CreatedAt.Time,
		"cpu_pct":          m.CpuPct,
		"mem_used_bytes":   m.MemUsedBytes,
		"mem_total_bytes":  m.MemTotalBytes,
		"disk_free_bytes":  m.DiskFreeBytes,
		"disk_total_bytes": m.DiskTotalBytes,
		"containers":       m.Containers,
		"running_apps":     m.RunningApps,
	}
}

// handleHostMetrics returns whole-server history within the requested window.
func (s *Server) handleHostMetrics(w http.ResponseWriter, r *http.Request) {
	rows, err := s.metrics.ListHostSince(r.Context(), time.Now().Add(-windowFrom(r)))
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, hostMetricJSON(m))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHostMetricsStream streams live whole-server samples over SSE.
func (s *Server) handleHostMetricsStream(w http.ResponseWriter, r *http.Request) {
	ctx, flusher, cleanup, ok := s.beginStream(w, r, func(ctx context.Context) error {
		u, err := s.revalidate(ctx, r)
		if err != nil {
			return err
		}
		// This stream crosses every org boundary, so losing instance-admin has
		// to end it just as surely as losing the session does.
		if !u.IsInstanceAdmin {
			return errNotAdmin
		}
		return nil
	})
	if !ok {
		return
	}
	defer cleanup()

	ch, cancel := s.hub.Subscribe(appmetrics.HostTopic)
	defer cancel()

	startSSE(w)
	send := func(b []byte) { _, _ = fmt.Fprintf(w, "data: %s\n\n", b); flusher.Flush() }

	// Replay the latest stored sample so the charts render immediately rather
	// than staying blank until the next 15s tick.
	if m, err := s.metrics.LatestHost(ctx); err == nil {
		if b, err := json.Marshal(hostMetricJSON(m)); err == nil {
			send(b)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		_ = err // non-fatal; continue to the live stream
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

// handleAllAppMetrics returns the newest sample for every app on the instance.
// Instance-admin only: it deliberately crosses org boundaries, which no
// org-scoped endpoint may do (FR-2.4).
func (s *Server) handleAllAppMetrics(w http.ResponseWriter, r *http.Request) {
	rows, err := s.metrics.LatestPerApp(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, map[string]any{
			"app_id":          m.AppID,
			"org_id":          m.OrgID,
			"name":            m.Name,
			"slug":            m.Slug,
			"desired_state":   m.DesiredState,
			"ts":              m.CreatedAt.Time,
			"cpu_pct":         m.CpuPct,
			"mem_bytes":       m.MemBytes,
			"mem_limit_bytes": m.MemLimitBytes,
			"req_rate":        m.ReqRate,
			"err_rate":        m.ErrRate,
			"p95_ms":          m.P95Ms,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
