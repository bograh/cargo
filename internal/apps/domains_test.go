package apps

import (
	"context"
	"errors"
	"testing"
)

func TestDomainLifecycle(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatal(err)
	}

	d, err := f.svc.AddDomain(ctx, app.ID, f.owner, "API.Example.com", "apps.example.com")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if d.Hostname != "api.example.com" || d.Status != "pending" {
		t.Fatalf("domain = %+v", d)
	}
	if _, err := f.svc.AddDomain(ctx, app.ID, f.owner, "api.example.com", ""); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("duplicate err = %v", err)
	}

	list, err := f.svc.ListDomains(ctx, app.ID, f.viewer)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	hosts, err := f.svc.CustomDomains(ctx, app.ID)
	if err != nil || len(hosts) != 1 || hosts[0] != "api.example.com" {
		t.Fatalf("custom = %v, %v", hosts, err)
	}
	if err := f.svc.RemoveDomain(ctx, app.ID, f.owner, d.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestDomainValidationAndRoles(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"not a domain", "nohost", "-bad.example.com", "web.apps.example.com"} {
		if _, err := f.svc.AddDomain(ctx, app.ID, f.owner, bad, "apps.example.com"); !errors.Is(err, ErrValidation) {
			t.Fatalf("hostname %q err = %v", bad, err)
		}
	}
	// viewer cannot attach (admin+ required)
	if _, err := f.svc.AddDomain(ctx, app.ID, f.viewer, "ok.example.com", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer add err = %v", err)
	}
	if _, err := f.svc.ListDomains(ctx, app.ID, f.outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list err = %v", err)
	}
}
