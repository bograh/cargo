package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

func TestOpenAndMigrate(t *testing.T) {
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
		SELECT FROM information_schema.tables
		WHERE table_name = 'instance_settings')`).Scan(&exists)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatal("instance_settings table missing after migrate")
	}
}

func TestMultiServerMigrationRoundTrip(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate up: %v", err)
	}
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var hostID string
	err = pool.QueryRow(ctx,
		`INSERT INTO hosts (name, address) VALUES ('w1', 'deploy@10.0.0.5') RETURNING id`).Scan(&hostID)
	if err != nil {
		t.Fatalf("insert host: %v", err)
	}
	var appName string
	err = pool.QueryRow(ctx, `SELECT name FROM applications LIMIT 1`).Scan(&appName)
	if err != nil && err != pgx.ErrNoRows {
		t.Fatalf("query applications: %v", err)
	}
	if appName != "" { // an app exists only in suites that seeded one; columns must accept host data
		if _, err := pool.Exec(ctx,
			`UPDATE applications SET host_id = $1, active_color = 'blue' WHERE id = (
				SELECT id FROM applications LIMIT 1)`, hostID); err != nil {
			t.Fatalf("columns missing before down: %v", err)
		}
	}
	pool.Close()

	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open for goose: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.DownContext(ctx, sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}
