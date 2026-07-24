package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type recordingAlerter struct {
	kinds []string
}

func (a *recordingAlerter) Alert(_ context.Context, kind, _ string) {
	a.kinds = append(a.kinds, kind)
}

func TestPlatformBackupRun(t *testing.T) {
	dir := t.TempDir()
	b := &PlatformBackuper{
		DataDir:   dir,
		MasterKey: []byte("0123456789abcdef0123456789abcdef"),
		Keep:      14,
		dumpFn:    func(_ context.Context, dest string) error { return os.WriteFile(dest, []byte("DUMP"), 0o600) },
		certsFn:   func(_ context.Context, destDir string) error { return os.MkdirAll(destDir, 0o700) },
	}
	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	list, err := ListBackups(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListBackups = %v, %v; want 1 entry", list, err)
	}
	if list[0].SizeBytes != 4 || !list[0].HasCerts {
		t.Fatalf("unexpected backup info: %+v", list[0])
	}
	// A key fingerprint sidecar must exist (the key itself must not).
	base := filepath.Join(dir, "platform-backups", list[0].Name)
	fp, err := os.ReadFile(base + ".keyfp")
	if err != nil || len(fp) == 0 {
		t.Fatalf("keyfp missing: %v", err)
	}
	if string(fp) == string(b.MasterKey) {
		t.Fatal("master key must never be written to disk")
	}
}

func TestPlatformBackupDumpFailureAlerts(t *testing.T) {
	al := &recordingAlerter{}
	b := &PlatformBackuper{
		DataDir:   t.TempDir(),
		MasterKey: []byte("k"),
		Alerter:   al,
		dumpFn:    func(_ context.Context, _ string) error { return errors.New("boom") },
	}
	if err := b.Run(context.Background()); err == nil {
		t.Fatal("expected error when dump fails")
	}
	if len(al.kinds) != 1 || al.kinds[0] != "backup_failed" {
		t.Fatalf("alerts = %v; want [backup_failed]", al.kinds)
	}
}

func TestPruneBackupsKeepsNewestN(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "platform-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Create 5 backup sets with lexically ordered (chronological) names.
	stamps := []string{
		"20260101-000000", "20260102-000000", "20260103-000000",
		"20260104-000000", "20260105-000000",
	}
	for _, s := range stamps {
		base := filepath.Join(dir, s)
		mustWrite(t, base+".dump", "d")
		mustWrite(t, base+".keyfp", "f")
		if err := os.MkdirAll(base+".certs", 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneBackups(dir, 2); err != nil {
		t.Fatalf("prune: %v", err)
	}
	list, _ := ListBackups(dataDir)
	if len(list) != 2 {
		t.Fatalf("after prune: %d backups, want 2", len(list))
	}
	// The two newest survive; older sidecars are gone.
	if list[0].Name != "20260105-000000" || list[1].Name != "20260104-000000" {
		t.Fatalf("kept the wrong sets: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, "20260101-000000.keyfp")); !os.IsNotExist(err) {
		t.Fatal("old keyfp should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "20260101-000000.certs")); !os.IsNotExist(err) {
		t.Fatal("old certs dir should have been pruned")
	}
}

func TestListBackupsMissingDir(t *testing.T) {
	list, err := ListBackups(t.TempDir()) // no platform-backups subdir yet
	if err != nil {
		t.Fatalf("ListBackups on empty dir errored: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want 0 backups, got %d", len(list))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
