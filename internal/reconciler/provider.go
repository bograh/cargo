package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Docker applies specs as per-app compose projects via the docker CLI.
type Docker struct {
	dataDir string
	// HealthTimeout bounds the post-apply health gate (default 2 min).
	HealthTimeout time.Duration
}

func NewDocker(dataDir string) *Docker {
	return &Docker{dataDir: dataDir, HealthTimeout: 2 * time.Minute}
}

func (d *Docker) projectDir(appID string) string {
	return filepath.Join(d.dataDir, "apps", appID)
}

func run(ctx context.Context, log io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func (d *Docker) ensureNetwork(ctx context.Context) {
	// "already exists" is the common case — ignore the error and keep its
	// noisy "network already exists" output out of the user's deploy log.
	_ = run(ctx, io.Discard, "docker", "network", "create", "cargo-proxy")
}

func (d *Docker) Apply(ctx context.Context, spec Spec, log io.Writer) error {
	dir := d.projectDir(spec.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	envContent, err := generateEnvFile(spec.Env)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o600); err != nil {
		return err
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(GenerateCompose(spec)), 0o644); err != nil {
		return err
	}
	d.ensureNetwork(ctx)
	if err := run(ctx, log, "docker", "compose", "-f", composePath, "up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	return d.waitHealthy(ctx, composePath, spec, log)
}

// waitHealthy gates the deploy: the container must stay running and, once an
// IP is known, answer an HTTP probe on the healthcheck path with status <500.
//
// When the probe network is unreachable (dev hosts where container IPs are
// not routable, e.g. Docker Desktop/WSL), the gate degrades to
// "container stays running": in production the controlplane shares the
// cargo-proxy network, so the HTTP gate is real there.
func (d *Docker) waitHealthy(ctx context.Context, composePath string, spec Spec, log io.Writer) error {
	deadline := time.Now().Add(d.HealthTimeout)
	client := &http.Client{Timeout: 3 * time.Second}
	var runningSince time.Time
	unreachableOnly := true
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		cid, err := output(ctx, "docker", "compose", "-f", composePath, "ps", "-q", "app")
		if err != nil || cid == "" {
			time.Sleep(2 * time.Second)
			continue
		}
		state, _ := output(ctx, "docker", "inspect", "-f", "{{.State.Status}}", cid)
		if state == "exited" || state == "dead" {
			return fmt.Errorf("container exited during startup")
		}
		if state != "running" {
			time.Sleep(2 * time.Second)
			continue
		}
		if runningSince.IsZero() {
			runningSince = time.Now()
		}
		ip, _ := output(ctx, "docker", "inspect", "-f",
			`{{json .NetworkSettings.Networks}}`, cid)
		if addr := firstIP(ip); addr != "" {
			url := fmt.Sprintf("http://%s:%d%s", addr, spec.Port, spec.HealthcheckPath)
			resp, err := client.Get(url)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode < 500 {
					_, _ = fmt.Fprintf(log, "healthcheck ok: %s → %d\n", url, resp.StatusCode)
					return nil
				}
				unreachableOnly = false // reachable but unhealthy — keep gating
			} else if errors.Is(err, syscall.ECONNREFUSED) {
				unreachableOnly = false // reachable host, app not listening yet
			}
		}
		// Probe network unreachable but container stable for 15s → accept.
		if unreachableOnly && !runningSince.IsZero() && time.Since(runningSince) > 15*time.Second {
			_, _ = fmt.Fprintln(log, "warning: health probe network unreachable from controlplane; accepting running container")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("healthcheck timed out after %s", d.HealthTimeout)
}

// firstIP extracts the first non-empty IPAddress from docker inspect's
// NetworkSettings.Networks JSON.
func firstIP(networksJSON string) string {
	var networks map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
	if err := json.Unmarshal([]byte(networksJSON), &networks); err != nil {
		return ""
	}
	for _, n := range networks {
		if n.IPAddress != "" {
			return n.IPAddress
		}
	}
	return ""
}

func (d *Docker) Teardown(ctx context.Context, appID, slug string, log io.Writer) error {
	dir := d.projectDir(appID)
	composePath := filepath.Join(dir, "compose.yaml")
	if _, err := os.Stat(composePath); err == nil {
		if err := run(ctx, log, "docker", "compose", "-f", composePath, "down", "--remove-orphans"); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}
