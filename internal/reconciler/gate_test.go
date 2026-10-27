package reconciler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// On a remote target the control plane cannot reach the worker's bridge
// network, so even with a HealthcheckPath configured the gate must pass on
// daemon-side state alone — and must never dial out. The httptest handler
// fails the test if it is ever hit, proving the probe stayed home.
func TestRemoteGateNeverProbesHTTP(t *testing.T) {
	probeHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeHit = true
		w.WriteHeader(500)
	}))
	defer srv.Close()

	ctx := context.Background()
	d := NewDocker(t.TempDir()).ForHost(Target{HostID: "h1", Endpoint: "ssh://u@h:22"})
	spec := Spec{
		AppID: "remote-1", Slug: "remote-test", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Domains: []string{"remote-test.apps.example.com"},
	}
	args := []string{"-f", "/dev/null"}

	// Daemon reports the container running; stability window then passes it.
	start := time.Now()
	d.containerStatusFn = func(ctx context.Context, args []string, service string) (string, string, string) {
		return "cid123", "running", "0"
	}
	var log bytes.Buffer
	if err := d.waitHealthy(ctx, args, "app", spec, &log); err != nil {
		t.Fatalf("gate failed: %v\n%s", err, log.String())
	}
	if elapsed := time.Since(start); elapsed < stableFor {
		t.Fatalf("gate passed before stability window (%s)", elapsed)
	}
	if probeHit {
		t.Fatal("HTTP probe was dialed for a remote target")
	}
}

// A crash loop on a remote target still fails the gate fast.
func TestRemoteGateFailsOnCrashLoop(t *testing.T) {
	ctx := context.Background()
	d := NewDocker(t.TempDir()).ForHost(Target{HostID: "h1"})
	d.containerStatusFn = func(ctx context.Context, args []string, service string) (string, string, string) {
		return "cid123", "running", "3"
	}
	spec := Spec{AppID: "remote-2", Slug: "r2", Image: "nginx:alpine", Port: 80}
	var log bytes.Buffer
	err := d.waitHealthy(ctx, []string{"-f", "/dev/null"}, "app", spec, &log)
	if err == nil || !contains(err.Error(), "crash loop") {
		t.Fatalf("err = %v, want crash-loop failure\n%s", err, log.String())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Redeploying an unchanged spec keeps the existing container, whose lifetime
// restart count can be above zero from long ago. That is not a crash on boot.
func TestGateIgnoresRestartsFromBeforeTheDeploy(t *testing.T) {
	ctx := context.Background()
	d := NewDocker(t.TempDir()).ForHost(Target{HostID: "h1"})
	d.containerStatusFn = func(ctx context.Context, args []string, service string) (string, string, string) {
		return "cid-kept", "running", "2"
	}
	spec := Spec{AppID: "kept", Slug: "kept", Image: "nginx:alpine", Port: 80}
	base := d.gateBaseline(ctx, nil, "app")
	var log bytes.Buffer
	if err := d.waitHealthyFrom(ctx, nil, "app", spec, &log, base); err != nil {
		t.Fatalf("gate failed on a kept container's old restarts: %v", err)
	}
	// ...but a restart *during* the gate still fails it.
	d.containerStatusFn = func(ctx context.Context, args []string, service string) (string, string, string) {
		return "cid-kept", "running", "3"
	}
	if err := d.waitHealthyFrom(ctx, nil, "app", spec, &log, base); err == nil || !contains(err.Error(), "crash loop") {
		t.Fatalf("err = %v, want crash loop", err)
	}
}

type failingColors struct{}

func (failingColors) ActiveColor(context.Context, string) (string, error) {
	return "", errors.New("db down")
}
func (failingColors) SetActiveColor(context.Context, string, string) error {
	return errors.New("db down")
}

// A color-store failure must abort a deploy, never read as "no color" —
// which made blue/green pick blue as next and tear down a live blue.
func TestApplyAbortsWhenColorStoreFails(t *testing.T) {
	d := NewDocker(t.TempDir())
	d.SetColors(failingColors{})
	spec := Spec{AppID: "a", Slug: "a", Image: "nginx:alpine", Port: 80, BlueGreen: true,
		Domains: []string{"a.apps.localhost"}}
	var log bytes.Buffer
	if err := d.Apply(context.Background(), spec, &log); err == nil || !contains(err.Error(), "active color") {
		t.Fatalf("err = %v, want active-color failure", err)
	}
	spec.BlueGreen = false
	if err := d.Apply(context.Background(), spec, &log); err == nil || !contains(err.Error(), "active color") {
		t.Fatalf("recreate err = %v, want active-color failure", err)
	}
}
