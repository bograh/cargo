package reconciler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var _ DatabaseProvider = (*Docker)(nil)

// dbDir returns the per-instance project directory for a managed database.
func (d *Docker) dbDir(instanceID string) string {
	return filepath.Join(d.dataDir, "databases", instanceID)
}

func (d *Docker) ensureDataNetwork(ctx context.Context, log io.Writer) {
	// "already exists" is fine — ignore the error entirely.
	_ = run(ctx, log, "docker", "network", "create", "cargo-data")
}

// ProvisionDB writes the compose project for a managed database instance,
// starts it, and waits for it to become ready.
func (d *Docker) ProvisionDB(ctx context.Context, spec DBSpec, log io.Writer) error {
	dir := d.dbDir(spec.InstanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	switch spec.Engine {
	case "redis":
		conf := fmt.Sprintf("requirepass %s\n", spec.AdminPass)
		if err := os.WriteFile(filepath.Join(dir, "redis.conf"), []byte(conf), 0o600); err != nil {
			return err
		}
	default:
		envContent, err := generateEnvFile(map[string]string{"POSTGRES_PASSWORD": spec.AdminPass})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o600); err != nil {
			return err
		}
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(GenerateDBCompose(spec)), 0o644); err != nil {
		return err
	}
	d.ensureDataNetwork(ctx, log)
	if err := run(ctx, log, "docker", "compose", "-f", composePath, "up", "-d"); err != nil {
		return err
	}
	return d.waitDBReady(ctx, spec, log)
}

// waitDBReady polls until the database accepts connections, up to 60s.
func (d *Docker) waitDBReady(ctx context.Context, spec DBSpec, log io.Writer) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		switch spec.Engine {
		case "redis":
			_, err = d.execDBWithEnv(ctx, spec.InstanceID, "",
				map[string]string{"REDISCLI_AUTH": spec.AdminPass},
				"redis-cli", "--no-auth-warning", "PING")
		default:
			_, err = d.ExecDB(ctx, spec.InstanceID, spec.Engine, "",
				"pg_isready", "-U", "postgres")
		}
		if err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("database %s not ready after 60s", spec.InstanceID)
}

// ExecDB runs a command inside the running db container, optionally feeding
// stdin, and returns combined stdout.
func (d *Docker) ExecDB(ctx context.Context, instanceID, engine string, stdin string, args ...string) (string, error) {
	_ = engine
	return d.execDBWithEnv(ctx, instanceID, stdin, nil, args...)
}

// execDBWithEnv runs a command inside the running db container, optionally
// forwarding host-process environment variables into the container via
// `docker compose exec -e KEY`. The values themselves are never placed on
// the docker CLI argv (host- or container-side) — they are read by docker
// from the calling process's own environment, so they never appear in any
// process list.
func (d *Docker) execDBWithEnv(ctx context.Context, instanceID, stdin string, env map[string]string, args ...string) (string, error) {
	composePath := filepath.Join(d.dbDir(instanceID), "compose.yaml")
	full := []string{"compose", "-f", composePath, "exec", "-T"}
	extraEnv := make([]string, 0, len(env))
	for k := range env {
		full = append(full, "-e", k)
	}
	for k, v := range env {
		extraEnv = append(extraEnv, k+"="+v)
	}
	full = append(full, "db")
	full = append(full, args...)
	return execCaptured(ctx, stdin, extraEnv, "docker", full...)
}

// SnapshotDB writes a point-in-time snapshot of the instance to destPath
// (".sql" appended for postgres, ".rdb" for redis).
func (d *Docker) SnapshotDB(ctx context.Context, instanceID, engine, adminPass, destPath string) error {
	composePath := filepath.Join(d.dbDir(instanceID), "compose.yaml")
	switch engine {
	case "redis":
		redisEnv := map[string]string{"REDISCLI_AUTH": adminPass}
		before, err := d.execDBWithEnv(ctx, instanceID, "", redisEnv,
			"redis-cli", "--no-auth-warning", "LASTSAVE")
		if err != nil {
			return err
		}
		if _, err := d.execDBWithEnv(ctx, instanceID, "", redisEnv,
			"redis-cli", "--no-auth-warning", "BGSAVE"); err != nil {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		saved := false
		for time.Now().Before(deadline) {
			after, err := d.execDBWithEnv(ctx, instanceID, "", redisEnv,
				"redis-cli", "--no-auth-warning", "LASTSAVE")
			if err == nil && strings.TrimSpace(after) != strings.TrimSpace(before) {
				saved = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !saved {
			return fmt.Errorf("redis snapshot: BGSAVE did not complete within 10s")
		}
		return run(ctx, os.Stderr, "docker", "compose", "-f", composePath, "cp",
			"db:/data/dump.rdb", destPath+".rdb")
	default:
		out, errOut, err := execCapturedSplit(ctx, "", nil, "docker",
			"compose", "-f", composePath, "exec", "-T", "db",
			"pg_dumpall", "--clean", "-U", "postgres")
		if err != nil {
			return fmt.Errorf("pg_dumpall: %w: %s", err, errOut)
		}
		return os.WriteFile(destPath+".sql", []byte(out), 0o600)
	}
}

// TeardownDB stops the instance's containers, removes volumes, and deletes
// the project directory.
func (d *Docker) TeardownDB(ctx context.Context, instanceID string, log io.Writer) error {
	dir := d.dbDir(instanceID)
	composePath := filepath.Join(dir, "compose.yaml")
	if _, err := os.Stat(composePath); err == nil {
		if err := run(ctx, log, "docker", "compose", "-f", composePath, "down", "-v", "--remove-orphans"); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

// execCaptured runs name+args, feeding stdinData to the process's stdin
// when non-empty, and returns combined stdout+stderr trimmed of trailing
// whitespace. extraEnv entries (KEY=VALUE) are appended to the child
// process's environment (inherited from os.Environ()) — never placed on
// argv — so secrets passed this way never show up in process listings.
func execCaptured(ctx context.Context, stdinData string, extraEnv []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdinData != "" {
		cmd.Stdin = strings.NewReader(stdinData)
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, buf.String())
	}
	return strings.TrimSpace(buf.String()), nil
}

// execCapturedSplit runs name+args like execCaptured, but keeps stdout and
// stderr separate so callers that persist stdout verbatim (e.g. a pg_dumpall
// SQL dump) don't get it corrupted by interleaved stderr NOTICEs/warnings.
func execCapturedSplit(ctx context.Context, stdinData string, extraEnv []string, name string, args ...string) (stdout string, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdinData != "" {
		cmd.Stdin = strings.NewReader(stdinData)
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		return "", errBuf.String(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), runErr)
	}
	return outBuf.String(), errBuf.String(), nil
}
