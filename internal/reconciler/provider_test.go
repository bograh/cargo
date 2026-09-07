package reconciler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// An app that has never been deployed has no recorded color, and the paths for
// the empty color must stay on the original single-project layout so an
// upgrade doesn't strand containers deployed by an earlier version.
func TestColorPathsAndState(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	appDir := filepath.Join(dataDir, "apps", "app-1")

	if got := d.activeColor(ctx, "app-1"); got != "" {
		t.Fatalf("activeColor of a fresh app = %q, want \"\"", got)
	}
	if got := d.colorDir("app-1", ""); got != appDir {
		t.Fatalf("legacy colorDir = %q, want %q", got, appDir)
	}
	if got := d.colorDir("app-1", "green"); got != filepath.Join(appDir, "green") {
		t.Fatalf("green colorDir = %q", got)
	}
	if got := d.activeComposePath(ctx, "app-1"); got != filepath.Join(appDir, "compose.yaml") {
		t.Fatalf("legacy activeComposePath = %q", got)
	}

	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := d.setActiveColor(ctx, "app-1", "green"); err != nil {
		t.Fatalf("setActiveColor: %v", err)
	}
	if got := d.activeColor(ctx, "app-1"); got != "green" {
		t.Fatalf("activeColor = %q, want green", got)
	}
	// Logs, stats, stop and start must all follow the color that is serving.
	if got := d.activeComposePath(ctx, "app-1"); got != filepath.Join(appDir, "green", "compose.yaml") {
		t.Fatalf("activeComposePath = %q, want the green project", got)
	}
}

// A corrupt state file must degrade to the legacy layout rather than panicking
// or inventing a color whose project does not exist.
func TestActiveColorIgnoresCorruptState(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	appDir := filepath.Join(dataDir, "apps", "app-1")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := d.activeColor(ctx, "app-1"); got != "" {
		t.Fatalf("activeColor on corrupt state = %q, want \"\"", got)
	}
}

// The headline Phase 11 behaviour: a redeploy of a healthy app hands over to
// the other color and leaves exactly one color running.
func TestApplyBlueGreenSwapsColors(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	spec := Spec{
		AppID: "bg-1", Slug: "bg-test", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Domains: []string{"bg-test.apps.localhost"},
		BlueGreen: true,
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("first apply: %v\n%s", err, log.String())
	}
	if got := d.activeColor(ctx, spec.AppID); got != "blue" {
		t.Fatalf("first deploy color = %q, want blue\n%s", got, log.String())
	}

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("second apply: %v\n%s", err, log.String())
	}
	if got := d.activeColor(ctx, spec.AppID); got != "green" {
		t.Fatalf("second deploy color = %q, want green\n%s", got, log.String())
	}
	// Only one color may survive the hand-off, or the app pays for two
	// containers forever and prune has to guess which one matters.
	if _, err := os.Stat(d.composePath(spec.AppID, "blue")); !os.IsNotExist(err) {
		t.Fatalf("retired blue project still present: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(d.composePath(spec.AppID, "green")); err != nil {
		t.Fatalf("green project missing: %v\n%s", err, log.String())
	}
}

// A new version that never becomes healthy must leave the old one serving and
// clean up after itself — the deployment fails with no manual rollback.
func TestApplyBlueGreenFailureKeepsOldColorLive(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	spec := Spec{
		AppID: "bg-2", Slug: "bg-fail", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Domains: []string{"bg-fail.apps.localhost"},
		BlueGreen: true,
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("first apply: %v\n%s", err, log.String())
	}

	// alpine's default command exits immediately, so the new color can never
	// pass the gate.
	bad := spec
	bad.Image = "alpine:3"
	var badLog bytes.Buffer
	if err := d.Apply(ctx, bad, &badLog); err == nil {
		t.Fatalf("apply of a broken image succeeded\n%s", badLog.String())
	}
	if got := d.activeColor(ctx, spec.AppID); got != "blue" {
		t.Fatalf("active color = %q after a failed deploy, want blue\n%s", got, badLog.String())
	}
	if _, err := os.Stat(d.composePath(spec.AppID, "blue")); err != nil {
		t.Fatalf("blue project gone after a failed deploy: %v", err)
	}
	// The failed color must not be left behind competing for traffic.
	if _, err := os.Stat(d.composePath(spec.AppID, "green")); !os.IsNotExist(err) {
		t.Fatalf("failed green project not cleaned up: %v\n%s", err, badLog.String())
	}
	cid, _ := output(ctx, "docker", "compose", "-f", d.composePath(spec.AppID, "blue"), "ps", "-q", "app")
	if cid == "" {
		t.Fatalf("blue container is not running after a failed deploy\n%s", badLog.String())
	}
}

