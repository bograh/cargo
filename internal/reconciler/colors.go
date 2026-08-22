package reconciler

import (
	"context"
	"encoding/json"
	"os"
)

// ColorStore records which deployment color currently serves an app.
//
// Historically this lived in <dataDir>/apps/<id>/state.json because it
// described "what is actually running on this host" and a restored control-
// plane backup must not disagree with the containers on disk. With multiple
// hosts there is no single disk it can live on, so the authoritative store is
// now Postgres (applications.active_color), written by the reconciler inside
// the advisory-locked deploy transaction. That satisfies the original
// invariant more strongly than the file ever did: a restored backup IS the
// state every host agrees on, because hosts are driven through it.
//
// A nil Colors field keeps the legacy on-disk behaviour, which standalone
// reconciler tests rely on.
type ColorStore interface {
	ActiveColor(ctx context.Context, appID string) (string, error)
	SetActiveColor(ctx context.Context, appID, color string) error
}

// SetColors wires the durable store. Call once at startup, before any deploy.
func (d *Docker) SetColors(cs ColorStore) { d.colors = cs }

// appState is the legacy on-disk shape of the active color.
type appState struct {
	Color string `json:"color"`
}

func (d *Docker) activeColor(ctx context.Context, appID string) string {
	if d.colors != nil {
		if c, err := d.colors.ActiveColor(ctx, appID); err == nil {
			return c
		}
		// The store failing means the database is unreachable; the deploy
		// pipeline will fail on its own next query. Report "no color" rather
		// than silently reading a possibly-stale file for another host.
		return ""
	}
	raw, err := os.ReadFile(d.statePath(appID))
	if err != nil {
		return ""
	}
	var st appState
	if err := json.Unmarshal(raw, &st); err != nil {
		return ""
	}
	return st.Color
}

func (d *Docker) setActiveColor(ctx context.Context, appID, color string) error {
	if d.colors != nil {
		if err := d.colors.SetActiveColor(ctx, appID, color); err != nil {
			return err
		}
		_ = os.Remove(d.statePath(appID)) // clear any pre-migration file
		return nil
	}
	raw, err := json.Marshal(appState{Color: color})
	if err != nil {
		return err
	}
	return os.WriteFile(d.statePath(appID), raw, 0o644)
}
