package jobs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunDomainCheckStatuses(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()
	for _, host := range []string{"active.example.com", "pending.example.com", "broken.example.com"} {
		if _, err := f.appSvc.AddDomain(ctx, f.app.ID, f.owner, host, ""); err != nil {
			t.Fatal(err)
		}
	}

	lookup := func(_ context.Context, host string) error {
		if host == "broken.example.com" {
			return errors.New("NXDOMAIN")
		}
		return nil
	}
	probe := func(_ context.Context, host string) bool {
		return host == "active.example.com"
	}
	if err := RunDomainCheck(ctx, f.pipeline.Pool, lookup, probe); err != nil {
		t.Fatalf("check: %v", err)
	}

	rows, err := f.appSvc.ListDomains(ctx, f.app.ID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"active.example.com":  "active",
		"pending.example.com": "pending",
		"broken.example.com":  "misconfigured",
	}
	for _, d := range rows {
		if d.Status != want[d.Hostname] {
			t.Fatalf("%s status = %s, want %s", d.Hostname, d.Status, want[d.Hostname])
		}
		if !d.LastCheckedAt.Valid {
			t.Fatalf("%s last_checked_at not set", d.Hostname)
		}
	}
}

// The probe must never reach a non-public address, however the domain
// resolves: a tenant pointing their custom domain at 127.0.0.1 (or the cloud
// metadata service) would otherwise get the control plane to make requests
// from inside its own network.
func TestProbeRefusesNonPublicAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	if probeWith(context.Background(), newProbeClient(publicOnlyControl), host) {
		t.Fatal("probe reported a loopback server as answering")
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback server received %d requests", hits.Load())
	}

	// Sanity: the same client without the guard does reach it, so the
	// refusal above is the guard and not a broken probe.
	if !probeWith(context.Background(), newProbeClient(nil), host) {
		t.Fatal("unguarded probe could not reach the test server")
	}
}

// A redirect already proves the domain answers; following it would let a
// tenant aim the probe at a host of their choosing.
func TestProbeDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	redir := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redir.Close()

	if !probeWith(context.Background(), newProbeClient(nil), strings.TrimPrefix(redir.URL, "http://")) {
		t.Fatal("a 302 should count as answering")
	}
	if followed.Load() {
		t.Fatal("probe followed the redirect")
	}
}

func TestPublicOnlyControl(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:80":          false,
		"10.0.0.5:443":          false,
		"169.254.169.254:80":    false,
		"[::1]:80":              false,
		"[::ffff:127.0.0.1]:80": false,
		"[fd00::1]:80":          false,
		"93.184.216.34:443":     true,
	} {
		err := publicOnlyControl("tcp", addr, nil)
		if (err == nil) != ok {
			t.Errorf("%s: err=%v, want allowed=%v", addr, err, ok)
		}
		if err != nil && !errors.Is(err, errNonPublicAddress) {
			t.Errorf("%s: err %v is not errNonPublicAddress", addr, err)
		}
	}
}
