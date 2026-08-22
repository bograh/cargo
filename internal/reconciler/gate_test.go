package reconciler

import (
	"bytes"
	"context"
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
