package reconciler

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// recordingStore is a stub ColorStore capturing the write sequence.
type recordingStore struct {
	active map[string]string
	writes []string
}

func (r *recordingStore) ActiveColor(ctx context.Context, appID string) (string, error) {
	return r.active[appID], nil
}
func (r *recordingStore) SetActiveColor(ctx context.Context, appID, color string) error {
	r.writes = append(r.writes, color)
	if r.active == nil {
		r.active = map[string]string{}
	}
	r.active[appID] = color
	return nil
}

// The injected store drives the color sequence; state.json is never written
// once Postgres owns the color.
func TestApplyBlueGreenUsesInjectedStore(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	ctx := context.Background()
	dataDir := t.TempDir()
	d := NewDocker(dataDir)
	d.HealthTimeout = 60 * time.Second
	store := &recordingStore{}
	d.SetColors(store)
	spec := Spec{
		AppID: "bg-db-1", Slug: "bg-db-test", Image: "nginx:alpine", Port: 80,
		Domains: []string{"bg-db-test.apps.localhost"}, BlueGreen: true,
	}
	var log bytes.Buffer
	t.Cleanup(func() { _ = d.Teardown(ctx, spec.AppID, spec.Slug, &log) })

	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("first apply: %v\n%s", err, log.String())
	}
	if err := d.Apply(ctx, spec, &log); err != nil {
		t.Fatalf("second apply: %v\n%s", err, log.String())
	}
	if len(store.writes) < 2 || store.writes[0] != "blue" || store.writes[1] != "green" {
		t.Fatalf("color writes = %v, want [blue green]", store.writes)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "apps", spec.AppID, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json written although a ColorStore is wired")
	}
	if got := d.activeColor(ctx, spec.AppID); got != "green" {
		t.Fatalf("activeColor via store = %q, want green", got)
	}
}

// A DB-backed ColorStore over applications.active_color: unknown app reads as
// the empty color with no error (matching legacy file semantics).
func TestActiveColorEmptyForUnknownApp(t *testing.T) {
	ctx := context.Background()
	d := NewDocker(t.TempDir())
	d.SetColors(&recordingStore{})
	if got := d.activeColor(ctx, "never-deployed"); got != "" {
		t.Fatalf("activeColor = %q, want \"\"", got)
	}
}
