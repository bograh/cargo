package hostmgr

import (
	"context"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoHost means the app targets no worker host (control-plane deploy).
var ErrNoHost = errors.New("app is not assigned to a worker host")

// SetPool gives the Manager DB access for host resolution. Optional: only
// Resolve needs it.
func (m *Manager) SetPool(pool *pgxpool.Pool) { m.pool = pool }

// Resolve maps an app to its worker host's materialized execution
// environment. A control-plane app yields (nil, "", nil). For a hosted app
// it returns the target plus the host's recorded health status so callers
// can fail fast before touching a dead machine.
func (m *Manager) Resolve(ctx context.Context, appID string) (*reconciler.Target, string, error) {
	if m.pool == nil {
		return nil, "", errors.New("hostmgr: Resolve requires a pool")
	}
	var hostID *string
	if err := m.pool.QueryRow(ctx,
		`SELECT host_id::text FROM applications WHERE id = $1::uuid`, appID).Scan(&hostID); err != nil {
		return nil, "", fmt.Errorf("load app host: %w", err)
	}
	if hostID == nil || *hostID == "" {
		return nil, "", nil
	}
	h, err := m.hostSvc.GetByID(ctx, *hostID)
	if err != nil {
		return &reconciler.Target{HostID: *hostID}, "", fmt.Errorf("load host %s: %w", *hostID, err)
	}
	tg, err := m.mat.Target(ctx, h)
	if err != nil {
		return &reconciler.Target{HostID: *hostID}, h.Status, err
	}
	return &tg, h.Status, nil
}
