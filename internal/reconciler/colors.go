package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
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

// activeColor is the best-effort read used to address a running app (logs,
// stats, stop/start). A store failure reads as "no color"; a deploy must use
// lookupActiveColor instead, which reports it.
func (d *Docker) activeColor(ctx context.Context, appID string) string {
	c, _ := d.lookupActiveColor(ctx, appID)
	return c
}

// lookupActiveColor returns the serving color, or an error when the store
// cannot be read. Deploys must not guess here: reading a DB failure as "no
// color" made blue/green pick blue as the *next* color and tear blue down —
// the very color that was live.
func (d *Docker) lookupActiveColor(ctx context.Context, appID string) (string, error) {
	if d.colors != nil {
		c, err := d.colors.ActiveColor(ctx, appID)
		if err != nil {
			return "", fmt.Errorf("read active color: %w", err)
		}
		return c, nil
	}
	raw, err := os.ReadFile(d.statePath(appID))
	if err != nil {
		return "", nil
	}
	var st appState
	if err := json.Unmarshal(raw, &st); err != nil {
		return "", nil
	}
	return st.Color, nil
}

func (d *Docker) setActiveColor(ctx context.Context, appID, color string) error {
	if color == "" && d.colors == nil {
		// Back to the uncolored layout: no state file at all.
		if err := os.Remove(d.statePath(appID)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
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
