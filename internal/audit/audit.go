// Package audit records an append-only trail of who did what. Writes are
// best-effort: a failed audit insert is logged and never fails the user action.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	q *sqlc.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: sqlc.New(pool)}
}

// Record writes one audit entry. actorID/orgID may be zero (NULL). detail is
// serialized to JSONB; nil becomes {}. Never returns an error — failures are
// logged so the caller (a request handler) is never disrupted.
func (s *Service) Record(ctx context.Context, actorID, orgID pgtype.UUID, action, targetType, targetID string, detail map[string]any) {
	if s == nil {
		return
	}
	raw := []byte("{}")
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			raw = b
		}
	}
	// Detach from the request context so a client disconnect can't drop the
	// record mid-write.
	if err := s.q.InsertAuditLog(context.WithoutCancel(ctx), sqlc.InsertAuditLogParams{
		ActorID:    actorID,
		OrgID:      orgID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Detail:     raw,
	}); err != nil {
		slog.Warn("audit write failed", "action", action, "err", err)
	}
}

// ListAll returns the newest entries across the instance (instance admin).
func (s *Service) ListAll(ctx context.Context, limit int32) ([]sqlc.ListAuditAllRow, error) {
	return s.q.ListAuditAll(ctx, limit)
}

// ListByOrg returns the newest entries scoped to one org (org admin).
func (s *Service) ListByOrg(ctx context.Context, orgID pgtype.UUID, limit int32) ([]sqlc.ListAuditByOrgRow, error) {
	return s.q.ListAuditByOrg(ctx, sqlc.ListAuditByOrgParams{OrgID: orgID, Limit: limit})
}

// Purge deletes entries older than cutoff, returning the count removed.
func (s *Service) Purge(ctx context.Context, cutoff pgtype.Timestamptz) (int64, error) {
	return s.q.PurgeAuditLog(ctx, cutoff)
}
