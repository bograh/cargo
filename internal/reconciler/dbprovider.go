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
			_, err = d.ExecDB(ctx, spec.InstanceID, spec.Engine, "",
				"redis-cli", "-a", spec.AdminPass, "--no-auth-warning", "PING")
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
	composePath := filepath.Join(d.dbDir(instanceID), "compose.yaml")
	full := append([]string{"compose", "-f", composePath, "exec", "-T", "db"}, args...)
	return execCaptured(ctx, stdin, "docker", full...)
}

// SnapshotDB writes a point-in-time snapshot of the instance to destPath
// (".sql" appended for postgres, ".rdb" for redis).
func (d *Docker) SnapshotDB(ctx context.Context, instanceID, engine, adminPass, destPath string) error {
	composePath := filepath.Join(d.dbDir(instanceID), "compose.yaml")
	switch engine {
	case "redis":
		before, err := d.ExecDB(ctx, instanceID, engine, "",
			"redis-cli", "-a", adminPass, "--no-auth-warning", "LASTSAVE")
		if err != nil {
			return err
		}
		if _, err := d.ExecDB(ctx, instanceID, engine, "",
			"redis-cli", "-a", adminPass, "--no-auth-warning", "BGSAVE"); err != nil {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			after, err := d.ExecDB(ctx, instanceID, engine, "",
				"redis-cli", "-a", adminPass, "--no-auth-warning", "LASTSAVE")
			if err == nil && strings.TrimSpace(after) != strings.TrimSpace(before) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		return run(ctx, os.Stderr, "docker", "compose", "-f", composePath, "cp",
			"db:/data/dump.rdb", destPath+".rdb")
	default:
		out, err := d.ExecDB(ctx, instanceID, engine, "",
			"pg_dumpall", "--clean", "-U", "postgres")
		if err != nil {
			return err
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
// whitespace.
func execCaptured(ctx context.Context, stdinData string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdinData != "" {
		cmd.Stdin = strings.NewReader(stdinData)
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
