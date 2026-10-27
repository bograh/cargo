package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/deployments"
	"github.com/bograh/cargo/internal/events"
	"github.com/bograh/cargo/internal/hosts"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// retiringProvider records Retire calls by the daemon they were bound to.
type retiringProvider struct {
	recordingProvider
}

func (p retiringProvider) Retire(_ context.Context, slug string, _ io.Writer) error {
	*p.calls = append(*p.calls, "retire:"+slug+"@"+p.where())
	return nil
}

func (p retiringProvider) ForTarget(t reconciler.Target) reconciler.DeployProvider {
	tt := t
	return retiringProvider{recordingProvider{target: &tt, calls: p.calls}}
}

// moveHostsAdmin materializes any host ID as a target named after it.
type moveHostsAdmin struct{ stubHostsAdmin }

func (*moveHostsAdmin) ResolveHost(_ context.Context, hostID string) (*reconciler.Target, error) {
	return &reconciler.Target{HostID: hostID}, nil
}

func (*moveHostsAdmin) Resolve(context.Context, string) (*reconciler.Target, string, error) {
	return nil, "", nil
}

type moveFixture struct {
	s     *Server
	q     *sqlc.Queries
	app   sqlc.Application
	user  sqlc.User
	host  string
	host2 string
	calls *[]string
	enq   *recordingEnqueuer
}

func setupMove(t *testing.T) *moveFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("needs docker")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	box, err := crypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := auth.NewService(pool).Register(ctx, "o@x.co", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	org, err := orgs.NewService(pool).Create(ctx, "Acme", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := apps.NewService(pool, box).Create(ctx, org.ID, u.ID,
		apps.CreateInput{Name: "web", SourceType: "image", ImageRef: "nginx:alpine", ExposedPort: 80})
	if err != nil {
		t.Fatal(err)
	}
	var host, host2 string
	for i, dst := range []*string{&host, &host2} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO hosts (name, address, port, status) VALUES ($1, 'deploy@10.0.0.5', 22, 'online')
			 RETURNING id::text`, []string{"w1", "w2"}[i]).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}

	s := NewServer(config.Config{Env: "development"}, pool, box)
	s.auth = stubAuth{user: u}
	calls := &[]string{}
	enq := &recordingEnqueuer{}
	s.WireDeployments(deployments.NewService(pool, events.NewHub(), t.TempDir()), enq, events.NewHub(),
		retiringProvider{recordingProvider{calls: calls}})
	s.WireHosts(&moveHostsAdmin{}, hosts.NewService(pool, box))
	return &moveFixture{s: s, q: sqlc.New(pool), app: app, user: u, host: host, host2: host2, calls: calls, enq: enq}
}

// markLive gives the app a live deployment, as a finished deploy would.
func (f *moveFixture) markLive(t *testing.T) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := f.s.pool.QueryRow(context.Background(),
		`INSERT INTO deployments (app_id, trigger, status, image_tag) VALUES ($1, 'manual', 'live', 'nginx:alpine')
		 RETURNING id`, f.app.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *moveFixture) move(t *testing.T, hostID string) {
	t.Helper()
	rec := doAuthed(t, f.s, http.MethodPatch, "/api/v1/apps/"+uuidString(f.app.ID), `{"host_id":"`+hostID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move to %q: status %d body %s", hostID, rec.Code, rec.Body)
	}
}

func (f *moveFixture) pending(t *testing.T) []string {
	t.Helper()
	got, err := f.q.ListAppRetirements(context.Background(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A serving app is redeployed onto its new host — re-applying the live image,
// not rebuilding — and the old location is only recorded, not torn down: the
// pipeline does that once the new host is live.
func TestMoveLiveAppRedeploysAndRecordsPreviousHost(t *testing.T) {
	f := setupMove(t)
	live := f.markLive(t)
	f.move(t, f.host)

	if len(f.enq.ids) != 1 {
		t.Fatalf("enqueued %v, want one redeploy", f.enq.ids)
	}
	dep, err := f.q.GetDeployment(context.Background(), pgUUID(f.enq.ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	if dep.Trigger != "rollback" || dep.ImageTag != "nginx:alpine" {
		t.Fatalf("redeploy trigger=%s image=%s, want a rollback to the live image (live %s)",
			dep.Trigger, dep.ImageTag, uuidString(live))
	}
	if got := f.pending(t); !slices.Equal(got, []string{apps.ControlPlaneTarget}) {
		t.Fatalf("pending retirements = %v, want [control-plane]", got)
	}
	if len(*f.calls) != 0 {
		t.Fatalf("old location torn down before the new one is live: %v", *f.calls)
	}
}

// Nothing will deploy a never-deployed app, so its old location is cleared
// in the request itself.
func TestMoveUndeployedAppRetiresImmediately(t *testing.T) {
	f := setupMove(t)
	f.move(t, f.host)

	if len(f.enq.ids) != 0 {
		t.Fatalf("enqueued %v for an app that was never deployed", f.enq.ids)
	}
	if want := []string{"retire:" + f.app.Slug + "@local"}; !slices.Equal(*f.calls, want) {
		t.Fatalf("calls = %v, want %v", *f.calls, want)
	}
	if got := f.pending(t); len(got) != 0 {
		t.Fatalf("pending retirements = %v after a successful retire", got)
	}
}

// A stopped app stays stopped: no redeploy, and the old location is cleared now.
func TestMoveStoppedAppDoesNotStartIt(t *testing.T) {
	f := setupMove(t)
	f.markLive(t)
	if _, err := f.s.pool.Exec(context.Background(),
		`UPDATE applications SET desired_state = 'stopped' WHERE id = $1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	f.move(t, f.host)

	if len(f.enq.ids) != 0 {
		t.Fatalf("moving a stopped app queued %v", f.enq.ids)
	}
	if want := []string{"retire:" + f.app.Slug + "@local"}; !slices.Equal(*f.calls, want) {
		t.Fatalf("calls = %v, want %v", *f.calls, want)
	}
}

// Moving back to a location still waiting to be cleared cancels that: the app
// is about to run there again. The location it is leaving takes its place.
func TestMoveBackCancelsPendingRetirement(t *testing.T) {
	f := setupMove(t)
	f.markLive(t)
	f.move(t, f.host)
	f.move(t, "") // back to the control plane before the first redeploy ran

	if got := f.pending(t); !slices.Equal(got, []string{f.host}) {
		t.Fatalf("pending retirements = %v, want only the worker host", got)
	}
}

// Moving twice before a deploy lands leaves containers possibly on both old
// locations; both are remembered.
func TestMoveTwiceRemembersBothPreviousTargets(t *testing.T) {
	f := setupMove(t)
	f.markLive(t)
	f.move(t, f.host)
	f.move(t, f.host2)

	got := f.pending(t)
	slices.Sort(got)
	want := []string{apps.ControlPlaneTarget, f.host}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("pending retirements = %v, want %v", got, want)
	}
}

// Deleting an app also clears the locations it was moved away from.
func TestDeleteAppRetiresPreviousTargets(t *testing.T) {
	f := setupMove(t)
	f.markLive(t)
	f.move(t, f.host)
	*f.calls = nil

	rec := doAuthed(t, f.s, http.MethodDelete, "/api/v1/apps/"+uuidString(f.app.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d body %s", rec.Code, rec.Body)
	}
	if !slices.Contains(*f.calls, "retire:"+f.app.Slug+"@local") {
		t.Fatalf("calls = %v, want the control plane retired", *f.calls)
	}
}
