package jobs

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/hosts"
	"github.com/bograh/cargo/internal/reconciler"
)

// moveProvider is a host-capable fake that records Retire calls by target,
// together with the deployment status at the moment of the call.
type moveProvider struct {
	fakeProvider
	target    *reconciler.Target
	shared    *moveState
	onApply   func()
	retireErr error
}

type moveState struct {
	mu      sync.Mutex
	retires []string // "<target>:<status of the deployment being run>"
	status  func() string
}

func (m *moveProvider) where() string {
	if m.target == nil {
		return apps.ControlPlaneTarget
	}
	return m.target.HostID
}

func (m *moveProvider) Apply(ctx context.Context, spec reconciler.Spec, log io.Writer) error {
	if m.onApply != nil {
		m.onApply()
	}
	return m.fakeProvider.Apply(ctx, spec, log)
}

func (m *moveProvider) ForTarget(t reconciler.Target) reconciler.DeployProvider {
	tt := t
	return &moveProvider{target: &tt, shared: m.shared, onApply: m.onApply, retireErr: m.retireErr}
}

func (m *moveProvider) Retire(_ context.Context, _ string, _ io.Writer) error {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()
	m.shared.retires = append(m.shared.retires, m.where()+":"+m.shared.status())
	return m.retireErr
}

// moveRouter routes the app to whatever host it currently points at and
// materializes any host by ID.
type moveRouter struct{ f *fixture }

func (r moveRouter) Resolve(ctx context.Context, appID string) (*reconciler.Target, string, error) {
	app, err := r.f.appSvc.GetRaw(ctx, r.f.app.ID)
	if err != nil || !app.HostID.Valid {
		return nil, "", err
	}
	return &reconciler.Target{HostID: apps.TargetKey(app.HostID)}, "online", nil
}

func (moveRouter) ResolveHost(_ context.Context, hostID string) (*reconciler.Target, error) {
	return &reconciler.Target{HostID: hostID}, nil
}

type moveRig struct {
	f     *fixture
	prov  *moveProvider
	state *moveState
	depID string
}

func setupMoveRig(t *testing.T) *moveRig {
	f := setup(t, "image")
	state := &moveState{}
	prov := &moveProvider{shared: state}
	f.pipeline.Provider = prov
	f.pipeline.Hosts = moveRouter{f: f}
	return &moveRig{f: f, prov: prov, state: state}
}

func (r *moveRig) addRetirement(t *testing.T, target string) {
	t.Helper()
	if err := sqlc.New(r.f.pipeline.Pool).AddAppRetirement(context.Background(),
		sqlc.AddAppRetirementParams{AppID: r.f.app.ID, Target: target}); err != nil {
		t.Fatal(err)
	}
}

func (r *moveRig) pending(t *testing.T) []string {
	t.Helper()
	got, err := sqlc.New(r.f.pipeline.Pool).ListAppRetirements(context.Background(), r.f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// deploy runs one deployment to completion and fails the test unless it went live.
func (r *moveRig) deploy(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	dep, err := r.f.deps.Create(ctx, r.f.app.ID, r.f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	r.depID = uuidString(t, dep.ID)
	r.state.status = func() string {
		d, _ := r.f.deps.GetRaw(context.Background(), dep.ID)
		return d.Status
	}
	if err := r.f.pipeline.Run(ctx, r.depID); err != nil {
		data, _ := os.ReadFile(r.f.deps.LogPath(r.depID))
		t.Fatalf("run: %v\nlog=%s", err, data)
	}
	if got, _ := r.f.deps.GetRaw(ctx, dep.ID); got.Status != "live" {
		t.Fatalf("status = %s, want live", got.Status)
	}
}

// The old location is torn down only once the new one is live, and only
// the old location.
func TestDeployRetiresPreviousTargetAfterLive(t *testing.T) {
	r := setupMoveRig(t)
	hostID := seedHost(t, r.f) // the app now points at this worker
	r.addRetirement(t, apps.ControlPlaneTarget)

	r.deploy(t)

	if want := []string{apps.ControlPlaneTarget + ":live"}; !slices.Equal(r.state.retires, want) {
		t.Fatalf("retires = %v, want %v (and never on %s)", r.state.retires, want, hostID)
	}
	if got := r.pending(t); len(got) != 0 {
		t.Fatalf("pending retirements = %v after a successful retire", got)
	}
}

// A failed teardown never fails the deploy; it stays recorded, is audited,
// and the next deploy retries it.
func TestDeployRetireFailureIsRetried(t *testing.T) {
	r := setupMoveRig(t)
	seedHost(t, r.f)
	r.addRetirement(t, apps.ControlPlaneTarget)
	r.prov.retireErr = errors.New("daemon unreachable")

	r.deploy(t)

	if got := r.pending(t); !slices.Equal(got, []string{apps.ControlPlaneTarget}) {
		t.Fatalf("pending retirements = %v, want the failed target kept", got)
	}
	var audited int
	if err := r.f.pipeline.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'app.retire_failed'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("audit entries = %d, want 1", audited)
	}

	r.prov.retireErr = nil
	r.deploy(t)

	if len(r.state.retires) != 2 {
		t.Fatalf("retires = %v, want a retry on the second deploy", r.state.retires)
	}
	if got := r.pending(t); len(got) != 0 {
		t.Fatalf("pending retirements = %v after the retry succeeded", got)
	}
}

// A deploy that went live somewhere the app no longer points at (it was
// moved again mid-deploy) must not retire anything: the deploy queued by that
// later move finishes the job.
func TestDeployMovedMidwayRetiresNothing(t *testing.T) {
	r := setupMoveRig(t)
	seedHost(t, r.f)
	r.addRetirement(t, apps.ControlPlaneTarget)
	var second string
	if err := r.f.pipeline.Pool.QueryRow(context.Background(),
		`INSERT INTO hosts (name, address, port, status) VALUES ('w2','deploy@10.0.0.6',22,'online')
		 RETURNING id::text`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	r.prov.onApply = func() {
		_, _ = r.f.pipeline.Pool.Exec(context.Background(),
			`UPDATE applications SET host_id = $1 WHERE id = $2`, second, r.f.app.ID)
	}

	r.deploy(t)

	if len(r.state.retires) != 0 {
		t.Fatalf("retires = %v, want none from a superseded location", r.state.retires)
	}
	if got := r.pending(t); !slices.Equal(got, []string{apps.ControlPlaneTarget}) {
		t.Fatalf("pending retirements = %v, want them kept", got)
	}
}

// A previous host that has since been deleted has nothing left to remove.
func TestDeployForgetsDeletedPreviousHost(t *testing.T) {
	r := setupMoveRig(t)
	r.f.pipeline.Hosts = goneHostRouter{moveRouter{f: r.f}}
	r.addRetirement(t, "99999999-0000-0000-0000-000000000009")

	r.deploy(t)

	if len(r.state.retires) != 0 {
		t.Fatalf("retires = %v, want none", r.state.retires)
	}
	if got := r.pending(t); len(got) != 0 {
		t.Fatalf("pending retirements = %v, want the vanished host forgotten", got)
	}
}

type goneHostRouter struct{ moveRouter }

func (goneHostRouter) ResolveHost(context.Context, string) (*reconciler.Target, error) {
	return nil, hosts.ErrNotFound
}
