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

// MongoshAdmin is the mongosh invocation that authenticates as the instance
// admin using credentials from the container environment (MONGO_ADMIN_USER /
// MONGO_ADMIN_PWD, set by ProvisionDB). Running it via `sh -c` expands the
// variables inside the container, so the password never appears on the host
// process argv.
const MongoshAdmin = `mongosh --quiet -u "$MONGO_ADMIN_USER" -p "$MONGO_ADMIN_PWD" --authenticationDatabase admin`

// mongodumpAdmin is the mongodump equivalent, authenticating from container env.
const mongodumpAdmin = `mongodump -u "$MONGO_ADMIN_USER" -p "$MONGO_ADMIN_PWD" --authenticationDatabase admin --archive`

// MySQLClient is the mysql invocation that authenticates as root. It exports
// MYSQL_PWD (read by the client) from MYSQL_ROOT_PASSWORD for that process
// only — run via `sh -c` so the secret is never on argv. MYSQL_PWD must NOT
// be set in the container's persistent env: the mysql image's first-boot
// entrypoint also reads it and fails to apply the root password.
const MySQLClient = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -u root`

// mysqldumpAll is the mysqldump equivalent used for snapshots.
const mysqldumpAll = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -u root --all-databases`

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
	// The project directory is 0700 so only the cargo server's own host user
	// can traverse/read it — that's what protects the secrets on the host.
	// Files inside it are 0644: the official images run as a non-root,
	// non-host uid (e.g. redis drops to uid 999), so the file itself must be
	// world-readable for the container to open it; the 0700 directory is
	// what keeps other host users out, not the file mode. MkdirAll doesn't
	// tighten permissions on an already-existing directory, so Chmod
	// explicitly.
	dir := d.dbDir(spec.InstanceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	var envVars map[string]string
	switch spec.Engine {
	case "redis":
		conf := fmt.Sprintf("requirepass %s\n", spec.AdminPass)
		if err := os.WriteFile(filepath.Join(dir, "redis.conf"), []byte(conf), 0o644); err != nil {
			return err
		}
		envVars = map[string]string{"REDISCLI_AUTH": spec.AdminPass}
	case "mysql":
		// Only MYSQL_ROOT_PASSWORD — it initializes the root account. MYSQL_PWD
		// must not be set here: the entrypoint's own client reads it and then
		// fails to apply the root password. Clients read it transiently instead
		// (see reconciler.MySQLClient).
		envVars = map[string]string{"MYSQL_ROOT_PASSWORD": spec.AdminPass}
	case "mongodb":
		// MONGO_INITDB_* create the root user on first boot; MONGO_ADMIN_* are
		// read by MongoshAdmin/mongodump (via `sh -c`) for later auth.
		envVars = map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": "root",
			"MONGO_INITDB_ROOT_PASSWORD": spec.AdminPass,
			"MONGO_ADMIN_USER":           "root",
			"MONGO_ADMIN_PWD":            spec.AdminPass,
		}
	default: // postgres
		envVars = map[string]string{"POSTGRES_PASSWORD": spec.AdminPass}
	}
	envContent, err := generateEnvFile(envVars)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0o644); err != nil {
		return err
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
				"redis-cli", "--no-auth-warning", "PING")
		case "mysql":
			// SELECT 1 confirms the server is up AND root auth is initialized.
			_, err = d.ExecDB(ctx, spec.InstanceID, spec.Engine, "",
				"sh", "-c", MySQLClient+` -e "SELECT 1"`)
		case "mongodb":
			// An authenticated ping confirms the root user has been created.
			_, err = d.ExecDB(ctx, spec.InstanceID, spec.Engine, "",
				"sh", "-c", MongoshAdmin+` --eval "db.adminCommand({ping:1})"`)
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
// stdin, and returns combined stdout. Secrets needed by the command (e.g.
// REDISCLI_AUTH) are supplied via the instance's env_file, written once by
// ProvisionDB — never placed on argv or forwarded from the host process.
func (d *Docker) ExecDB(ctx context.Context, instanceID, engine string, stdin string, args ...string) (string, error) {
	_ = engine
	composePath := filepath.Join(d.dbDir(instanceID), "compose.yaml")
	full := []string{"compose", "-f", composePath, "exec", "-T", "db"}
	full = append(full, args...)
	return execCaptured(ctx, stdin, nil, "docker", full...)
}

// SnapshotDB writes a point-in-time snapshot of the instance to destPath
// (".sql" appended for postgres, ".rdb" for redis).
func (d *Docker) SnapshotDB(ctx context.Context, instanceID, engine, adminPass, destPath string) error {
	_ = adminPass // auth is supplied via the instance's env_file (see ProvisionDB), not passed here
	composePath := filepath.Join(d.dbDir(instanceID), "compose.yaml")
	switch engine {
	case "redis":
		before, err := d.ExecDB(ctx, instanceID, engine, "",
			"redis-cli", "--no-auth-warning", "LASTSAVE")
		if err != nil {
			return err
		}
		if _, err := d.ExecDB(ctx, instanceID, engine, "",
			"redis-cli", "--no-auth-warning", "BGSAVE"); err != nil {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		saved := false
		for time.Now().Before(deadline) {
			after, err := d.ExecDB(ctx, instanceID, engine, "",
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
	case "mysql":
		out, errOut, err := execCapturedSplit(ctx, "", nil, "docker",
			"compose", "-f", composePath, "exec", "-T", "db",
			"sh", "-c", mysqldumpAll)
		if err != nil {
			return fmt.Errorf("mysqldump: %w: %s", err, errOut)
		}
		return os.WriteFile(destPath+".sql", []byte(out), 0o600)
	case "mongodb":
		out, errOut, err := execCapturedSplit(ctx, "", nil, "docker",
			"compose", "-f", composePath, "exec", "-T", "db",
			"sh", "-c", mongodumpAdmin)
		if err != nil {
			return fmt.Errorf("mongodump: %w: %s", err, errOut)
		}
		return os.WriteFile(destPath+".archive", []byte(out), 0o600)
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