// The point of Phase 11: during a redeploy the old version must keep answering
// until the new one has taken over. Traefik load-balances whatever containers
// carry the app's service label, so "a container is always serving" is the
// property that makes the hand-off invisible to clients. This polls the running
// container's own port throughout a redeploy and fails on the first refusal.
func TestApplyBlueGreenServesThroughout(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	spec := Spec{
		AppID: "bg-4", Slug: "bg-serve", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Domains: []string{"bg-serve.apps.localhost"},
		BlueGreen: true,
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("first apply: %v\n%s", err, log.String())
	}

	// Poll every app container the way Traefik would, and record any moment
	// when none of them answers.
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()
	var (
		mu      sync.Mutex
		polls   int
		outages int
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		client := &http.Client{Timeout: 2 * time.Second}
		for pollCtx.Err() == nil {
			served := false
			for _, color := range []string{"blue", "green"} {
				composePath := d.composePath(spec.AppID, color)
				if _, err := os.Stat(composePath); err != nil {
					continue
				}
				cid, err := output(pollCtx, "docker", "compose", "-f", composePath, "ps", "-q", "app")
				if err != nil || cid == "" {
					continue
				}
				ipJSON, _ := output(pollCtx, "docker", "inspect", "-f",
					`{{json .NetworkSettings.Networks}}`, cid)
				addr := firstIP(ipJSON)
				if addr == "" {
					continue
				}
				resp, err := client.Get(fmt.Sprintf("http://%s:%d/", addr, spec.Port))
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode < 500 {
						served = true
						break
					}
				}
			}
			// Cancelling pollCtx aborts the docker calls above, which would
			// otherwise read as an outage. Drop that final partial sample.
			if pollCtx.Err() != nil {
				return
			}
			mu.Lock()
			polls++
			if !served {
				outages++
			}
			mu.Unlock()
			// No sleep: each iteration already shells out to docker several
			// times, which paces the loop at a few samples per second.
		}
	}()

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("redeploy: %v\n%s", err, log.String())
	}
	stopPolling()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if polls < 3 {
		t.Fatalf("only %d polls completed — the probe never got going", polls)
	}
	if outages > 0 {
		t.Fatalf("%d/%d polls found no container serving during the hand-off\n%s",
			outages, polls, log.String())
	}
	t.Logf("no outage across %d polls during the blue→green hand-off", polls)
}

// An app deployed before blue/green existed must be migrated onto a color
// cleanly: the legacy project is retired before the first color starts, so the
// two never declare the same Traefik service with conflicting options.
func TestApplyBlueGreenMigratesLegacyProject(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	spec := Spec{
		AppID: "bg-5", Slug: "bg-legacy", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Domains: []string{"bg-legacy.apps.localhost"},
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	// Deploy the way a pre-blue/green Cargo would have.
	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("legacy apply: %v\n%s", err, log.String())
	}
	if _, err := os.Stat(d.composePath(spec.AppID, "")); err != nil {
		t.Fatalf("legacy project missing: %v", err)
	}

	spec.BlueGreen = true
	var bgLog bytes.Buffer
	if err := d.Apply(ctx, spec, &bgLog); err != nil {
		t.Fatalf("blue/green apply: %v\n%s", err, bgLog.String())
	}
	if got := d.activeColor(ctx, spec.AppID); got != "blue" {
		t.Fatalf("active color = %q, want blue\n%s", got, bgLog.String())
	}
	// The legacy project must be gone, not left racing the new color.
	if _, err := os.Stat(d.composePath(spec.AppID, "")); !os.IsNotExist(err) {
		t.Fatalf("legacy project survived the migration: %v\n%s", err, bgLog.String())
	}
	if !strings.Contains(bgLog.String(), "one-time restart") {
		t.Fatalf("migration not reported in the deploy log:\n%s", bgLog.String())
	}
	// And the next deploy is a real hand-off with no restart notice.
	var next bytes.Buffer
	if err := d.Apply(ctx, spec, &next); err != nil {
		t.Fatalf("follow-up apply: %v\n%s", err, next.String())
	}
	if got := d.activeColor(ctx, spec.AppID); got != "green" {
		t.Fatalf("follow-up color = %q, want green", got)
	}
	if strings.Contains(next.String(), "one-time restart") {
		t.Fatal("migration path taken twice")
	}
	if !strings.Contains(next.String(), "alongside the running version") {
		t.Fatalf("follow-up deploy was not a hand-off:\n%s", next.String())
	}
}

