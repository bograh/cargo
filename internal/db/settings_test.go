package db

import (
	"context"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
)

func TestSettingsRoundTrip(t *testing.T) {
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

	q := sqlc.New(pool)
	_, err = q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{
		Key:   "apps_domain_suffix",
		Value: []byte(`"apps.example.com"`),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	row, err := q.GetInstanceSetting(ctx, "apps_domain_suffix")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(row.Value) != `"apps.example.com"` {
		t.Fatalf("value = %s", row.Value)
	}
}
