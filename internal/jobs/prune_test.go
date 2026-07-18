package jobs

import (
	"context"
	"os"
	"testing"
)

func TestRunPruneKeepsNewestFive(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()

	var ids []string
	for i := 0; i < 7; i++ {
		dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
		if err != nil {
			t.Fatal(err)
		}
		id := uuidString(t, dep.ID)
		ids = append(ids, id)
		w, err := f.deps.LogWriter(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("log\n")); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		if err := f.deps.SetStatus(ctx, dep.ID, "deploying"); err != nil {
			t.Fatal(err)
		}
		if err := f.deps.Finish(ctx, dep.ID, "live", ""); err != nil {
			t.Fatal(err)
		}
	}

	if err := RunPrune(ctx, f.pipeline.Pool, f.deps); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var count int
	if err := f.pipeline.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployments`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("deployments after prune = %d, want 5", count)
	}
	// Oldest two logs removed, newest five retained.
	for i, id := range ids {
		_, err := os.Stat(f.deps.LogPath(id))
		if i < 2 && !os.IsNotExist(err) {
			t.Fatalf("old log %d still present (err=%v)", i, err)
		}
		if i >= 2 && err != nil {
			t.Fatalf("new log %d missing: %v", i, err)
		}
	}
}
