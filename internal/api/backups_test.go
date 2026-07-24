package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bograh/cargo/internal/config"
)

func TestRunBackupEnqueues(t *testing.T) {
	enq := &recordingEnqueuer{}
	s := &Server{enqueue: enq}
	rec := httptest.NewRecorder()
	s.handleRunBackup(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/backups", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if len(enq.ids) != 1 || enq.ids[0] != "platform_backup" {
		t.Fatalf("enqueued = %v; want [platform_backup]", enq.ids)
	}
}

func TestListBackups(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "platform-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20260101-000000.dump"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{DataDir: dataDir}}
	rec := httptest.NewRecorder()
	s.handleListBackups(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/backups", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0]["name"] != "20260101-000000" {
		t.Fatalf("unexpected listing: %v", out)
	}
}
