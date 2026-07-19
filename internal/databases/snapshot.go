package databases

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// snapshotDir returns the per-instance backups directory.
func (s *Service) snapshotDir(id string) string {
	return filepath.Join(s.dataDir, "db-backups", id)
}

// Snapshot writes a point-in-time backup and returns the created file name.
// The whole snapshot surface is admin-gated: a postgres snapshot is a
// pg_dumpall that includes role password hashes, so it must not be reachable
// by ordinary members.
func (s *Service) Snapshot(ctx context.Context, id, actor pgtype.UUID) (string, error) {
	inst, err := s.instFor(ctx, id, actor, "admin")
	if err != nil {
		return "", err
	}
	idStr := uuidStr(inst.ID)
	dir := s.snapshotDir(idStr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	adminPass, err := s.openSecret(inst.AdminSecret)
	if err != nil {
		return "", err
	}
	base := time.Now().UTC().Format("20060102-150405")
	if err := s.provider.SnapshotDB(ctx, idStr, inst.Engine, adminPass, filepath.Join(dir, base)); err != nil {
		return "", err
	}
	ext := ".sql"
	if inst.Engine == "redis" {
		ext = ".rdb"
	}
	return base + ext, nil
}

// ListSnapshots lists on-disk snapshots for the instance, newest first.
func (s *Service) ListSnapshots(ctx context.Context, id, actor pgtype.UUID) ([]SnapshotInfo, error) {
	inst, err := s.instFor(ctx, id, actor, "admin")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.snapshotDir(uuidStr(inst.ID)))
	if os.IsNotExist(err) {
		return []SnapshotInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, SnapshotInfo{Name: e.Name(), Size: info.Size(), Created: info.ModTime()})
	}
	return out, nil
}

// SnapshotPath resolves a snapshot name to its absolute path. The name must
// appear in the directory listing, which rejects any path traversal.
func (s *Service) SnapshotPath(ctx context.Context, id, actor pgtype.UUID, name string) (string, error) {
	snaps, err := s.ListSnapshots(ctx, id, actor)
	if err != nil {
		return "", err
	}
	if !validSnapshotName(name, snaps) {
		return "", fmt.Errorf("%w: unknown snapshot %q", ErrValidation, name)
	}
	return filepath.Join(s.snapshotDir(uuidStr(id)), name), nil
}

// DeleteSnapshot removes a snapshot file after validating it is a real entry.
func (s *Service) DeleteSnapshot(ctx context.Context, id, actor pgtype.UUID, name string) error {
	inst, err := s.instFor(ctx, id, actor, "admin")
	if err != nil {
		return err
	}
	snaps, err := s.ListSnapshots(ctx, id, actor)
	if err != nil {
		return err
	}
	if !validSnapshotName(name, snaps) {
		return fmt.Errorf("%w: unknown snapshot %q", ErrValidation, name)
	}
	return os.Remove(filepath.Join(s.snapshotDir(uuidStr(inst.ID)), name))
}

// validSnapshotName guards against traversal: the name must be a plain
// basename that exactly matches a listed snapshot.
func validSnapshotName(name string, snaps []SnapshotInfo) bool {
	if name == "" || name != filepath.Base(name) {
		return false
	}
	for _, s := range snaps {
		if s.Name == name {
			return true
		}
	}
	return false
}
