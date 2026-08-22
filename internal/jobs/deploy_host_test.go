package jobs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/reconciler"
)

// fakeHostProvider wraps fakeProvider with host support, recording the
// target each Apply ran through.
type fakeHostProvider struct {
	fakeProvider
	targets []reconciler.Target
}

func (f *fakeHostProvider) ForHost(t reconciler.Target) *reconciler.Docker {
	f.targets = append(f.targets, t)
	return nil // never used as a real provider in tests
}

// stubRouter is a canned HostRouter.
type stubRouter struct {
	target *reconciler.Target
	status string
	err    error
}

func (s stubRouter) Resolve(_ context.Context, _ string) (*reconciler.Target, string, error) {
	if s.err != nil {
		return nil, "", s.err
	}
	return s.target, s.status, nil
}

func seedHost(t testing.TB, f *fixture) (hostID string) {
	t.Helper()
	ctx := context.Background()
	if err := f.pipeline.Pool.QueryRow(ctx,
		`INSERT INTO hosts (name, address, port, status) VALUES ('w1','deploy@10.0.0.5',22,'online')
		 RETURNING id::text`).Scan(&hostID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pipeline.Pool.Exec(ctx,
		`UPDATE applications SET host_id = $1 WHERE id = $2`, hostID, f.app.ID); err != nil {
		t.Fatal(err)
	}
	return hostID
}

func TestDeployToLocalUnchanged(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	// No Host router wired at all: the classic single-host install.
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if got.Status != "live" || got.HostID.Valid {
		t.Fatalf("status = %s host = %v, want live/NULL", got.Status, got.HostID)
	}
}

func TestPipelineHostedAppRoutesAndRecords(t *testing.T) {
	f := setup(t, "image")
	hostID := seedHost(t, f)
	sentinel := reconciler.Target{HostID: hostID, Endpoint: "ssh://deploy@10.0.0.5:22"}
	fhp := &fakeHostProvider{}
	p := f.pipeline
	p.Provider = fhp
	p.Hosts = stubRouter{target: &sentinel, status: "online"}

	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	_ = logBuf
	if err := p.Run(ctx, uuidString(t, dep.ID)); err != nil {
		data, _ := os.ReadFile(f.deps.LogPath(uuidString(t, dep.ID)))
		t.Fatalf("run: %v\nlog=%s", err, data)
	}
	if len(fhp.targets) != 1 || fhp.targets[0].Endpoint != sentinel.Endpoint {
		t.Fatalf("targets = %+v, want one %q", fhp.targets, sentinel.Endpoint)
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if !got.HostID.Valid || uuidString(t, got.HostID) != hostID {
		t.Fatalf("deployment host = %v, want %s", got.HostID, hostID)
	}
}

func TestPipelineRejectsProviderWithoutHostSupport(t *testing.T) {
	f := setup(t, "image")
	seedHost(t, f)
	sentinel := reconciler.Target{HostID: "h", Endpoint: "ssh://u@h"}
	f.pipeline.Provider = &fakeProvider{} // no ForHost
	f.pipeline.Hosts = stubRouter{target: &sentinel, status: "online"}

	ctx := context.Background()
	dep, _ := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	err := f.pipeline.Run(ctx, uuidString(t, dep.ID))
	if err == nil || !strings.Contains(err.Error(), "worker hosts") {
		t.Fatalf("err = %v, want unsupported-provider failure", err)
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if got.Status != "failed" {
		t.Fatalf("status = %s, want failed", got.Status)
	}
}

func TestDeployFailFastWhenHostUnreachable(t *testing.T) {
	f := setup(t, "image")
	seedHost(t, f)
	f.pipeline.Provider = &fakeProvider{}
	f.pipeline.Hosts = stubRouter{
		target: &reconciler.Target{HostID: "h", Endpoint: "ssh://u@h"},
		status: "unreachable",
	}

	ctx := context.Background()
	dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	runErr := f.pipeline.Run(ctx, uuidString(t, dep.ID))
	if runErr == nil || !strings.Contains(runErr.Error(), "unreachable") {
		t.Fatalf("err = %v, want unreachable failure", runErr)
	}
	got, _ := f.deps.GetRaw(ctx, dep.ID)
	if got.Status != "failed" || !strings.Contains(got.Error, "unreachable") {
		t.Fatalf("deployment = %s / %q, want failed with SSH error", got.Status, got.Error)
	}
	data, _ := os.ReadFile(f.deps.LogPath(uuidString(t, dep.ID)))
	if !strings.Contains(string(data), "failed") {
		t.Fatalf("log missing failure marker: %q", data)
	}
}

func TestDeployFailsWhenResolverErrors(t *testing.T) {
	f := setup(t, "image")
	seedHost(t, f)
	f.pipeline.Provider = &fakeProvider{}
	f.pipeline.Hosts = stubRouter{err: errors.New("sealed key unreadable")}

	ctx := context.Background()
	dep, _ := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err := f.pipeline.Run(ctx, uuidString(t, dep.ID)); err == nil ||
		!strings.Contains(err.Error(), "resolve worker host") {
		t.Fatalf("err = %v, want resolver failure", err)
	}
}

var (
	_ io.Writer = (*bytes.Buffer)(nil)
	_           = sqlc.Deployment{}
)
