package jobs

import (
	"context"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func parseID(appID string) (pgtype.UUID, error) {
	u, err := uuid.Parse(appID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

// DBColorStore implements reconciler.ColorStore over
// applications.active_color — the durable, multi-host home for blue/green
// state (Phase 12a). The reconciler writes it inside the advisory-locked
// deploy transaction, so every host agrees on what is serving.
type DBColorStore struct {
	Q *sqlc.Queries
}

func (s DBColorStore) ActiveColor(ctx context.Context, appID string) (string, error) {
	id, err := parseID(appID)
	if err != nil {
		return "", nil // never deployed through this table: empty color
	}
	c, err := s.Q.GetActiveColor(ctx, id)
	if err != nil {
		return "", err
	}
	return c.String, nil
}

func (s DBColorStore) SetActiveColor(ctx context.Context, appID, color string) error {
	id, err := parseID(appID)
	if err != nil {
		return nil
	}
	return s.Q.SetActiveColor(ctx, sqlc.SetActiveColorParams{ID: id, ActiveColor: pgtype.Text{String: color, Valid: true}})
}
