package settings

import (
	"context"
	"testing"
)

func TestSeedSuffixOnlyWhenUnset(t *testing.T) {
	svc, _ := startService(t)
	ctx := context.Background()

	if wrote, err := svc.SeedSuffix(ctx, ""); err != nil || wrote {
		t.Fatalf("empty seed = %v, %v; want no-op", wrote, err)
	}
	if wrote, err := svc.SeedSuffix(ctx, "apps.example.com"); err != nil || !wrote {
		t.Fatalf("first seed = %v, %v; want written", wrote, err)
	}
	if got, _ := svc.Suffix(ctx); got != "apps.example.com" {
		t.Fatalf("suffix = %q after seed", got)
	}
	// An admin's later choice survives every restart.
	if err := svc.SetSuffix(ctx, "apps.other.com"); err != nil {
		t.Fatal(err)
	}
	if wrote, err := svc.SeedSuffix(ctx, "apps.example.com"); err != nil || wrote {
		t.Fatalf("re-seed = %v, %v; want skipped", wrote, err)
	}
	if got, _ := svc.Suffix(ctx); got != "apps.other.com" {
		t.Fatalf("suffix = %q, seed overrode the admin's setting", got)
	}
}
