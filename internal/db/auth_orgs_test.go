package db

import (
	"context"
	"testing"
)

func TestAuthOrgsTablesExist(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()
	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	for _, table := range []string{"users", "sessions", "organizations", "memberships", "invites"} {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s missing after migrate", table)
		}
	}
}

func TestGithubInstallationsTableExists(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()
	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()
	var exists bool
	err = pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT FROM information_schema.tables WHERE table_name = 'github_installations')`).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("github_installations missing: %v", err)
	}
}
