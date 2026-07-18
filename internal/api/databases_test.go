package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/databases"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubDatabases struct {
	inst    sqlc.DatabaseInstance
	detail  databases.Detail
	list    []databases.InstanceSummary
	url     string
	att     sqlc.DatabaseAttachment
	snap    string
	snaps   []databases.SnapshotInfo
	snapDir string
	err     error
}

func (s stubDatabases) Create(_ context.Context, _, _ pgtype.UUID, _ databases.CreateInput) (sqlc.DatabaseInstance, error) {
	return s.inst, s.err
}
func (s stubDatabases) List(_ context.Context, _, _ pgtype.UUID) ([]databases.InstanceSummary, error) {
	return s.list, s.err
}
func (s stubDatabases) Get(_ context.Context, _, _ pgtype.UUID) (databases.Detail, error) {
	return s.detail, s.err
}
func (s stubDatabases) Delete(_ context.Context, _, _ pgtype.UUID) error { return s.err }
func (s stubDatabases) Attach(_ context.Context, _, _, _ pgtype.UUID) (string, sqlc.DatabaseAttachment, error) {
	return s.url, s.att, s.err
}
func (s stubDatabases) Detach(_ context.Context, _, _, _ pgtype.UUID) error { return s.err }
func (s stubDatabases) Snapshot(_ context.Context, _, _ pgtype.UUID) (string, error) {
	return s.snap, s.err
}
func (s stubDatabases) ListSnapshots(_ context.Context, _, _ pgtype.UUID) ([]databases.SnapshotInfo, error) {
	return s.snaps, s.err
}
func (s stubDatabases) SnapshotPath(_ context.Context, _, _ pgtype.UUID, name string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return filepath.Join(s.snapDir, name), nil
}
func (s stubDatabases) DeleteSnapshot(_ context.Context, _, _ pgtype.UUID, _ string) error {
	return s.err
}
func (s stubDatabases) LogPath(id string) string { return filepath.Join(os.TempDir(), id+".log") }

func dbServer(d DatabaseService, e Enqueuer) *Server {
	return &Server{
		auth:      stubAuth{user: sqlc.User{Email: "a@b.co"}},
		databases: d,
		enqueue:   e,
	}
}

func TestCreateDatabaseEnqueues(t *testing.T) {
	var id pgtype.UUID
	if err := id.Scan(testUUID); err != nil {
		t.Fatal(err)
	}
	enq := &recordingEnqueuer{}
	s := dbServer(stubDatabases{inst: sqlc.DatabaseInstance{ID: id, Status: "provisioning"}}, enq)
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/databases",
		`{"name":"mydb","engine":"postgres","version":"16"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"provisioning"`) {
		t.Fatalf("body missing status: %s", rec.Body)
	}
	if len(enq.ids) != 1 || enq.ids[0] != testUUID {
		t.Fatalf("enqueued = %v", enq.ids)
	}
}

func TestCreateDatabaseValidationError(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrValidation}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/databases", `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCreateDatabaseConflict(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrConflict}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs/"+testUUID+"/databases",
		`{"name":"mydb","engine":"postgres","version":"16"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetDatabaseForbidden(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrForbidden}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetDatabaseNotFound(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrNotFound}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeleteDatabaseConflict(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrConflict}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/databases/"+testUUID, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAttachDatabaseReturnsURLNotDetail(t *testing.T) {
	att := sqlc.DatabaseAttachment{DbName: pgtype.Text{String: "app_x", Valid: true}}
	s := dbServer(stubDatabases{url: "postgres://user:pass@host:5432/app_x", att: att}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/databases/"+testUUID+"/attachments",
		`{"app_id":"`+testUUID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "postgres://user:pass@host:5432/app_x") {
		t.Fatalf("attach response missing url: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"DATABASE_URL"`) {
		t.Fatalf("attach response missing env_key: %s", rec.Body)
	}

	// GET detail must never surface a connection URL / secret.
	detail := databases.Detail{
		Instance:    sqlc.DatabaseInstance{Status: "running"},
		Attachments: []sqlc.DatabaseAttachment{att},
	}
	s2 := dbServer(stubDatabases{detail: detail}, &recordingEnqueuer{})
	rec2 := doAuthed(t, s2, http.MethodGet, "/api/v1/databases/"+testUUID, "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec2.Code, rec2.Body)
	}
	if strings.Contains(rec2.Body.String(), "postgres://user:pass") {
		t.Fatalf("detail leaked connection url: %s", rec2.Body)
	}
}

func TestAttachDatabaseRedisEnvKey(t *testing.T) {
	att := sqlc.DatabaseAttachment{DbIndex: pgtype.Int4{Int32: 3, Valid: true}}
	s := dbServer(stubDatabases{url: "redis://:pass@host:6379/3", att: att}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/databases/"+testUUID+"/attachments",
		`{"app_id":"`+testUUID+`"}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"REDIS_URL"`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestDetachDatabaseNotFound(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrNotFound}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/databases/"+testUUID+"/attachments/"+testUUID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSnapshotDownload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20260101-000000.sql"), []byte("dump-contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := dbServer(stubDatabases{snapDir: dir}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID+"/snapshots/20260101-000000.sql", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if rec.Body.String() != "dump-contents" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestSnapshotDownloadUnknownName(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrValidation}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID+"/snapshots/unknown.sql", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListSnapshots(t *testing.T) {
	s := dbServer(stubDatabases{snaps: []databases.SnapshotInfo{{Name: "a.sql", Size: 10}}}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID+"/snapshots", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "a.sql") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestDeleteSnapshotForbidden(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrForbidden}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/databases/"+testUUID+"/snapshots/a.sql", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDatabaseLogsReplaysFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, testUUID+".log")
	if err := os.WriteFile(logPath, []byte("provisioning...\ndone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := stubDatabases{detail: databases.Detail{Instance: sqlc.DatabaseInstance{Status: "running"}}}
	s := &Server{
		auth:      stubAuth{user: sqlc.User{Email: "a@b.co"}},
		databases: dbLogPathOverride{stubDatabases: d, dir: dir},
	}
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID+"/logs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "provisioning...") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

// dbLogPathOverride lets a single test point LogPath at a temp dir.
type dbLogPathOverride struct {
	stubDatabases
	dir string
}

func (d dbLogPathOverride) LogPath(id string) string { return filepath.Join(d.dir, id+".log") }

func TestDatabaseLogsForbidden(t *testing.T) {
	s := dbServer(stubDatabases{err: databases.ErrForbidden}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/databases/"+testUUID+"/logs", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListDatabasesIncludesAttachmentCount(t *testing.T) {
	var id pgtype.UUID
	if err := id.Scan(testUUID); err != nil {
		t.Fatal(err)
	}
	s := dbServer(stubDatabases{list: []databases.InstanceSummary{
		{Instance: sqlc.DatabaseInstance{ID: id, Name: "mydb"}, AttachmentCount: 2},
	}}, &recordingEnqueuer{})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/orgs/"+testUUID+"/databases", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"attachment_count":2`) {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}
