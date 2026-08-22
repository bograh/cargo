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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bograh/cargo/internal/compose"
)

// Docker applies specs as per-app compose projects via the docker CLI.
// A nil target means the control plane's own daemon; a non-nil target (set
// via ForHost) redirects every invocation to a worker host through its
// materialized docker context.
type Docker struct {
	dataDir string
	target  *Target
	colors  ColorStore // nil = legacy state.json on disk
	// containerStatusFn overrides daemon polling in tests; nil in production.
	containerStatusFn func(ctx context.Context, args []string, service string) (cid, status, restarts string)
	// HealthTimeout bounds the post-apply health gate (default 2 min).
	HealthTimeout time.Duration
}

func NewDocker(dataDir string) *Docker {
	return &Docker{dataDir: dataDir, HealthTimeout: 2 * time.Minute}
}

// ForHost returns a view of this provider bound to one worker host. The
// receiver is left untouched; data dir and timeouts are shared.
func (d *Docker) ForHost(t Target) *Docker {
	tt := t
	return &Docker{dataDir: d.dataDir, target: &tt, HealthTimeout: d.HealthTimeout}
}

// ForTarget is the seam-friendly form of ForHost: consumers (the deploy
// pipeline, log streaming) hold DeployProvider values and fakes can
// implement this without constructing a real Docker.
func (d *Docker) ForTarget(t Target) DeployProvider { return d.ForHost(t) }

// buildCmd is the single point where target routing happens: every docker
// exec in this package flows through it. Local invocations inherit the
// process env exactly as they always did; host invocations carry the
// per-host DOCKER_CONFIG / HOME / DOCKER_CONTEXT on top of it.
func (d *Docker) buildCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if d.target != nil {
		cmd.Env = append(os.Environ(), d.target.Env...)
	}
	return cmd
}

func (d *Docker) run(ctx context.Context, log io.Writer, name string, args ...string) error {
	cmd := d.buildCmd(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (d *Docker) projectDir(appID string) string {
	return filepath.Join(d.dataDir, "apps", appID)
}

// colorDir is the compose project directory for one deployment color. The
// empty color is the original single-project layout (recreate strategy, and
// every app deployed before blue/green existed), which stays in place so
// upgrades don't strand running containers.
func (d *Docker) colorDir(appID, color string) string {
	if color == "" {
		return d.projectDir(appID)
	}
	return filepath.Join(d.projectDir(appID), color)
}

func (d *Docker) composePath(appID, color string) string {
	return filepath.Join(d.colorDir(appID, color), "compose.yaml")
}

// appState records which color currently serves an app. It lives next to the
// compose projects rather than in Postgres because it describes what is
func (d *Docker) statePath(appID string) string {
	return filepath.Join(d.projectDir(appID), "state.json")
}

// activeComposePath is the compose file of the color currently serving the
// app — the one Stop/Start/logs/stats should address.
func (d *Docker) activeComposePath(ctx context.Context, appID string) string {
	return d.composePath(appID, d.activeColor(ctx, appID))
}

// projectRef records how to address a running project after the fact. Cargo's
// own single-service apps are fully described by their compose path, but a
// compose-source app needs both files and the name of its web service — and
// Stop/Start/logs/stats only ever receive an app ID.
type projectRef struct {
	Files   []string `json:"files"`
	Service string   `json:"service"`
}

func (d *Docker) writeProjectRef(appID, color string, ref projectRef) error {
	raw, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.colorDir(appID, color), "project.json"), raw, 0o644)
}

