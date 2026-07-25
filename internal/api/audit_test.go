package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type auditRec struct {
	action, targetType, targetID string
}

type recordingAudit struct{ recs []auditRec }

func (a *recordingAudit) Record(_ context.Context, _, _ pgtype.UUID, action, tt, tid string, _ map[string]any) {
	a.recs = append(a.recs, auditRec{action, tt, tid})
}
func (a *recordingAudit) ListAll(context.Context, int32) ([]sqlc.ListAuditAllRow, error) {
	return nil, nil
}
func (a *recordingAudit) ListByOrg(context.Context, pgtype.UUID, int32) ([]sqlc.ListAuditByOrgRow, error) {
	return nil, nil
}

func serveThrough(s *Server, status int, method, target string) {
	h := s.auditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, target, nil))
}

func TestAuditMiddlewareRecordsMutations(t *testing.T) {
	a := &recordingAudit{}
	s := &Server{audit: a}

	serveThrough(s, 200, http.MethodPost, "/api/v1/apps/abc123/deploy")
	if len(a.recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(a.recs))
	}
	if a.recs[0].action != "POST" || a.recs[0].targetType != "apps" || a.recs[0].targetID != "abc123" {
		t.Fatalf("unexpected record: %+v", a.recs[0])
	}
}

func TestAuditMiddlewareSkipsReadsAndFailures(t *testing.T) {
	a := &recordingAudit{}
	s := &Server{audit: a}

	serveThrough(s, 200, http.MethodGet, "/api/v1/apps")  // read → not audited
	serveThrough(s, 400, http.MethodPost, "/api/v1/orgs") // failed → not audited
	serveThrough(s, 500, http.MethodDelete, "/api/v1/apps/x")
	if len(a.recs) != 0 {
		t.Fatalf("reads/failures must not be audited, got %+v", a.recs)
	}
}
