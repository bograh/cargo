package reconciler

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyRunsContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	spec := Spec{
		AppID: "test-1", Slug: "recon-test", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Env: map[string]string{"FOO": "bar"},
		Domains: []string{"recon-test.apps.localhost"},
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("apply: %v\n%s", err, log.String())
	}
	envPath := filepath.Join(dataDir, "apps", "test-1", ".env")
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "FOO=bar") {
		t.Fatalf("env content = %q", data)
	}

	if err := d.Teardown(ctx, spec.AppID, spec.Slug, &log); err != nil {
		t.Fatalf("teardown: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "apps", "test-1")); !os.IsNotExist(err) {
		t.Fatalf("project dir not removed: %v", err)
	}
}
