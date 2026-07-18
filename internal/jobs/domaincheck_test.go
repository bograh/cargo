package jobs

import (
	"context"
	"errors"
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
