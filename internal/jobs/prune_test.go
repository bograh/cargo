package jobs

import (
	"context"
	"os"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// A rollback creates a new deployment that reuses an older deployment's image.
// When the older row ages out of the keep window its tag must NOT be removed,
// or the running container can never restart. The same protection covers a
// blue/green hand-off, where two colors run different images at once.
func TestPruneRetainsImageStillInUse(t *testing.T) {
	f := setup(t, "image")
	ctx := context.Background()

	const sharedTag = "app-rollback-target:abc123"
	// finishLive walks a deployment through the status machine and demotes
	// whatever was live before it, exactly as the deploy pipeline does.
	finishLive := func(id pgtype.UUID, tag string) {
		t.Helper()
		if err := f.deps.SetBuildInfo(ctx, id, "", tag); err != nil {
			t.Fatal(err)
		}
		if err := f.deps.SetStatus(ctx, id, "deploying"); err != nil {
			t.Fatal(err)
		}
		if err := f.deps.Finish(ctx, id, "live", ""); err != nil {
			t.Fatal(err)
		}
		if err := f.deps.Supersede(ctx, f.app.ID, id); err != nil {
			t.Fatal(err)
		}
	}

	// Oldest deployment: builds the image that a later rollback will reuse.
	first, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
	if err != nil {
		t.Fatal(err)
	}
	finishLive(first.ID, sharedTag)

	// Enough newer deployments to push the first one out of the keep window.
	for i := 0; i < keepDeployments; i++ {
		dep, err := f.deps.Create(ctx, f.app.ID, f.owner, "manual")
		if err != nil {
			t.Fatal(err)
		}
		finishLive(dep.ID, "app-other:v"+uuidString(t, dep.ID)[:4])
	}

	// The rollback: newest deployment, live, running the first one's image.
	live, err := f.deps.Rollback(ctx, f.app.ID, f.owner, first.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	finishLive(live.ID, sharedTag)

	q := sqlc.New(f.pipeline.Pool)
	tags, err := q.ListRetainedImageTags(ctx, sqlc.ListRetainedImageTagsParams{
		AppID: f.app.ID, Keep: keepDeployments,
	})
	if err != nil {
		t.Fatalf("retained tags: %v", err)
	}
	var found bool
	for _, tag := range tags {
		if tag == sharedTag {
			found = true
		}
	}
	if !found {
		t.Fatalf("live image %q not reported as retained; prune would untag a running container (got %v)",
			sharedTag, tags)
	}

	// The stale row itself is still pruned — only its image survives.
	if err := RunPrune(ctx, f.pipeline.Pool, f.deps); err != nil {
		t.Fatalf("prune: %v", err)
	}
	var stale int
	if err := f.pipeline.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM deployments WHERE id = $1`, first.ID).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatal("stale deployment row survived the prune")
	}
}

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
