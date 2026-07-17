package auth

import (
	"context"

	"github.com/bograh/cargo/internal/db/sqlc"
)

// Provider authenticates a user from credentials. Email/password today;
// OIDC later implements the same seam (FR-1.5).
type Provider interface {
	Authenticate(ctx context.Context, email, password string) (sqlc.User, error)
}

type passwordProvider struct {
	q *sqlc.Queries
}

func (p *passwordProvider) Authenticate(ctx context.Context, email, password string) (sqlc.User, error) {
	u, err := p.q.GetUserByEmail(ctx, email)
	if err != nil {
		// Burn comparable time so unknown emails aren't distinguishable by latency.
		_, _ = VerifyPassword("$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return sqlc.User{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(u.PasswordHash, password)
	if err != nil || !ok {
		return sqlc.User{}, ErrInvalidCredentials
	}
	return u, nil
}
