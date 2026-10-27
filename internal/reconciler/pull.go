package reconciler

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// RegistryAuth is one app's private-registry login.
type RegistryAuth struct {
	Server, Username, Password string
}

// PullImage pulls ref onto the daemon this provider targets, authenticating
// with auth (nil = anonymous) in a throwaway DOCKER_CONFIG.
//
// `docker login` used to write into the control plane's global
// ~/.docker/config.json and was never undone, so every later pull on the
// instance — any tenant's — carried that tenant's registry credentials, and a
// hosted app's pull (which uses the host's own DOCKER_CONFIG) carried none.
// Pulling explicitly with only this app's credentials also means an app cannot
// run a private image merely because another tenant's deploy left it in the
// daemon's cache: the pull is the authorization check.
func (d *Docker) PullImage(ctx context.Context, ref string, auth *RegistryAuth, log io.Writer) error {
	cfgDir, err := os.MkdirTemp("", "cargo-pull-*")
	if err != nil {
		return fmt.Errorf("registry config dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(cfgDir) }()
	env := d.isolatedEnv(cfgDir)

	if auth != nil {
		_, _ = fmt.Fprintf(log, "==> docker login %s\n", auth.Server)
		login := exec.CommandContext(ctx, "docker", "login", auth.Server,
			"-u", auth.Username, "--password-stdin")
		login.Env = env
		login.Stdin = strings.NewReader(auth.Password)
		login.Stdout, login.Stderr = log, log
		if err := login.Run(); err != nil {
			return fmt.Errorf("docker login: %w", err)
		}
	}
	_, _ = fmt.Fprintf(log, "==> pulling %s\n", ref)
	pull := exec.CommandContext(ctx, "docker", "pull", ref)
	pull.Env = env
	pull.Stdout, pull.Stderr = log, log
	if err := pull.Run(); err != nil {
		return fmt.Errorf("docker pull %s: %w", ref, err)
	}
	return nil
}

// isolatedEnv is the process env with DOCKER_CONFIG pointed at cfgDir. A
// worker host is normally addressed through a docker context stored in its
// own DOCKER_CONFIG; with that swapped out, it is addressed by DOCKER_HOST
// instead (the per-host ssh wrapper stays on PATH, so the same key and pinned
// host key apply).
func (d *Docker) isolatedEnv(cfgDir string) []string {
	base := os.Environ()
	if d.target != nil {
		base = append(base, d.target.Env...)
	}
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if k == "DOCKER_CONFIG" || k == "DOCKER_CONTEXT" {
			continue
		}
		if d.target != nil && k == "DOCKER_HOST" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "DOCKER_CONFIG="+cfgDir)
	if d.target != nil {
		env = append(env, "DOCKER_HOST="+d.target.Endpoint)
	}
	return env
}

// ShipImage makes a locally-built image available on this provider's worker
// host. Git and nixpacks builds run against the control plane's daemon, so on
// a worker host the app-<slug>:<id> tag simply did not exist and every
// git-source (or rollback) deploy there failed at `up`. The image is streamed
// `docker save | docker load` over the host's SSH connection. It is a no-op
// for the control plane, and when the host already holds the image (a
// rollback to a version that ran there).
func (d *Docker) ShipImage(ctx context.Context, ref string, log io.Writer) error {
	if d.target == nil {
		return nil
	}
	if _, err := d.output(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref); err == nil {
		return nil
	}
	_, _ = fmt.Fprintf(log, "==> shipping image %s to worker host\n", ref)
	save := exec.CommandContext(ctx, "docker", "save", ref)
	load := d.buildCmd(ctx, "docker", "load")
	pipe, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	var saveErr strings.Builder
	save.Stderr = &saveErr
	load.Stdin = pipe
	load.Stdout, load.Stderr = log, log
	if err := save.Start(); err != nil {
		return fmt.Errorf("docker save %s: %w", ref, err)
	}
	loadErr := load.Run()
	// If load died early, save may block on a full pipe; close our end.
	_ = pipe.Close()
	if err := save.Wait(); err != nil {
		return fmt.Errorf("docker save %s: %w: %s", ref, err, strings.TrimSpace(saveErr.String()))
	}
	if loadErr != nil {
		return fmt.Errorf("docker load on worker host: %w", loadErr)
	}
	return nil
}

// RemoveImage untags ref on this provider's daemon (best-effort cleanup).
func (d *Docker) RemoveImage(ctx context.Context, ref string) error {
	return d.run(ctx, io.Discard, "docker", "rmi", "-f", ref)
}
