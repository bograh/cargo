package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bograh/cargo/internal/reconciler"
)

// recordingRemovers is a HostRemovers that hands back a remover recording
// every ref it is asked to untag, or err for every app.
type recordingRemovers struct {
	removed []string
	asked   []string
	err     error
}

func (r *recordingRemovers) RemoverFor(_ context.Context, appID string) (imageRemover, error) {
	r.asked = append(r.asked, appID)
	if r.err != nil {
		return nil, r.err
	}
	return func(_ context.Context, ref string) error {
		r.removed = append(r.removed, ref)
		return nil
	}, nil
}

// seedFailedOnHost creates n failed deployments with Cargo-built tags that
// ran on hostID, oldest first, and returns their tags.
func seedFailedOnHost(t *testing.T, f *fixture, hostID string, n int) []string {
	t.Helper()
	ctx := context.Background()
	var tags []string
	for i := 0; i < n; i++ {
		dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
		if err != nil {
			t.Fatal(err)
		}
		tag := fmt.Sprintf("app-hosted:v%d", i)
		if err := f.deps.SetBuildInfo(ctx, dep.ID, "", tag); err != nil {
			t.Fatal(err)
		}
		if err := f.deps.SetStatus(ctx, dep.ID, "deploying"); err != nil {
			t.Fatal(err)
		}
		if err := f.deps.Finish(ctx, dep.ID, "failed", "boom"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pipeline.Pool.Exec(ctx,
			`UPDATE deployments SET host_id = $1 WHERE id = $2`, hostID, dep.ID); err != nil {
			t.Fatal(err)
		}
		tags = append(tags, tag)
	}
	return tags
}

// The daily prune must untag old images on the worker host they ran on, not
// just on the control plane, or a worker's disk fills with dead images.
func TestRunPruneRemovesImagesOnWorkerHost(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	hostID := seedHost(t, f)
	tags := seedFailedOnHost(t, f, hostID, keepDeployments+2)

	hosts := &recordingRemovers{}
	if err := RunPrune(ctx, f.pipeline.Pool, f.deps, hosts); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(hosts.asked) != 1 || hosts.asked[0] != uuidString(t, f.app.ID) {
		t.Fatalf("RemoverFor asked about %v, want just app %s", hosts.asked, uuidString(t, f.app.ID))
	}
	want := map[string]bool{tags[0]: true, tags[1]: true}
	if len(hosts.removed) != len(want) {
		t.Fatalf("removed on host %v, want %v", hosts.removed, tags[:2])
	}
	for _, ref := range hosts.removed {
		if !want[ref] {
			t.Fatalf("removed %q on host, which is inside the keep window", ref)
		}
	}
	var left int
	if err := f.pipeline.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM deployments WHERE app_id = $1`, f.app.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != keepDeployments {
		t.Fatalf("%d deployments left, want %d", left, keepDeployments)
	}
}

// An unreachable host must not fail the whole job, and the app's rows must
// survive so a later run can still find and remove the host's images.
func TestRunPruneSkipsUnavailableHost(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	hostID := seedHost(t, f)
	seedFailedOnHost(t, f, hostID, keepDeployments+2)

	hosts := &recordingRemovers{err: fmt.Errorf("%w: w1 is unreachable", errHostUnavailable)}
	if err := RunPrune(ctx, f.pipeline.Pool, f.deps, hosts); err != nil {
		t.Fatalf("prune should skip an unavailable host, got %v", err)
	}
	var left int
	if err := f.pipeline.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM deployments WHERE app_id = $1`, f.app.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != keepDeployments+2 {
		t.Fatalf("%d deployments left, want all %d kept for the next run", left, keepDeployments+2)
	}
}

// removingProvider is a host-capable provider whose bound view records
// RemoveImage calls and the target it was bound to.
type removingProvider struct {
	fakeProvider
	bound   []reconciler.Target
	removed []string
}

func (p *removingProvider) ForTarget(t reconciler.Target) reconciler.DeployProvider {
	p.bound = append(p.bound, t)
	return p
}

func (p *removingProvider) RemoveImage(_ context.Context, ref string) error {
	p.removed = append(p.removed, ref)
	return nil
}

func TestPipelineRemoverFor(t *testing.T) {
	ctx := context.Background()
	target := &reconciler.Target{HostID: "h1"}

	t.Run("control plane app", func(t *testing.T) {
		p := &Pipeline{Hosts: stubRouter{}, Provider: &removingProvider{}}
		rm, err := p.RemoverFor(ctx, "app-1")
		if err != nil || rm != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", rm != nil, err)
		}
	})
	t.Run("no router", func(t *testing.T) {
		p := &Pipeline{Provider: &removingProvider{}}
		if rm, err := p.RemoverFor(ctx, "app-1"); err != nil || rm != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", rm != nil, err)
		}
	})
	t.Run("online host", func(t *testing.T) {
		prov := &removingProvider{}
		p := &Pipeline{Hosts: stubRouter{target: target, status: "online", wantApp: "app-1"}, Provider: prov}
		rm, err := p.RemoverFor(ctx, "app-1")
		if err != nil || rm == nil {
			t.Fatalf("got (%v, %v), want a remover", rm != nil, err)
		}
		_ = rm(ctx, "app-x:1")
		if len(prov.bound) != 1 || prov.bound[0].HostID != "h1" || len(prov.removed) != 1 {
			t.Fatalf("bound=%v removed=%v", prov.bound, prov.removed)
		}
	})
	t.Run("unreachable host", func(t *testing.T) {
		p := &Pipeline{Hosts: stubRouter{target: target, status: "unreachable"}, Provider: &removingProvider{}}
		if _, err := p.RemoverFor(ctx, "app-1"); !errors.Is(err, errHostUnavailable) {
			t.Fatalf("err = %v, want errHostUnavailable", err)
		}
	})
	t.Run("resolve error", func(t *testing.T) {
		p := &Pipeline{Hosts: stubRouter{err: errors.New("db down")}, Provider: &removingProvider{}}
		if _, err := p.RemoverFor(ctx, "app-1"); !errors.Is(err, errHostUnavailable) {
			t.Fatalf("err = %v, want errHostUnavailable", err)
		}
	})
}
