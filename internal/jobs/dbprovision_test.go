package jobs

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/databases"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

// fakeDBProvider implements reconciler.DatabaseProvider for tests.
type fakeDBProvider struct {
	provisionErr error
}

func (f *fakeDBProvider) ProvisionDB(_ context.Context, _ reconciler.DBSpec, _ io.Writer) error {
	return f.provisionErr
}

func (f *fakeDBProvider) ExecDB(_ context.Context, _, _, _ string, _ ...string) (string, error) {
	return "", nil
}

func (f *fakeDBProvider) SnapshotDB(_ context.Context, _, _, _, _ string) error { return nil }

func (f *fakeDBProvider) TeardownDB(_ context.Context, _ string, _ io.Writer) error { return nil }

func newTestInstance(t *testing.T, provider reconciler.DatabaseProvider) (svc *databases.Service, instID, actor pgtype.UUID) {
	t.Helper()
	pool := startPool(t)
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, _, err := auth.NewService(pool).Register(ctx, "o@x.co", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	org, err := orgs.NewService(pool).Create(ctx, "Acme", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	svc = databases.NewService(pool, box, provider, dataDir)
	inst, err := svc.Create(ctx, org.ID, u.ID, databases.CreateInput{
		Name:    "maindb",
		Engine:  "postgres",
		Version: "16",
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, inst.ID, u.ID
}

func TestDBProvisionWorker_Success(t *testing.T) {
	provider := &fakeDBProvider{}
	svc, id, actor := newTestInstance(t, provider)
	w := &DBProvisionWorker{Databases: svc}
	job := &river.Job[DBProvisionArgs]{Args: DBProvisionArgs{InstanceID: uuidStr(id)}}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("Work: %v", err)
	}
	detail, err := svc.Get(context.Background(), id, actor)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.Instance.Status != "running" {
		t.Fatalf("status = %q, want running", detail.Instance.Status)
	}
}

func TestDBProvisionWorker_ProviderError(t *testing.T) {
	provider := &fakeDBProvider{provisionErr: context.DeadlineExceeded}
	svc, id, actor := newTestInstance(t, provider)
	w := &DBProvisionWorker{Databases: svc}
	job := &river.Job[DBProvisionArgs]{Args: DBProvisionArgs{InstanceID: uuidStr(id)}}
	if err := w.Work(context.Background(), job); err != nil {
		t.Fatalf("Work should return nil on provider failure (no retry storm), got: %v", err)
	}
	detail, err := svc.Get(context.Background(), id, actor)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.Instance.Status != "error" {
		t.Fatalf("status = %q, want error", detail.Instance.Status)
	}
}
