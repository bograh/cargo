package api

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
)

// recordingProvider records which daemon (local or a bound target) each
// lifecycle call went to.
type recordingProvider struct {
	target *reconciler.Target
	calls  *[]string
}

func (p recordingProvider) where() string {
	if p.target == nil {
		return "local"
	}
	return p.target.HostID
}
func (p recordingProvider) Apply(context.Context, reconciler.Spec, io.Writer) error { return nil }
func (p recordingProvider) Teardown(_ context.Context, _, _ string, _ io.Writer) error {
	*p.calls = append(*p.calls, "teardown@"+p.where())
	return nil
}
func (p recordingProvider) Stop(context.Context, string, io.Writer) error {
	*p.calls = append(*p.calls, "stop@"+p.where())
	return nil
}
func (p recordingProvider) Start(context.Context, string, io.Writer) error {
	*p.calls = append(*p.calls, "start@"+p.where())
	return nil
}
func (p recordingProvider) ForTarget(t reconciler.Target) reconciler.DeployProvider {
	tt := t
	return recordingProvider{target: &tt, calls: p.calls}
}

// resolvingHostsAdmin adds Resolve to the hosts-admin stub, checking it is
// asked about the app.
type resolvingHostsAdmin struct {
	stubHostsAdmin
	t       *testing.T
	wantApp string
}

func (r *resolvingHostsAdmin) Resolve(_ context.Context, appID string) (*reconciler.Target, string, error) {
	if appID != r.wantApp {
		r.t.Errorf("Resolve(%q), want app %q", appID, r.wantApp)
	}
	return &reconciler.Target{HostID: "worker-1"}, "online", nil
}

type listingApps struct {
	stubApps
	list []sqlc.Application
}

func (l listingApps) List(context.Context, pgtype.UUID, pgtype.UUID) ([]sqlc.Application, error) {
	return l.list, nil
}

const hostedAppID = "11111111-0000-0000-0000-000000000001"

func hostedApp() sqlc.Application {
	return sqlc.Application{ID: pgUUID(hostedAppID), Slug: "web", Name: "web",
		HostID: pgUUID("22222222-0000-0000-0000-000000000002")}
}

func hostRoutingServer(t *testing.T, a AppService) (*Server, *[]string) {
	calls := &[]string{}
	s := appServer(a)
	s.provider = recordingProvider{calls: calls}
	s.hostsAdmin = &resolvingHostsAdmin{t: t, wantApp: hostedAppID}
	return s, calls
}

// Stop, Start and Delete on a hosted app must act on the worker's daemon; on
// the local one they "succeeded" while the worker kept serving.
func TestHostedAppLifecycleRoutesToWorker(t *testing.T) {
	for _, tc := range []struct{ method, path, want string }{
		{http.MethodPost, "/api/v1/apps/" + hostedAppID + "/stop", "stop@worker-1"},
		{http.MethodPost, "/api/v1/apps/" + hostedAppID + "/start", "start@worker-1"},
		{http.MethodDelete, "/api/v1/apps/" + hostedAppID, "teardown@worker-1"},
	} {
		s, calls := hostRoutingServer(t, stubApps{app: hostedApp()})
		rec := doAuthed(t, s, tc.method, tc.path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d body %s", tc.method, tc.path, rec.Code, rec.Body)
		}
		if len(*calls) != 1 || (*calls)[0] != tc.want {
			t.Fatalf("%s %s: calls = %v, want [%s]", tc.method, tc.path, *calls, tc.want)
		}
	}
}

func TestControlPlaneAppLifecycleStaysLocal(t *testing.T) {
	app := hostedApp()
	app.HostID = pgtype.UUID{}
	s, calls := hostRoutingServer(t, stubApps{app: app})
	if rec := doAuthed(t, s, http.MethodPost, "/api/v1/apps/"+hostedAppID+"/stop", ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(*calls) != 1 || (*calls)[0] != "stop@local" {
		t.Fatalf("calls = %v", *calls)
	}
}

// Deleting an org must tear down its apps' containers (on whichever daemon
// runs them), not just cascade the rows away.
func TestDeleteOrgTearsDownApps(t *testing.T) {
	s, calls := hostRoutingServer(t, listingApps{list: []sqlc.Application{hostedApp()}})
	s.orgs = stubOrgs{role: "owner"}
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if len(*calls) != 1 || (*calls)[0] != "teardown@worker-1" {
		t.Fatalf("calls = %v, want one teardown on the worker", *calls)
	}
}

func TestDeleteOrgNonOwnerTouchesNothing(t *testing.T) {
	s, calls := hostRoutingServer(t, listingApps{list: []sqlc.Application{hostedApp()}})
	s.orgs = stubOrgs{role: "admin"}
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatalf("non-owner delete tore down %v", *calls)
	}
}

type countingApps struct {
	stubApps
	created *int
}

func (c countingApps) Create(ctx context.Context, o, a pgtype.UUID, in apps.CreateInput) (sqlc.Application, error) {
	*c.created++
	return c.stubApps.Create(ctx, o, a, in)
}

// A bad host must fail the request before the app row exists; validating it
// afterwards returned 400 but left the app behind for the retry to collide with.
func TestCreateAppWithUnknownHostCreatesNothing(t *testing.T) {
	created := 0
	s := appServer(countingApps{created: &created})
	s.hostSvc = stubHostReader{}
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000/apps",
		`{"name":"web","source_type":"image","image_ref":"nginx","host_id":"33333333-0000-0000-0000-000000000003"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	if created != 0 {
		t.Fatalf("app created %d time(s) despite the bad host", created)
	}
}