// Switching an app from blue/green to recreate must retire the color project,
// otherwise the old container keeps answering alongside the new one.
func TestApplyRecreateRetiresColorProject(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	spec := Spec{
		AppID: "bg-3", Slug: "bg-switch", Image: "nginx:alpine", Port: 80,
		HealthcheckPath: "/", Domains: []string{"bg-switch.apps.localhost"},
		BlueGreen: true,
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("blue/green apply: %v\n%s", err, log.String())
	}
	spec.BlueGreen = false
	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("recreate apply: %v\n%s", err, log.String())
	}
	if got := d.activeColor(ctx, spec.AppID); got != "" {
		t.Fatalf("active color = %q after switching to recreate, want \"\"", got)
	}
	if _, err := os.Stat(d.composePath(spec.AppID, "blue")); !os.IsNotExist(err) {
		t.Fatalf("blue project survived the switch to recreate: %v\n%s", err, log.String())
	}
}

// End-to-end for the compose app source: a real two-service compose file from a
// "repository" is applied together with Cargo's overlay, and the overlay's
// additions land on the running web container without disturbing the other
// service.
func TestApplyComposeSource(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second

	src := filepath.Join(dataDir, "apps", "cs-1", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := `services:
  web:
    image: nginx:alpine
  cache:
    image: redis:7-alpine
`
	if err := os.WriteFile(filepath.Join(src, "docker-compose.yml"), []byte(userFile), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := Spec{
		AppID: "cs-1", Slug: "cs-test", Port: 80, HealthcheckPath: "/",
		Domains:     []string{"cs-test.apps.localhost"},
		Env:         map[string]string{"FROM_CARGO": "yes"},
		ComposeFile: "docker-compose.yml", ComposeService: "web",
		SourceDir:   src,
		MemoryLimit: "256m",
	}
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("apply: %v\n%s", err, log.String())
	}

	args, service, err := d.activeProject(ctx, spec.AppID)
	if err != nil {
		t.Fatalf("activeProject: %v", err)
	}
	if service != "web" {
		t.Fatalf("service = %q, want web", service)
	}

	// Both of the user's services run...
	for _, svc := range []string{"web", "cache"} {
		cid, err := outputCompose(ctx, args, "ps", "-q", svc)
		if err != nil || cid == "" {
			t.Fatalf("service %q not running (err=%v)\n%s", svc, err, log.String())
		}
	}

	// ...and Cargo's overlay reached the web container: Traefik labels, the
	// memory cap, and the app's environment.
	webID, _ := outputCompose(ctx, args, "ps", "-q", "web")
	labels, _ := output(ctx, "docker", "inspect", "-f",
		"{{index .Config.Labels \"traefik.http.routers.app-cs-test.rule\"}}", webID)
	if !strings.Contains(labels, "cs-test.apps.localhost") {
		t.Fatalf("traefik label missing from the web container: %q\n%s", labels, log.String())
	}
	mem, _ := output(ctx, "docker", "inspect", "-f", "{{.HostConfig.Memory}}", webID)
	if mem != "268435456" {
		t.Fatalf("mem_limit = %s, want 268435456 (256m)", mem)
	}
	env, _ := output(ctx, "docker", "inspect", "-f", "{{json .Config.Env}}", webID)
	if !strings.Contains(env, "FROM_CARGO=yes") {
		t.Fatalf("cargo env not injected: %s", env)
	}

	// Every service in the user's file is capped, not just the one Cargo
	// routes to: an uncapped sidecar is an uncapped sidecar, and the host does
	// not care which service in the compose file exhausted its memory.
	cacheID, _ := outputCompose(ctx, args, "ps", "-q", "cache")
	cacheMem, _ := output(ctx, "docker", "inspect", "-f", "{{.HostConfig.Memory}}", cacheID)
	if cacheMem != "268435456" {
		t.Fatalf("cache mem_limit = %s, want 268435456 — every service gets the cap", cacheMem)
	}
	// Routing, though, stays on the one service Cargo was told about: a
	// sidecar must not end up with a Traefik router of its own.
	cacheLabels, _ := output(ctx, "docker", "inspect", "-f",
		"{{index .Config.Labels \"traefik.enable\"}}", cacheID)
	if strings.TrimSpace(cacheLabels) == "true" {
		t.Fatalf("overlay enabled Traefik on a service it does not route to")
	}

	// Teardown removes every service, not just the one Cargo routes to.
	if err := d.Teardown(ctx, spec.AppID, spec.Slug, &log); err != nil {
		t.Fatalf("teardown: %v\n%s", err, log.String())
	}
	remaining, _ := output(ctx, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project=cargo-app-cs-test")
	if strings.TrimSpace(remaining) != "" {
		t.Fatalf("containers survived teardown: %q", remaining)
	}
}
