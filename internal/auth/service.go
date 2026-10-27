package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	accessTTL   = 15 * time.Minute
	refreshTTL  = 30 * 24 * time.Hour
	minPassword = 10
	// refreshReuseGrace is how long after a rotation the old refresh token is
	// still honoured. A browser that fires several requests at once after its
	// access token lapses sends the same refresh cookie on each; without this
	// window the second one would look like theft and revoke the session the
	// first one just issued.
	refreshReuseGrace = 10 * time.Second
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email already registered")
	ErrWeakPassword       = fmt.Errorf("password must be at least %d characters", minPassword)
	ErrUnauthenticated    = errors.New("unauthenticated")
)

type Tokens struct {
	Access           string
	AccessExpiresAt  time.Time
	Refresh          string
	RefreshExpiresAt time.Time
}

type Service struct {
	q        *sqlc.Queries
	pool     *pgxpool.Pool
	provider Provider
	// reuseGrace is refreshReuseGrace; a field so tests can exercise theft
	// detection without sleeping.
	reuseGrace time.Duration
}

func NewService(pool *pgxpool.Pool) *Service {
	q := sqlc.New(pool)
	return &Service{q: q, pool: pool, provider: &passwordProvider{q: q}, reuseGrace: refreshReuseGrace}
}

func (s *Service) Register(ctx context.Context, email, password string) (sqlc.User, Tokens, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(password) < minPassword {
		return sqlc.User{}, Tokens{}, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return sqlc.User{}, Tokens{}, err
	}
	u, err := s.createUser(ctx, email, hash)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return sqlc.User{}, Tokens{}, ErrEmailTaken
		}
		return sqlc.User{}, Tokens{}, err
	}
	tok, err := s.newSession(ctx, u.ID)
	return u, tok, err
}

// createUser inserts under the user-creation lock so at most one account can
// ever be made instance admin by the bootstrap rule.
func (s *Service) createUser(ctx context.Context, email, hash string) (sqlc.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sqlc.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)
	if err := q.LockUserCreation(ctx); err != nil {
		return sqlc.User{}, err
	}
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: email, PasswordHash: pgtype.Text{String: hash, Valid: true}})
	if err != nil {
		return sqlc.User{}, err
	}
	return u, tx.Commit(ctx)
}

func (s *Service) Login(ctx context.Context, email, password string) (sqlc.User, Tokens, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.provider.Authenticate(ctx, email, password)
	if err != nil {
		return sqlc.User{}, Tokens{}, err
	}
	tok, err := s.newSession(ctx, u.ID)
	return u, tok, err
}

// Refresh rotates the refresh token. Presenting an already-rotated or revoked
// token is treated as theft: the whole session family is revoked (FR-1.2) —
// except for a token rotated within refreshReuseGrace, which is a concurrent
// refresh from the same client and gets a sibling session in the family.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	sess, err := s.q.GetSessionByRefreshHash(ctx, hashToken(refreshToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrUnauthenticated
	}
	if err != nil {
		return Tokens{}, err
	}
	if sess.RevokedAt.Valid {
		_ = s.q.RevokeSessionFamily(ctx, sess.FamilyID)
		return Tokens{}, ErrUnauthenticated
	}
	if time.Now().After(sess.RefreshExpiresAt.Time) {
		return Tokens{}, ErrUnauthenticated
	}
	if !sess.RotatedAt.Valid {
		n, err := s.q.ClaimSessionRotation(ctx, sess.ID)
		if err != nil {
			return Tokens{}, err
		}
		if n == 1 {
			return s.issueSession(ctx, sess.UserID, sess.FamilyID)
		}
		// Lost the race to a concurrent refresh: re-read to see what it did.
		if sess, err = s.q.GetSessionByRefreshHash(ctx, hashToken(refreshToken)); err != nil {
			return Tokens{}, err
		}
		if sess.RevokedAt.Valid || !sess.RotatedAt.Valid {
			return Tokens{}, ErrUnauthenticated
		}
	}
	if time.Since(sess.RotatedAt.Time) <= s.reuseGrace {
		return s.issueSession(ctx, sess.UserID, sess.FamilyID)
	}
	_ = s.q.RevokeSessionFamily(ctx, sess.FamilyID)
	return Tokens{}, ErrUnauthenticated
}

// Logout revokes the session family identified by either cookie. The access
// cookie lapses after 15 minutes, so the refresh token is what still names the
// session for a user who logs out after being idle.
func (s *Service) Logout(ctx context.Context, accessToken, refreshToken string) error {
	var errs []error
	if accessToken != "" {
		sess, err := s.q.GetSessionByAccessHash(ctx, hashToken(accessToken))
		if err == nil {
			errs = append(errs, s.q.RevokeSessionFamily(ctx, sess.FamilyID))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			errs = append(errs, err)
		}
	}
	if refreshToken != "" {
		sess, err := s.q.GetSessionByRefreshHash(ctx, hashToken(refreshToken))
		if err == nil {
			errs = append(errs, s.q.RevokeSessionFamily(ctx, sess.FamilyID))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) UserForAccessToken(ctx context.Context, accessToken string) (sqlc.User, error) {
	sess, err := s.q.GetSessionByAccessHash(ctx, hashToken(accessToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, ErrUnauthenticated
	}
	if err != nil {
		return sqlc.User{}, err
	}
	if sess.RevokedAt.Valid || time.Now().After(sess.AccessExpiresAt.Time) {
		return sqlc.User{}, ErrUnauthenticated
	}
	u, err := s.q.GetUserByID(ctx, sess.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, ErrUnauthenticated
	}
	return u, err
}

// IssueSession creates a fresh session for an already-authenticated user —
// the exact path Login uses. Callers (OIDC) must have verified identity first.
func (s *Service) IssueSession(ctx context.Context, userID pgtype.UUID) (Tokens, error) {
	return s.newSession(ctx, userID)
}

func (s *Service) newSession(ctx context.Context, userID pgtype.UUID) (Tokens, error) {
	var family pgtype.UUID
	if err := family.Scan(uuid.NewString()); err != nil {
		return Tokens{}, err
	}
	return s.issueSession(ctx, userID, family)
}

func (s *Service) issueSession(ctx context.Context, userID, familyID pgtype.UUID) (Tokens, error) {
	access, accessHash, err := newToken()
	if err != nil {
		return Tokens{}, err
	}
	refresh, refreshHash, err := newToken()
	if err != nil {
		return Tokens{}, err
	}
	now := time.Now()
	tok := Tokens{
		Access:           access,
		AccessExpiresAt:  now.Add(accessTTL),
		Refresh:          refresh,
		RefreshExpiresAt: now.Add(refreshTTL),
	}
	_, err = s.q.CreateSession(ctx, sqlc.CreateSessionParams{
		UserID:           userID,
		FamilyID:         familyID,
		AccessHash:       accessHash,
		RefreshHash:      refreshHash,
		AccessExpiresAt:  pgtype.Timestamptz{Time: tok.AccessExpiresAt, Valid: true},
		RefreshExpiresAt: pgtype.Timestamptz{Time: tok.RefreshExpiresAt, Valid: true},
	})
	return tok, err
}
