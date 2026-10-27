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
	org                          pgtype.UUID
	detail                       map[string]any
}

type recordingAudit struct {
	recs []auditRec
	// orgOf maps a resource ID to the org ResolveOrg reports for it.
	orgOf map[string]pgtype.UUID
}

func (a *recordingAudit) Record(_ context.Context, _, org pgtype.UUID, action, tt, tid string, detail map[string]any) {
	a.recs = append(a.recs, auditRec{action, tt, tid, org, detail})
}
func (a *recordingAudit) ResolveOrg(_ context.Context, _ string, id pgtype.UUID) pgtype.UUID {
	return a.orgOf[uuidString(id)]
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

// Mutations under /apps, /deployments and /databases have no {orgID} route
// param; they must still be attributed to their org so the org audit view
// shows them. Deleting an org records no org_id (the row is gone) but keeps
// the ID in detail.
func TestAuditMiddlewareAttributesOrg(t *testing.T) {
	var org pgtype.UUID
	_ = org.Scan("11111111-1111-1111-1111-111111111111")
	appID := "22222222-2222-2222-2222-222222222222"
	a := &recordingAudit{orgOf: map[string]pgtype.UUID{appID: org}}
	s := &Server{audit: a}

	serveThrough(s, 200, http.MethodPost, "/api/v1/apps/"+appID+"/deploy")
	serveThrough(s, 200, http.MethodDelete, "/api/v1/orgs/"+uuidString(org))

	if len(a.recs) != 2 {
		t.Fatalf("recs = %+v", a.recs)
	}
	if a.recs[0].org != org {
		t.Fatalf("app mutation org = %v, want %v", a.recs[0].org, org)
	}
	if a.recs[1].org.Valid || a.recs[1].detail["org_id"] != uuidString(org) {
		t.Fatalf("org delete rec = %+v", a.recs[1])
	}
}
