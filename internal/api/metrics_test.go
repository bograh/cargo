package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/apps"
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
	s := appServer(stubApps{app: sqlc.Application{}})
	s.WireMetrics(stubMetrics{rows: []sqlc.AppMetric{{CpuPct: 5}}})

	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID+"/metrics?window=6h", "")
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

func TestHandleAppMetricsForbidden(t *testing.T) {
	s := appServer(stubApps{err: apps.ErrForbidden})
	s.WireMetrics(stubMetrics{})

	rec := doAuthed(t, s, http.MethodGet, "/api/v1/apps/"+testUUID+"/metrics", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
