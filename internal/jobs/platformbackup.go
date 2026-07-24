package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/riverqueue/river"
)

// Alerter is notified of platform-level failure conditions (backup/disk).
// Optional (nil = no alerts); implemented by internal/notify (m11 Task 8).
type Alerter interface {
	Alert(ctx context.Context, kind, detail string)
}

type PlatformBackupArgs struct{}

func (PlatformBackupArgs) Kind() string { return "platform_backup" }

type PlatformBackupWorker struct {
	river.WorkerDefaults[PlatformBackupArgs]
	B *PlatformBackuper
}

func (w *PlatformBackupWorker) Work(ctx context.Context, _ *river.Job[PlatformBackupArgs]) error {
	return w.B.Run(ctx)
}

// PlatformBackuper dumps the control-plane database, copies Traefik's ACME
// certs, and records a master-key fingerprint into <DataDir>/platform-backups.
//
// The runtime image ships no pg_dump and the certs live in Traefik's cargo-acme
// volume (not this process's filesystem), so both go through the Docker socket:
// pg_dump runs via `docker exec` into the platform DB container, certs via
// `docker cp` from the Traefik container. Containers are discovered by their
// compose project+service labels so naming/prefix differences don't matter.
type PlatformBackuper struct {
	DataDir     string
	DatabaseURL string
	MasterKey   []byte
	Keep        int    // backups retained (default 14)
	Project     string // compose project; "" → self-discover, else "cargo"
	DBService   string // compose service of the platform DB (default "db")
	TraefikSvc  string // compose service of Traefik (default "traefik")
	Alerter     Alerter

	// Test seams; nil in production (real docker-backed impls are used).
	now     func() time.Time
	dumpFn  func(ctx context.Context, dest string) error
	certsFn func(ctx context.Context, destDir string) error
}

func (b *PlatformBackuper) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Run performs a backup and, on failure, fires a backup_failed alert before
// returning the error so River retries with backoff.
func (b *PlatformBackuper) Run(ctx context.Context) error {
	err := b.run(ctx)
	if err != nil && b.Alerter != nil {
		b.Alerter.Alert(ctx, "backup_failed", err.Error())
	}
	return err
}

func (b *PlatformBackuper) run(ctx context.Context) error {
	dir := filepath.Join(b.DataDir, "platform-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := filepath.Join(dir, b.clock().UTC().Format("20060102-150405"))

	// 1. DB dump — the critical artifact; a failure fails the whole backup.
	dump := b.dumpFn
	if dump == nil {
		dump = b.dockerDump
	}
	if err := dump(ctx, base+".dump"); err != nil {
		return fmt.Errorf("database dump failed: %w", err)
	}

	// 2. Certs — best-effort; a fresh cert set is re-issued by ACME on restore,
	// so a missing copy must not fail the backup.
	certs := b.certsFn
	if certs == nil {
		certs = b.dockerCopyCerts
	}
	if err := certs(ctx, base+".certs"); err != nil {
		slog.Warn("platform backup: cert copy skipped", "err", err)
	}

	// 3. Key fingerprint — lets an operator confirm a backup set matches the
	// key they hold. The key itself is never written.
	fp := sha256.Sum256(b.MasterKey)
	if err := os.WriteFile(base+".keyfp", []byte(hex.EncodeToString(fp[:])+"\n"), 0o600); err != nil {
		return err
	}

	keep := b.Keep
	if keep <= 0 {
		keep = 14
	}
	return pruneBackups(dir, keep)
}

// dockerDump streams `pg_dump -Fc` from the platform DB container to dest.
func (b *PlatformBackuper) dockerDump(ctx context.Context, dest string) error {
	u, err := url.Parse(b.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	user := u.User.Username()
	dbname := strings.TrimPrefix(u.Path, "/")
	cid, err := b.resolve(ctx, b.dbService())
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", cid,
		"pg_dump", "-Fc", "-U", user, "-d", dbname)
	cmd.Stdout = f
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// dockerCopyCerts copies /acme/. out of the Traefik container into destDir.
func (b *PlatformBackuper) dockerCopyCerts(ctx context.Context, destDir string) error {
	cid, err := b.resolve(ctx, b.traefikSvc())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "cp", cid+":/acme/.", destDir)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (b *PlatformBackuper) dbService() string {
	if b.DBService != "" {
		return b.DBService
	}
	return "db"
}

func (b *PlatformBackuper) traefikSvc() string {
	if b.TraefikSvc != "" {
		return b.TraefikSvc
	}
	return "traefik"
}

// resolve returns the container ID of a compose service in this stack's project.
func (b *PlatformBackuper) resolve(ctx context.Context, service string) (string, error) {
	project := b.project(ctx)
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q",
		"-f", "label=com.docker.compose.project="+project,
		"-f", "label=com.docker.compose.service="+service).Output()
	if err != nil {
		return "", fmt.Errorf("resolve %s container: %w", service, err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("no running container for project=%s service=%s", project, service)
	}
	return strings.SplitN(id, "\n", 2)[0], nil
}

// project resolves the compose project name: an explicit override, else the
// label on this controlplane's own container, else "cargo".
func (b *PlatformBackuper) project(ctx context.Context) string {
	if b.Project != "" {
		return b.Project
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f",
			`{{index .Config.Labels "com.docker.compose.project"}}`, host).Output()
		if err == nil {
			if p := strings.TrimSpace(string(out)); p != "" {
				return p
			}
		}
	}
	return "cargo"
}

// pruneBackups keeps the newest `keep` backup sets (by timestamped name) and
// removes the .dump/.keyfp/.certs of older ones.
func pruneBackups(dir string, keep int) error {
	dumps, err := filepath.Glob(filepath.Join(dir, "*.dump"))
	if err != nil {
		return err
	}
	if len(dumps) <= keep {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dumps))) // newest first (name sorts chronologically)
	for _, d := range dumps[keep:] {
		base := strings.TrimSuffix(d, ".dump")
		_ = os.Remove(base + ".dump")
		_ = os.Remove(base + ".keyfp")
		_ = os.RemoveAll(base + ".certs")
	}
	return nil
}

// BackupInfo describes one on-disk backup set for the admin listing.
type BackupInfo struct {
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	HasCerts  bool      `json:"has_certs"`
}

// ListBackups returns the backup sets under <dataDir>/platform-backups, newest
// first. A missing directory is not an error (no backups yet).
func ListBackups(dataDir string) ([]BackupInfo, error) {
	dir := filepath.Join(dataDir, "platform-backups")
	dumps, err := filepath.Glob(filepath.Join(dir, "*.dump"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dumps)))
	out := make([]BackupInfo, 0, len(dumps))
	for _, d := range dumps {
		fi, err := os.Stat(d)
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(d, ".dump")
		_, certErr := os.Stat(base + ".certs")
		out = append(out, BackupInfo{
			Name:      strings.TrimSuffix(filepath.Base(d), ".dump"),
			SizeBytes: fi.Size(),
			CreatedAt: fi.ModTime().UTC(),
			HasCerts:  certErr == nil,
		})
	}
	return out, nil
}