// activeProject returns the compose `-f` arguments and web-service name for
// whichever color is currently serving. Apps deployed before compose sources
// existed have no project.json, so it falls back to the single-file layout.
func (d *Docker) activeProject(ctx context.Context, appID string) ([]string, string, error) {
	dir := d.colorDir(appID, d.activeColor(ctx, appID))
	if raw, err := os.ReadFile(filepath.Join(dir, "project.json")); err == nil {
		var ref projectRef
		if err := json.Unmarshal(raw, &ref); err == nil && len(ref.Files) > 0 {
			args := make([]string, 0, len(ref.Files)*2)
			for _, f := range ref.Files {
				args = append(args, "-f", f)
			}
			svc := ref.Service
			if svc == "" {
				svc = "app"
			}
			return args, svc, nil
		}
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if _, err := os.Stat(composePath); err != nil {
		return nil, "", err
	}
	return []string{"-f", composePath}, "app", nil
}

// writeProject renders a color's compose project (env file + compose file) to
// disk and returns the path of the compose file.
func (d *Docker) writeProject(spec Spec) (string, error) {
	dir := d.colorDir(spec.AppID, spec.Color)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	envContent, err := generateEnvFile(spec.Env)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o600); err != nil {
		return "", err
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if spec.ComposeFile != "" {
		// The user's checkout is the project directory, so their env_file and
		// relative bind mounts resolve against their own repository.
		if err := os.WriteFile(filepath.Join(spec.SourceDir, ".env"), []byte(envContent), 0o600); err != nil {
			return "", err
		}
		overlay := compose.GenerateOverlay(compose.OverlaySpec{
			Slug: spec.Slug, Service: spec.ComposeService, Port: spec.Port,
			Domains:     spec.Domains,
			MemoryLimit: spec.MemoryLimit, CPULimit: spec.CPULimit, PidsLimit: spec.PidsLimit,
		})
		if err := os.WriteFile(composePath, []byte(overlay), 0o644); err != nil {
			return "", err
		}
	} else if err := os.WriteFile(composePath, []byte(GenerateCompose(spec)), 0o644); err != nil {
		return "", err
	}
	if err := d.writeProjectRef(spec.AppID, spec.Color, projectRef{
		Files: fileList(spec, composePath), Service: serviceName(spec),
	}); err != nil {
		return "", err
	}
	return composePath, nil
}

// fileList is the ordered set of compose files for a project.
func fileList(spec Spec, composePath string) []string {
	if spec.ComposeFile == "" {
		return []string{composePath}
	}
	return []string{filepath.Join(spec.SourceDir, spec.ComposeFile), composePath}
}

// composeArgs builds the `-f` arguments for a project. A compose-source app is
// applied as the user's own file plus Cargo's overlay: compose merges the two,
// which lets Cargo add networking, labels, and limits without ever rewriting
// YAML it does not fully control.
//
// Ordering matters twice over: the overlay must come second so its values win,
// and compose takes the project directory from the *first* file — so the user's
// file leads and their relative paths resolve inside their own repository.
func (d *Docker) composeArgs(spec Spec, composePath string) []string {
	files := fileList(spec, composePath)
	args := make([]string, 0, len(files)*2)
	for _, f := range files {
		args = append(args, "-f", f)
	}
	return args
}

// serviceName is the compose service the health gate, logs, and stats address.
// Cargo's own file always calls it "app"; a compose source names its own.
func serviceName(spec Spec) string {
	if spec.ComposeService != "" {
		return spec.ComposeService
	}
	return "app"
}

// downColor tears down one color's compose project and removes its directory.
// Best-effort: a color that was never brought up has no compose file.
func (d *Docker) downColor(ctx context.Context, appID, color string, log io.Writer) {
	composePath := d.composePath(appID, color)
	if _, err := os.Stat(composePath); err == nil {
		// A compose-source project must be torn down with the user's file too;
		// the overlay alone does not describe their services.
		args := []string{"-f", composePath}
		if raw, err := os.ReadFile(filepath.Join(d.colorDir(appID, color), "project.json")); err == nil {
			var ref projectRef
			if err := json.Unmarshal(raw, &ref); err == nil && len(ref.Files) > 0 {
				args = args[:0]
				for _, f := range ref.Files {
					args = append(args, "-f", f)
				}
			}
		}
		_ = d.runCompose(ctx, log, args, "down", "--remove-orphans")
	}
	if color != "" {
		_ = os.RemoveAll(d.colorDir(appID, color))
		return
	}
	// The legacy layout shares its directory with state.json and the color
	// subdirectories, so only its own files may be removed.
	_ = os.Remove(composePath)
	_ = os.Remove(filepath.Join(d.colorDir(appID, ""), ".env"))
}

// outputCompose invokes `docker compose` with a project's `-f` arguments
// followed by the subcommand. A compose-source app carries two files, so the
// file set can no longer be a single fixed flag pair.
func outputCompose(ctx context.Context, files []string, args ...string) (string, error) {
	full := append([]string{"compose"}, files...)
	return output(ctx, "docker", append(full, args...)...)
}

func output(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// Target-aware twins of the package helpers above. Production code paths
// (apply, teardown, stop/start, logs/stats) use these so a host-bound
// provider reaches the right daemon; the package functions stay for tests
// that drive the local docker directly.

func (d *Docker) output(ctx context.Context, name string, args ...string) (string, error) {
	out, err := d.buildCmd(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func (d *Docker) runCompose(ctx context.Context, log io.Writer, files []string, args ...string) error {
	full := append([]string{"compose"}, files...)
	return d.run(ctx, log, "docker", append(full, args...)...)
}

func (d *Docker) outputCompose(ctx context.Context, files []string, args ...string) (string, error) {
	full := append([]string{"compose"}, files...)
	return d.output(ctx, "docker", append(full, args...)...)
}

func (d *Docker) ensureNetwork(ctx context.Context) {
	// "already exists" is the common case — ignore the error and keep its
	// noisy "network already exists" output out of the user's deploy log.
	_ = d.run(ctx, io.Discard, "docker", "network", "create", "cargo-proxy")
}

func (d *Docker) Apply(ctx context.Context, spec Spec, log io.Writer) error {
	// The safety gate for a user-supplied compose file sits here, ahead of the
	// strategy split, so no future branch can route around it. It needs the
	// env file on disk (compose interpolates from it), which writeProject also
	// writes — so write it once up front for the compose case.
	if spec.ComposeFile != "" {
		if err := d.writeComposeEnv(spec); err != nil {
			return err
		}
		if err := d.validateComposeSource(ctx, spec); err != nil {
			return err
		}
	}
	if spec.BlueGreen {
		return d.applyBlueGreen(ctx, spec, log)
	}
	return d.applyRecreate(ctx, spec, log)
}

// applyRecreate is the original strategy: one compose project per app,
// replaced in place. The container is down for the length of the swap.
func (d *Docker) applyRecreate(ctx context.Context, spec Spec, log io.Writer) error {
	// An app moving back from blue/green still has a color project running;
	// note it now and reap it once the recreate project is healthy.
	prev := d.activeColor(ctx, spec.AppID)
	spec.Color = ""
	composePath, err := d.writeProject(spec)
	if err != nil {
		return err
	}
	d.ensureNetwork(ctx)
	args := d.composeArgs(spec, composePath)
	if err := d.runCompose(ctx, log, args, "up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	if err := d.waitHealthy(ctx, args, serviceName(spec), spec, log); err != nil {
		return err
	}
	if prev != "" {
		_ = os.Remove(d.statePath(spec.AppID))
		d.downColor(ctx, spec.AppID, prev, log)
	}
	return nil
}

// applyBlueGreen brings the new version up beside the running one, health-gates
// it, and only then reaps the old color (FR-4.7). Both colors declare the same
// Traefik router and service, so once the new color passes its healthcheck
// Traefik load-balances across both and removing the old one is seamless.
//
// A new version that fails its gate is torn down and the old color is left
// untouched and serving — the deployment fails with no manual rollback needed.
func (d *Docker) applyBlueGreen(ctx context.Context, spec Spec, log io.Writer) error {
	prev := d.activeColor(ctx, spec.AppID)
	// A previous deploy may have been interrupted after starting a color but
	// before recording it; clear any stale project for the color we're about
	// to occupy so `up -d` starts from a clean slate.
	spec.Color = nextColor(prev)
	if prev != spec.Color {
		d.downColor(ctx, spec.AppID, spec.Color, io.Discard)
	}

	// An app deployed before blue/green existed still runs the unsuffixed
	// project, whose labels declare the same Traefik service but WITHOUT the
	// load-balancer healthcheck the colored projects now carry. Two containers
	// declaring one service with different options is a conflict Traefik
	// resolves by dropping the service, which would black-hole the app for the
	// whole overlap. Retire the legacy project first: this one transition costs
	// the same brief gap as a recreate, and every deploy after it is seamless.
	if prev == "" {
		if _, err := os.Stat(d.composePath(spec.AppID, "")); err == nil {
			_, _ = fmt.Fprintln(log, "==> migrating to blue/green (one-time restart)")
			d.downColor(ctx, spec.AppID, "", log)
		}
	}

	composePath, err := d.writeProject(spec)
	if err != nil {
		return err
	}
	d.ensureNetwork(ctx)
	if prev != "" {
		_, _ = fmt.Fprintf(log, "==> starting %s alongside the running version\n", spec.Color)
	} else {
		_, _ = fmt.Fprintf(log, "==> starting %s\n", spec.Color)
	}
	args := d.composeArgs(spec, composePath)
	if err := d.runCompose(ctx, log, args, "up", "-d", "--remove-orphans"); err != nil {
		d.downColor(ctx, spec.AppID, spec.Color, io.Discard)
		return err
	}
	if err := d.waitHealthy(ctx, args, serviceName(spec), spec, log); err != nil {
		_, _ = fmt.Fprintf(log, "==> %s failed its healthcheck; keeping the current version live\n", spec.Color)
		// context.WithoutCancel: on a cancelled/timed-out deploy the cleanup
		// still has to run, or the failed color keeps serving traffic beside
		// the good one.
		d.downColor(context.WithoutCancel(ctx), spec.AppID, spec.Color, io.Discard)
		return err
	}

	// Record the new color before reaping the old one: it is already in the
	// Traefik pool and serving, so a crash here must leave it discoverable
	// rather than stranding a container no later deploy knows about.
	if err := d.setActiveColor(ctx, spec.AppID, spec.Color); err != nil {
		return err
	}
	if prev != spec.Color {
		_, _ = fmt.Fprintf(log, "==> %s is healthy\n", spec.Color)
		d.downColor(ctx, spec.AppID, prev, log)
	}
	return nil
}

// stableFor is how long a container must stay running (crash-free) to pass
// the container-status gate when no HTTP healthcheck is configured.
const stableFor = 10 * time.Second

// containerStatus asks the daemon (local or, via the target env, remote)
// for one container's identity and liveness. Split out from waitHealthy so
// tests can drive the gate without a daemon.
func (d *Docker) containerStatus(ctx context.Context, args []string, service string) (cid, status string, restarts string) {
	if d.containerStatusFn != nil {
		return d.containerStatusFn(ctx, args, service)
	}
	cid, err := d.outputCompose(ctx, args, "ps", "-q", service)
	if err != nil || cid == "" {
		return "", "", ""
	}
	status, _ = d.output(ctx, "docker", "inspect", "-f", "{{.State.Status}}", cid)
	restarts, _ = d.output(ctx, "docker", "inspect", "-f", "{{.RestartCount}}", cid)
	return cid, status, restarts
}

// waitHealthy gates the deploy. The container must start and stay running
// (not exit or crash-loop). An HTTP readiness probe is opt-in: when the app
// sets a HealthcheckPath AND the control plane can reach container bridge
// IPs (local targets only — a worker host's bridge network is unreachable
// over SSH), the deploy additionally waits for that path to answer <500.
// Remote targets rely on the daemon-side state plus Traefik's own load-
// balancer healthcheck, which consumes the same compose-declared healthcheck.
// With no path set, a container that stays up for a short window is
// considered live — an HTTP 200 isn't required for every app.
//
// When the probe network is unreachable on a local target too (dev hosts,
// e.g. Docker Desktop/WSL), the HTTP gate degrades the same way:
// "container stays running". In production the controlplane shares the
// cargo-proxy network, so the local HTTP gate is real there.
func (d *Docker) waitHealthy(ctx context.Context, args []string, service string, spec Spec, log io.Writer) error {
	deadline := time.Now().Add(d.HealthTimeout)
	client := &http.Client{Timeout: 3 * time.Second}
	httpCheck := spec.HealthcheckPath != "" && d.target == nil
	var runningSince time.Time
	unreachableOnly := true
	refused := false   // reachable host, nothing listening on spec.Port
	serverErr := false // app answered but with 5xx
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		cid, state, restarts := d.containerStatus(ctx, args, service)
		if cid == "" {
			time.Sleep(2 * time.Second)
			continue
		}
		if state == "exited" || state == "dead" {
			return fmt.Errorf("container exited during startup — check the app's logs")
		}
		if state != "running" {
			time.Sleep(2 * time.Second)
			continue
		}
		// A restart during startup means the app crashed on boot.
		if restarts != "0" && restarts != "" {
			return fmt.Errorf("container keeps restarting (crash loop) — check the app's logs")
		}
		if runningSince.IsZero() {
			runningSince = time.Now()
		}

		// No HTTP healthcheck configured: pass once the container has stayed
		// running crash-free for a short window.
		if !httpCheck {
			if time.Since(runningSince) >= stableFor {
				_, _ = fmt.Fprintln(log, "container up and stable — marking live")
				return nil
			}
			time.Sleep(2 * time.Second)
			continue
		}

		ip, _ := d.output(ctx, "docker", "inspect", "-f",
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
				unreachableOnly, serverErr = false, true // reachable but unhealthy — keep gating
			} else if errors.Is(err, syscall.ECONNREFUSED) {
				unreachableOnly, refused = false, true // reachable host, app not listening yet
			}
		}
		// Probe network unreachable but container stable for 15s → accept.
		if unreachableOnly && !runningSince.IsZero() && time.Since(runningSince) > 15*time.Second {
			_, _ = fmt.Fprintln(log, "warning: health probe network unreachable from controlplane; accepting running container")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	// Timed out. With no HTTP check, accept a container that reached running.
	if !httpCheck {
		if !runningSince.IsZero() {
			_, _ = fmt.Fprintln(log, "container up — marking live")
			return nil
		}
		return fmt.Errorf("container did not start within %s", d.HealthTimeout)
	}
	// Turn the opaque HTTP timeout into an actionable message.
	switch {
	case refused:
		return fmt.Errorf("healthcheck timed out after %s: nothing is listening on port %d (connection refused). "+
			"Set the app's exposed port to the one your app listens on", d.HealthTimeout, spec.Port)
	case serverErr:
		return fmt.Errorf("healthcheck timed out after %s: %s on port %d kept returning 5xx",
			d.HealthTimeout, spec.HealthcheckPath, spec.Port)
	default:
		return fmt.Errorf("healthcheck timed out after %s (no response on port %d%s)",
			d.HealthTimeout, spec.Port, spec.HealthcheckPath)
	}
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

// RunningContainers counts every running container on the host — tenant apps,
// managed databases, and the platform's own three. Feeds the instance-wide
// monitoring view.
func (d *Docker) RunningContainers(ctx context.Context) (int, error) {
	out, err := d.output(ctx, "docker", "ps", "-q")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(out) == "" {
		return 0, nil
	}
	return len(strings.Split(strings.TrimSpace(out), "\n")), nil
}

// AppLogs streams the running app container's stdout+stderr (application and
// request logs) via `docker compose logs -f`, starting with the last `tail`
// lines. The returned reader is closed — and the underlying process killed —
// when ctx is cancelled (e.g. the SSE client disconnects) or the caller Closes.
func (d *Docker) AppLogs(ctx context.Context, appID string, tail int) (io.ReadCloser, error) {
	args, service, err := d.activeProject(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("app is not running")
	}
	full := append(append([]string{"compose"}, args...),
		"logs", "--no-color", "--tail", strconv.Itoa(tail), "-f", service)
	cmd := d.buildCmd(ctx, "docker", full...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return nil, err
	}
	go func() { _ = pw.CloseWithError(cmd.Wait()) }()
	return pr, nil
}

// Stop halts the app's containers but leaves the compose project on disk so
// Start can bring them back. It's a no-op-with-error if the app was never
// deployed (no compose project exists).
func (d *Docker) Stop(ctx context.Context, appID string, log io.Writer) error {
	args, _, err := d.activeProject(ctx, appID)
	if err != nil {
		return fmt.Errorf("app is not running")
	}
	return d.runCompose(ctx, log, args, "stop")
}

// Start resumes a previously stopped app's containers.
func (d *Docker) Start(ctx context.Context, appID string, log io.Writer) error {
	args, _, err := d.activeProject(ctx, appID)
	if err != nil {
		return fmt.Errorf("app has not been deployed")
	}
	d.ensureNetwork(ctx)
	return d.runCompose(ctx, log, args, "start")
}

// Teardown removes every color's compose project, not just the active one: a
// deploy interrupted mid-hand-off can leave two of them running, and a leftover
// container would keep answering on the app's domain after the app is deleted.
func (d *Docker) Teardown(ctx context.Context, appID, slug string, log io.Writer) error {
	for _, color := range []string{"", "blue", "green"} {
		if _, err := os.Stat(d.composePath(appID, color)); err != nil {
			continue
		}
		d.downColor(ctx, appID, color, log)
	}
	return os.RemoveAll(d.projectDir(appID))
}
