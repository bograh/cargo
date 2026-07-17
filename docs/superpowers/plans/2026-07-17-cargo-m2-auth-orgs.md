# Cargo M2 — Auth & Organizations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement FR-1 (email/password auth with argon2id, cookie sessions, refresh rotation + reuse detection, first-user-is-admin, rate limiting, `AuthProvider` seam) and FR-2 (organizations, roles, invite links, query scoping, instance-admin listing) as REST endpoints under `/api/v1`.

**Architecture:** New `internal/auth` package (hashing, tokens, session service behind `Provider` interface) and `internal/orgs` package (org/membership/invite service), wired into the existing chi `Server` in `internal/api`. All state in Postgres via goose migration `00003` + sqlc queries. Cookie pair: short-lived access token + long-lived refresh token, both random 256-bit values stored hashed (SHA-256); rotation tracked per session family for reuse detection.

**Tech Stack:** Go 1.25, chi v5, pgx v5, sqlc, goose, `golang.org/x/crypto/argon2`, `golang.org/x/time/rate`, testcontainers-go for DB tests.

## Global Constraints

- Module path: `github.com/bograh/cargo`
- Error responses always use the existing envelope via `api.Error(w, status, code, message)` (`internal/api/errors.go`)
- JSON success responses via existing `writeJSON` helper
- Roles are exactly: `owner`, `admin`, `member`, `viewer` (FR-2.2)
- All org-scoped queries filter by membership in SQL/service layer, never UI-only (FR-2.4)
- Cookies: `HttpOnly`, `SameSite=Lax`, `Secure` when `cfg.Env == "production"`
- Access token TTL 15 min; refresh token TTL 30 days
- Run `gofmt` on all files; CI runs golangci-lint (errcheck is enforced — check or `_ =` every error)
- sqlc regeneration command: `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate`
- DB integration tests follow the existing `startPostgres(t)` testcontainers pattern (`internal/db/db_test.go`)

---

### Task 1: Schema migration + sqlc queries (users, sessions, orgs, memberships, invites)

**Files:**
- Create: `internal/db/migrations/00003_auth_orgs.sql`
- Create: `internal/db/queries/users.sql`
- Create: `internal/db/queries/sessions.sql`
- Create: `internal/db/queries/orgs.sql`
- Create: `internal/db/queries/invites.sql`
- Generated: `internal/db/sqlc/*.sql.go`, `internal/db/sqlc/models.go` (via sqlc)
- Test: `internal/db/auth_orgs_test.go`

**Interfaces:**
- Produces: sqlc-generated `Queries` methods used by Tasks 3–8: `CreateUser`, `GetUserByEmail`, `GetUserByID`, `ListUsers`, `CreateSession`, `GetSessionByAccessHash`, `GetSessionByRefreshHash`, `MarkSessionRotated`, `RevokeSessionFamily`, `CreateOrganization`, `GetOrganizationByID`, `DeleteOrganization`, `ListOrganizationsForUser`, `ListAllOrganizations`, `CreateMembership`, `GetMembership`, `ListMembers`, `UpdateMembershipRole`, `DeleteMembership`, `CountOwners`, `CreateInvite`, `GetInviteByTokenHash`, `ListInvitesForOrg`, `RevokeInvite`

- [ ] **Step 1: Write the failing test**

`internal/db/auth_orgs_test.go`:

```go
package db

import (
	"context"
	"testing"
)

func TestAuthOrgsTablesExist(t *testing.T) {
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

	for _, table := range []string{"users", "sessions", "organizations", "memberships", "invites"} {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s missing after migrate", table)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/ -run TestAuthOrgsTablesExist -v`
Expected: FAIL with `table users missing after migrate`

- [ ] **Step 3: Write the migration**

`internal/db/migrations/00003_auth_orgs.sql`:

```sql
-- +goose Up
CREATE TABLE users (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email             TEXT NOT NULL UNIQUE,
    password_hash     TEXT NOT NULL,
    is_instance_admin BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id          UUID NOT NULL,
    access_hash        BYTEA NOT NULL UNIQUE,
    refresh_hash       BYTEA NOT NULL UNIQUE,
    access_expires_at  TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    rotated_at         TIMESTAMPTZ,
    revoked_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_family_idx ON sessions (family_id);

CREATE TABLE organizations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE TABLE invites (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE invites;
DROP TABLE memberships;
DROP TABLE organizations;
DROP TABLE sessions;
DROP TABLE users;
```

- [ ] **Step 4: Write the queries**

`internal/db/queries/users.sql`:

```sql
-- name: CreateUser :one
INSERT INTO users (email, password_hash, is_instance_admin)
VALUES ($1, $2, NOT EXISTS (SELECT 1 FROM users))
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at;
```

`internal/db/queries/sessions.sql`:

```sql
-- name: CreateSession :one
INSERT INTO sessions (user_id, family_id, access_hash, refresh_hash, access_expires_at, refresh_expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByAccessHash :one
SELECT * FROM sessions WHERE access_hash = $1;

-- name: GetSessionByRefreshHash :one
SELECT * FROM sessions WHERE refresh_hash = $1;

-- name: MarkSessionRotated :exec
UPDATE sessions SET rotated_at = now() WHERE id = $1;

-- name: RevokeSessionFamily :exec
UPDATE sessions SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL;
```

`internal/db/queries/orgs.sql`:

```sql
-- name: CreateOrganization :one
INSERT INTO organizations (name, slug) VALUES ($1, $2) RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = $1;

-- name: ListOrganizationsForUser :many
SELECT o.*, m.role FROM organizations o
JOIN memberships m ON m.org_id = o.id
WHERE m.user_id = $1
ORDER BY o.created_at;

-- name: ListAllOrganizations :many
SELECT * FROM organizations ORDER BY created_at;

-- name: CreateMembership :one
INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3) RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: ListMembers :many
SELECT m.org_id, m.user_id, m.role, m.created_at, u.email
FROM memberships m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 ORDER BY m.created_at;

-- name: UpdateMembershipRole :one
UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2 RETURNING *;

-- name: DeleteMembership :exec
DELETE FROM memberships WHERE org_id = $1 AND user_id = $2;

-- name: CountOwners :one
SELECT COUNT(*) FROM memberships WHERE org_id = $1 AND role = 'owner';
```

`internal/db/queries/invites.sql`:

```sql
-- name: CreateInvite :one
INSERT INTO invites (org_id, token_hash, role, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: ListInvitesForOrg :many
SELECT * FROM invites WHERE org_id = $1 AND revoked_at IS NULL ORDER BY created_at;

-- name: RevokeInvite :exec
UPDATE invites SET revoked_at = now() WHERE id = $1 AND org_id = $2;
```

- [ ] **Step 5: Regenerate sqlc**

Run: `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate`
Expected: exits 0; new files under `internal/db/sqlc/`

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/db/ -run TestAuthOrgsTablesExist -v` then `go build ./...`
Expected: PASS, build clean

- [ ] **Step 7: Commit**

```bash
git add internal/db
git commit -m "feat: add auth and org schema with sqlc queries"
```

---

### Task 2: Password hashing (argon2id)

**Files:**
- Create: `internal/auth/password.go`
- Test: `internal/auth/password_test.go`

**Interfaces:**
- Produces: `auth.HashPassword(password string) (string, error)`; `auth.VerifyPassword(hash, password string) (bool, error)` — hash is PHC string `$argon2id$v=19$m=65536,t=3,p=2$<b64salt>$<b64key>`

- [ ] **Step 1: Write the failing test**

`internal/auth/password_test.go`:

```go
package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("s3cret-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash format = %q", hash)
	}
	ok, err := VerifyPassword(hash, "s3cret-password")
	if err != nil || !ok {
		t.Fatalf("verify correct = %v, %v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil || ok {
		t.Fatalf("verify wrong = %v, %v", ok, err)
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	if _, err := VerifyPassword("not-a-hash", "x"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/ -v`
Expected: FAIL to build — `HashPassword` undefined

- [ ] **Step 3: Write the implementation**

`internal/auth/password.go`:

```go
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword hashes with argon2id and returns a PHC-format string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC argon2id hash in constant time.
func VerifyPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("malformed argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("malformed version: %w", err)
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("malformed params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("malformed salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("malformed key: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
```

Then: `go get golang.org/x/crypto@latest`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/ -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/auth go.mod go.sum
git commit -m "feat: add argon2id password hashing"
```

---

### Task 3: Session service — register/login/refresh with rotation + reuse detection

**Files:**
- Create: `internal/auth/token.go`
- Create: `internal/auth/service.go`
- Create: `internal/auth/provider.go`
- Test: `internal/auth/service_test.go` (integration, testcontainers)
- Test helper: `internal/auth/testdb_test.go`

**Interfaces:**
- Consumes: sqlc `Queries` from Task 1; `HashPassword`/`VerifyPassword` from Task 2
- Produces (used by Task 4 handlers):
  - `auth.Tokens{Access string; AccessExpiresAt time.Time; Refresh string; RefreshExpiresAt time.Time}`
  - `auth.Service` with methods:
    - `Register(ctx, email, password string) (sqlc.User, Tokens, error)`
    - `Login(ctx, email, password string) (sqlc.User, Tokens, error)`
    - `Refresh(ctx, refreshToken string) (Tokens, error)`
    - `Logout(ctx, accessToken string) error`
    - `UserForAccessToken(ctx, accessToken string) (sqlc.User, error)`
  - `auth.NewService(pool *pgxpool.Pool) *Service`
  - Sentinel errors: `auth.ErrInvalidCredentials`, `auth.ErrEmailTaken`, `auth.ErrUnauthenticated`, `auth.ErrWeakPassword`
  - `auth.Provider` interface: `Authenticate(ctx context.Context, email, password string) (sqlc.User, error)` (FR-1.5 seam; `passwordProvider` is the v1 implementation)

- [ ] **Step 1: Write the test helper**

`internal/auth/testdb_test.go`:

```go
package auth

import (
	"context"
	"testing"

	"github.com/bograh/cargo/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
```

- [ ] **Step 2: Write the failing tests**

`internal/auth/service_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRegisterFirstUserIsInstanceAdmin(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()

	u1, tok, err := svc.Register(ctx, "First@Example.com", "password-123")
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if !u1.IsInstanceAdmin {
		t.Fatal("first user should be instance admin")
	}
	if u1.Email != "first@example.com" {
		t.Fatalf("email not lowercased: %q", u1.Email)
	}
	if tok.Access == "" || tok.Refresh == "" {
		t.Fatal("tokens missing")
	}

	u2, _, err := svc.Register(ctx, "second@example.com", "password-123")
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if u2.IsInstanceAdmin {
		t.Fatal("second user must not be instance admin")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	if _, _, err := svc.Register(ctx, "a@b.co", "password-123"); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _, err := svc.Register(ctx, "a@b.co", "password-456")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}

func TestRegisterWeakPassword(t *testing.T) {
	svc := NewService(startPool(t))
	_, _, err := svc.Register(context.Background(), "a@b.co", "short")
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("err = %v, want ErrWeakPassword", err)
	}
}

func TestLoginAndAccessToken(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	if _, _, err := svc.Register(ctx, "a@b.co", "password-123"); err != nil {
		t.Fatalf("register: %v", err)
	}

	u, tok, err := svc.Login(ctx, "A@B.CO", "password-123")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	got, err := svc.UserForAccessToken(ctx, tok.Access)
	if err != nil {
		t.Fatalf("UserForAccessToken: %v", err)
	}
	if got.ID != u.ID {
		t.Fatal("access token resolved to wrong user")
	}

	if _, _, err := svc.Login(ctx, "a@b.co", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if _, _, err := svc.Login(ctx, "nobody@b.co", "password-123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown email err = %v", err)
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	_, tok1, err := svc.Register(ctx, "a@b.co", "password-123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	tok2, err := svc.Refresh(ctx, tok1.Refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok2.Refresh == tok1.Refresh || tok2.Access == tok1.Access {
		t.Fatal("tokens not rotated")
	}

	// Reusing the already-rotated refresh token must fail AND revoke the family.
	if _, err := svc.Refresh(ctx, tok1.Refresh); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("reuse err = %v, want ErrUnauthenticated", err)
	}
	if _, err := svc.Refresh(ctx, tok2.Refresh); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("family not revoked after reuse: %v", err)
	}
	if _, err := svc.UserForAccessToken(ctx, tok2.Access); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("access token usable after family revoke: %v", err)
	}
}

func TestLogoutRevokesFamily(t *testing.T) {
	svc := NewService(startPool(t))
	ctx := context.Background()
	_, tok, err := svc.Register(ctx, "a@b.co", "password-123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.Logout(ctx, tok.Access); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.UserForAccessToken(ctx, tok.Access); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("access token usable after logout: %v", err)
	}
	if _, err := svc.Refresh(ctx, tok.Refresh); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("refresh usable after logout: %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/auth/ -v`
Expected: FAIL to build — `NewService` undefined

- [ ] **Step 4: Write token helpers**

`internal/auth/token.go`:

```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// newToken returns a URL-safe random token and its SHA-256 hash for storage.
func newToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
```

- [ ] **Step 5: Write the provider seam**

`internal/auth/provider.go`:

```go
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
```

- [ ] **Step 6: Write the service**

`internal/auth/service.go`:

```go
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
	provider Provider
}

func NewService(pool *pgxpool.Pool) *Service {
	q := sqlc.New(pool)
	return &Service{q: q, provider: &passwordProvider{q: q}}
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
	u, err := s.q.CreateUser(ctx, sqlc.CreateUserParams{Email: email, PasswordHash: hash})
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
// token is treated as theft: the whole session family is revoked (FR-1.2).
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	sess, err := s.q.GetSessionByRefreshHash(ctx, hashToken(refreshToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrUnauthenticated
	}
	if err != nil {
		return Tokens{}, err
	}
	if sess.RotatedAt.Valid || sess.RevokedAt.Valid {
		_ = s.q.RevokeSessionFamily(ctx, sess.FamilyID)
		return Tokens{}, ErrUnauthenticated
	}
	if time.Now().After(sess.RefreshExpiresAt.Time) {
		return Tokens{}, ErrUnauthenticated
	}
	if err := s.q.MarkSessionRotated(ctx, sess.ID); err != nil {
		return Tokens{}, err
	}
	return s.issueSession(ctx, sess.UserID, sess.FamilyID)
}

func (s *Service) Logout(ctx context.Context, accessToken string) error {
	sess, err := s.q.GetSessionByAccessHash(ctx, hashToken(accessToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.q.RevokeSessionFamily(ctx, sess.FamilyID)
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

func (s *Service) newSession(ctx context.Context, userID pgtype.UUID) (Tokens, error) {
	var family pgtype.UUID
	_ = family.Scan(uuid.NewString())
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
```

Then: `go get github.com/google/uuid`

Note: sqlc may generate `time.Time`/`uuid.UUID` types instead of `pgtype` ones depending on config — after Task 1's generation, check `internal/db/sqlc/models.go` and adjust field wrapping in this file to match the generated types exactly.

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/auth/ -v`
Expected: PASS (all 6 tests; testcontainers pulls postgres:16-alpine)

- [ ] **Step 8: Commit**

```bash
git add internal/auth go.mod go.sum
git commit -m "feat: add session service with refresh rotation and reuse detection"
```

---

### Task 4: Auth HTTP endpoints + cookies + auth middleware + rate limiting

**Files:**
- Create: `internal/api/auth.go`
- Create: `internal/api/middleware.go`
- Create: `internal/api/ratelimit.go`
- Modify: `internal/api/server.go` (add `auth` field)
- Modify: `internal/api/router.go` (mount routes)
- Modify: `cmd/server/main.go` (construct auth service)
- Test: `internal/api/auth_test.go`

**Interfaces:**
- Consumes: `auth.Service` methods from Task 3
- Produces (used by Tasks 5–7):
  - `Server.auth AuthService` interface field: `Register`, `Login`, `Refresh`, `Logout`, `UserForAccessToken` (same signatures as `auth.Service`)
  - `s.requireAuth` chi middleware → puts `sqlc.User` into context; `userFrom(ctx) sqlc.User`
  - Cookie names: `cargo_access` (path `/`), `cargo_refresh` (path `/api/v1/auth`)
  - Routes: `POST /api/v1/auth/register|login|logout|refresh`, `GET /api/v1/auth/me`
  - `authRateLimiter() func(http.Handler) http.Handler` — per-IP, 10 req/min burst 10

- [ ] **Step 1: Write the failing tests**

`internal/api/auth_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/db/sqlc"
)

type stubAuth struct {
	user   sqlc.User
	tokens auth.Tokens
	err    error
}

func (s stubAuth) Register(_ context.Context, _, _ string) (sqlc.User, auth.Tokens, error) {
	return s.user, s.tokens, s.err
}
func (s stubAuth) Login(_ context.Context, _, _ string) (sqlc.User, auth.Tokens, error) {
	return s.user, s.tokens, s.err
}
func (s stubAuth) Refresh(_ context.Context, _ string) (auth.Tokens, error) {
	return s.tokens, s.err
}
func (s stubAuth) Logout(_ context.Context, _ string) error { return s.err }
func (s stubAuth) UserForAccessToken(_ context.Context, tok string) (sqlc.User, error) {
	if s.err != nil || tok == "" {
		return sqlc.User{}, auth.ErrUnauthenticated
	}
	return s.user, nil
}

func testTokens() auth.Tokens {
	return auth.Tokens{
		Access: "acc", AccessExpiresAt: time.Now().Add(15 * time.Minute),
		Refresh: "ref", RefreshExpiresAt: time.Now().Add(720 * time.Hour),
	}
}

func TestRegisterSetsCookies(t *testing.T) {
	s := &Server{auth: stubAuth{user: sqlc.User{Email: "a@b.co"}, tokens: testTokens()}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"email":"a@b.co","password":"password-123"}`))
	NewRouter(s).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	var names []string
	for _, c := range cookies {
		names = append(names, c.Name)
		if !c.HttpOnly {
			t.Fatalf("cookie %s not HttpOnly", c.Name)
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "cargo_access") || !strings.Contains(joined, "cargo_refresh") {
		t.Fatalf("cookies = %v", names)
	}
}

func TestRegisterValidation(t *testing.T) {
	s := &Server{auth: stubAuth{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register",
		strings.NewReader(`{"email":"","password":""}`))
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	s := &Server{auth: stubAuth{err: auth.ErrInvalidCredentials}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"email":"a@b.co","password":"wrong-password"}`))
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMeRequiresAuth(t *testing.T) {
	s := &Server{auth: stubAuth{err: auth.ErrUnauthenticated}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMeReturnsUser(t *testing.T) {
	s := &Server{auth: stubAuth{user: sqlc.User{Email: "a@b.co", IsInstanceAdmin: true}}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["email"] != "a@b.co" || body["is_instance_admin"] != true {
		t.Fatalf("body = %v", body)
	}
}

func TestAuthRateLimited(t *testing.T) {
	s := &Server{auth: stubAuth{err: auth.ErrInvalidCredentials}}
	r := NewRouter(s)
	var last int
	for i := 0; i < 15; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"email":"a@b.co","password":"wrong-password"}`))
		req.RemoteAddr = "10.9.9.9:1234"
		r.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("15th request status = %d, want 429", last)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -v`
Expected: FAIL to build — `Server` has no field `auth`

- [ ] **Step 3: Add rate limiter**

`internal/api/ratelimit.go`:

```go
package api

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// authRateLimiter returns per-IP limiting middleware for auth endpoints (FR-1.4).
func authRateLimiter() func(http.Handler) http.Handler {
	var mu sync.Mutex
	limiters := map[string]*rate.Limiter{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			mu.Lock()
			lim, ok := limiters[ip]
			if !ok {
				lim = rate.NewLimiter(rate.Limit(10.0/60.0), 10)
				limiters[ip] = lim
			}
			mu.Unlock()
			if !lim.Allow() {
				Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

Then: `go get golang.org/x/time`

- [ ] **Step 4: Add auth middleware**

`internal/api/middleware.go`:

```go
package api

import (
	"context"
	"net/http"

	"github.com/bograh/cargo/internal/db/sqlc"
)

type ctxKey int

const userKey ctxKey = 0

func userFrom(ctx context.Context) sqlc.User {
	u, _ := ctx.Value(userKey).(sqlc.User)
	return u
}

// requireAuth resolves the access cookie to a user or rejects with 401.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("cargo_access")
		if err != nil {
			Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		u, err := s.auth.UserForAccessToken(r.Context(), c.Value)
		if err != nil {
			Error(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

// requireInstanceAdmin must be nested inside requireAuth.
func (s *Server) requireInstanceAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !userFrom(r.Context()).IsInstanceAdmin {
			Error(w, http.StatusForbidden, "forbidden", "instance admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 5: Add handlers**

`internal/api/auth.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/db/sqlc"
)

type credentialsBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func userJSON(u sqlc.User) map[string]any {
	return map[string]any{
		"id":                u.ID,
		"email":             u.Email,
		"is_instance_admin": u.IsInstanceAdmin,
	}
}

func (s *Server) setAuthCookies(w http.ResponseWriter, tok auth.Tokens) {
	secure := s.cfg.Env == "production"
	http.SetCookie(w, &http.Cookie{
		Name: "cargo_access", Value: tok.Access, Path: "/",
		Expires: tok.AccessExpiresAt, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: "cargo_refresh", Value: tok.Refresh, Path: "/api/v1/auth",
		Expires: tok.RefreshExpiresAt, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "cargo_access", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "cargo_refresh", Value: "", Path: "/api/v1/auth", MaxAge: -1, HttpOnly: true})
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentialsBody, bool) {
	var body credentialsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return body, false
	}
	if body.Email == "" || body.Password == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "email and password are required")
		return body, false
	}
	return body, true
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	u, tok, err := s.auth.Register(r.Context(), body.Email, body.Password)
	switch {
	case errors.Is(err, auth.ErrEmailTaken):
		Error(w, http.StatusConflict, "email_taken", err.Error())
		return
	case errors.Is(err, auth.ErrWeakPassword):
		Error(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	case err != nil:
		Error(w, http.StatusInternalServerError, "internal", "registration failed")
		return
	}
	s.setAuthCookies(w, tok)
	writeJSON(w, http.StatusCreated, userJSON(u))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	u, tok, err := s.auth.Login(r.Context(), body.Email, body.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		Error(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	s.setAuthCookies(w, tok)
	writeJSON(w, http.StatusOK, userJSON(u))
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("cargo_refresh")
	if err != nil {
		Error(w, http.StatusUnauthorized, "unauthenticated", "refresh token missing")
		return
	}
	tok, err := s.auth.Refresh(r.Context(), c.Value)
	if err != nil {
		s.clearAuthCookies(w)
		Error(w, http.StatusUnauthorized, "unauthenticated", "session expired, log in again")
		return
	}
	s.setAuthCookies(w, tok)
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("cargo_access"); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	s.clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userJSON(userFrom(r.Context())))
}
```

- [ ] **Step 6: Wire Server and router**

In `internal/api/server.go`, add the interface and field:

```go
// AuthService is satisfied by *auth.Service.
type AuthService interface {
	Register(ctx context.Context, email, password string) (sqlc.User, auth.Tokens, error)
	Login(ctx context.Context, email, password string) (sqlc.User, auth.Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (auth.Tokens, error)
	Logout(ctx context.Context, accessToken string) error
	UserForAccessToken(ctx context.Context, accessToken string) (sqlc.User, error)
}
```

Add `auth AuthService` to the `Server` struct and in `NewServer` (inside the `if pool != nil` block) set `s.auth = auth.NewService(pool)`. Import `github.com/bograh/cargo/internal/auth`.

In `internal/api/router.go`, inside `r.Route("/api/v1", ...)` add:

```go
r.Route("/auth", func(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(authRateLimiter())
		r.Post("/register", s.handleRegister)
		r.Post("/login", s.handleLogin)
		r.Post("/refresh", s.handleRefresh)
	})
	r.Post("/logout", s.handleLogout)
	r.With(s.requireAuth).Get("/me", s.handleMe)
})
```

In `cmd/server/main.go`: no change needed (NewServer wires it), verify it still builds.

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/api/ -v && go build ./...`
Expected: PASS (all tests incl. existing instance/health tests)

- [ ] **Step 8: Commit**

```bash
git add internal/api cmd go.mod go.sum
git commit -m "feat: add auth endpoints with cookies, middleware, and rate limiting"
```

---

### Task 5: Organizations service + endpoints (create/list/get/delete, members)

**Files:**
- Create: `internal/orgs/service.go`
- Create: `internal/orgs/slug.go`
- Test: `internal/orgs/service_test.go` (integration; copy the `startPool` helper from `internal/auth/testdb_test.go` into `internal/orgs/testdb_test.go`)
- Create: `internal/api/orgs.go`
- Modify: `internal/api/server.go`, `internal/api/router.go`
- Test: `internal/api/orgs_test.go`

**Interfaces:**
- Consumes: sqlc queries (Task 1), `requireAuth`/`userFrom` (Task 4)
- Produces:
  - `orgs.NewService(pool *pgxpool.Pool) *Service`
  - `orgs.Service` methods (also the `api.OrgService` interface stub surface):
    - `Create(ctx, name string, creator pgtype.UUID) (sqlc.Organization, error)` — creator becomes owner (FR-2.1)
    - `ListForUser(ctx, userID pgtype.UUID) ([]sqlc.ListOrganizationsForUserRow, error)`
    - `Get(ctx, orgID, userID pgtype.UUID) (sqlc.Organization, string, error)` — returns org + caller's role; `ErrNotFound` for non-members (FR-2.4: invisible, not 403)
    - `Delete(ctx, orgID, userID pgtype.UUID) error` — owner only
    - `ListMembers(ctx, orgID, userID pgtype.UUID) ([]sqlc.ListMembersRow, error)`
    - `UpdateRole(ctx, orgID, actor, target pgtype.UUID, role string) (sqlc.Membership, error)`
    - `RemoveMember(ctx, orgID, actor, target pgtype.UUID) error`
  - Sentinel errors: `orgs.ErrNotFound`, `orgs.ErrForbidden`, `orgs.ErrLastOwner`, `orgs.ErrBadRole`
  - Role helper: `orgs.RoleAtLeast(role, min string) bool` with ranking viewer < member < admin < owner
  - Routes: `POST/GET /api/v1/orgs`, `GET/DELETE /api/v1/orgs/{orgID}`, `GET /api/v1/orgs/{orgID}/members`, `PATCH/DELETE /api/v1/orgs/{orgID}/members/{userID}`

- [ ] **Step 1: Write failing service tests**

`internal/orgs/service_test.go`:

```go
package orgs

import (
	"context"
	"errors"
	"testing"

	"github.com/bograh/cargo/internal/auth"
	"github.com/jackc/pgx/v5/pgtype"
)

func register(t *testing.T, svc *auth.Service, email string) pgtype.UUID {
	t.Helper()
	u, _, err := svc.Register(context.Background(), email, "password-123")
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return u.ID
}

func TestCreateOrgMakesCreatorOwner(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")

	org, err := svc.Create(ctx, "Acme Team", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if org.Slug != "acme-team" {
		t.Fatalf("slug = %q", org.Slug)
	}
	got, role, err := svc.Get(ctx, org.ID, owner)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != org.ID || role != "owner" {
		t.Fatalf("get = %v role %q", got, role)
	}
}

func TestOrgInvisibleToNonMembers(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	outsider := register(t, authSvc, "outsider@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := svc.Get(ctx, org.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider get err = %v, want ErrNotFound", err)
	}
	if _, err := svc.ListMembers(ctx, org.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider members err = %v", err)
	}
	list, err := svc.ListForUser(ctx, outsider)
	if err != nil || len(list) != 0 {
		t.Fatalf("outsider list = %v, %v", list, err)
	}
}

func TestSlugCollisionGetsSuffix(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")

	if _, err := svc.Create(ctx, "Acme", owner); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	org2, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if org2.Slug == "acme" || len(org2.Slug) <= len("acme") {
		t.Fatalf("collision slug = %q", org2.Slug)
	}
}

func TestDeleteRequiresOwner(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	member := register(t, authSvc, "member@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, member, "admin"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := svc.Delete(ctx, org.ID, member); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin delete err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(ctx, org.ID, owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestRoleChangesAndLastOwnerGuard(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	member := register(t, authSvc, "member@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, member, "viewer"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// member (viewer) cannot change roles
	if _, err := svc.UpdateRole(ctx, org.ID, member, member, "admin"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer update err = %v", err)
	}
	// owner promotes member
	m, err := svc.UpdateRole(ctx, org.ID, owner, member, "admin")
	if err != nil || m.Role != "admin" {
		t.Fatalf("promote = %v, %v", m, err)
	}
	// cannot demote the last owner
	if _, err := svc.UpdateRole(ctx, org.ID, owner, owner, "member"); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("demote last owner err = %v", err)
	}
	// cannot remove the last owner
	if err := svc.RemoveMember(ctx, org.ID, owner, owner); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("remove last owner err = %v", err)
	}
	// invalid role rejected
	if _, err := svc.UpdateRole(ctx, org.ID, owner, member, "superuser"); !errors.Is(err, ErrBadRole) {
		t.Fatalf("bad role err = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/orgs/ -v`
Expected: FAIL to build — package missing

- [ ] **Step 3: Write slug helper**

`internal/orgs/slug.go`:

```go
package orgs

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a display name into a DNS-safe slug.
func slugify(name string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 4: Write the service**

`internal/orgs/service.go`:

```go
package orgs

import (
	"context"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("organization not found")
	ErrForbidden = errors.New("insufficient role")
	ErrLastOwner = errors.New("organization must keep at least one owner")
	ErrBadRole   = errors.New("invalid role")
)

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}

// RoleAtLeast reports whether role meets the minimum required role.
func RoleAtLeast(role, min string) bool {
	r, ok := roleRank[role]
	m, ok2 := roleRank[min]
	return ok && ok2 && r >= m
}

func ValidRole(role string) bool { _, ok := roleRank[role]; return ok }

type Service struct {
	q *sqlc.Queries
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{q: sqlc.New(pool)} }

// membership returns the caller's membership or ErrNotFound (org is invisible
// to non-members — FR-2.4).
func (s *Service) membership(ctx context.Context, orgID, userID pgtype.UUID) (sqlc.Membership, error) {
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Membership{}, ErrNotFound
	}
	return m, err
}

func (s *Service) Create(ctx context.Context, name string, creator pgtype.UUID) (sqlc.Organization, error) {
	slug := slugify(name)
	org, err := s.q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{Name: name, Slug: slug})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		org, err = s.q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
			Name: name, Slug: fmt.Sprintf("%s-%s", slug, randomSuffix()),
		})
	}
	if err != nil {
		return sqlc.Organization{}, err
	}
	_, err = s.q.CreateMembership(ctx, sqlc.CreateMembershipParams{
		OrgID: org.ID, UserID: creator, Role: "owner",
	})
	if err != nil {
		return sqlc.Organization{}, err
	}
	return org, nil
}

func (s *Service) ListForUser(ctx context.Context, userID pgtype.UUID) ([]sqlc.ListOrganizationsForUserRow, error) {
	rows, err := s.q.ListOrganizationsForUser(ctx, userID)
	if rows == nil {
		rows = []sqlc.ListOrganizationsForUserRow{}
	}
	return rows, err
}

func (s *Service) Get(ctx context.Context, orgID, userID pgtype.UUID) (sqlc.Organization, string, error) {
	m, err := s.membership(ctx, orgID, userID)
	if err != nil {
		return sqlc.Organization{}, "", err
	}
	org, err := s.q.GetOrganizationByID(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Organization{}, "", ErrNotFound
	}
	return org, m.Role, err
}

func (s *Service) Delete(ctx context.Context, orgID, userID pgtype.UUID) error {
	m, err := s.membership(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if m.Role != "owner" {
		return ErrForbidden
	}
	return s.q.DeleteOrganization(ctx, orgID)
}

// AddMember inserts a membership directly (used by invite acceptance and tests).
func (s *Service) AddMember(ctx context.Context, orgID, userID pgtype.UUID, role string) error {
	if !ValidRole(role) {
		return ErrBadRole
	}
	_, err := s.q.CreateMembership(ctx, sqlc.CreateMembershipParams{OrgID: orgID, UserID: userID, Role: role})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil // already a member — accepting an invite twice is a no-op
	}
	return err
}

func (s *Service) ListMembers(ctx context.Context, orgID, userID pgtype.UUID) ([]sqlc.ListMembersRow, error) {
	if _, err := s.membership(ctx, orgID, userID); err != nil {
		return nil, err
	}
	rows, err := s.q.ListMembers(ctx, orgID)
	if rows == nil {
		rows = []sqlc.ListMembersRow{}
	}
	return rows, err
}

func (s *Service) UpdateRole(ctx context.Context, orgID, actor, target pgtype.UUID, role string) (sqlc.Membership, error) {
	if !ValidRole(role) {
		return sqlc.Membership{}, ErrBadRole
	}
	am, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return sqlc.Membership{}, err
	}
	if !RoleAtLeast(am.Role, "admin") {
		return sqlc.Membership{}, ErrForbidden
	}
	tm, err := s.membership(ctx, orgID, target)
	if err != nil {
		return sqlc.Membership{}, err
	}
	if tm.Role == "owner" && role != "owner" {
		if err := s.ensureNotLastOwner(ctx, orgID); err != nil {
			return sqlc.Membership{}, err
		}
	}
	return s.q.UpdateMembershipRole(ctx, sqlc.UpdateMembershipRoleParams{
		OrgID: orgID, UserID: target, Role: role,
	})
}

func (s *Service) RemoveMember(ctx context.Context, orgID, actor, target pgtype.UUID) error {
	am, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return err
	}
	// Admins can remove others; anyone can remove themselves (leave).
	if actor != target && !RoleAtLeast(am.Role, "admin") {
		return ErrForbidden
	}
	tm, err := s.membership(ctx, orgID, target)
	if err != nil {
		return err
	}
	if tm.Role == "owner" {
		if err := s.ensureNotLastOwner(ctx, orgID); err != nil {
			return err
		}
	}
	return s.q.DeleteMembership(ctx, sqlc.DeleteMembershipParams{OrgID: orgID, UserID: target})
}

func (s *Service) ensureNotLastOwner(ctx context.Context, orgID pgtype.UUID) error {
	n, err := s.q.CountOwners(ctx, orgID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastOwner
	}
	return nil
}
```

- [ ] **Step 5: Run service tests to verify they pass**

Run: `go test ./internal/orgs/ -v`
Expected: PASS (5 tests)

- [ ] **Step 6: Write failing API tests**

`internal/api/orgs_test.go` — stub the org service the same way `stubAuth` stubs auth:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/jackc/pgx/v5/pgtype"
)

type stubOrgs struct {
	org  sqlc.Organization
	role string
	err  error
}

func (s stubOrgs) Create(_ context.Context, _ string, _ pgtype.UUID) (sqlc.Organization, error) {
	return s.org, s.err
}
func (s stubOrgs) ListForUser(_ context.Context, _ pgtype.UUID) ([]sqlc.ListOrganizationsForUserRow, error) {
	return []sqlc.ListOrganizationsForUserRow{}, s.err
}
func (s stubOrgs) Get(_ context.Context, _, _ pgtype.UUID) (sqlc.Organization, string, error) {
	return s.org, s.role, s.err
}
func (s stubOrgs) Delete(_ context.Context, _, _ pgtype.UUID) error { return s.err }
func (s stubOrgs) AddMember(_ context.Context, _, _ pgtype.UUID, _ string) error {
	return s.err
}
func (s stubOrgs) ListMembers(_ context.Context, _, _ pgtype.UUID) ([]sqlc.ListMembersRow, error) {
	return []sqlc.ListMembersRow{}, s.err
}
func (s stubOrgs) UpdateRole(_ context.Context, _, _, _ pgtype.UUID, _ string) (sqlc.Membership, error) {
	return sqlc.Membership{}, s.err
}
func (s stubOrgs) RemoveMember(_ context.Context, _, _, _ pgtype.UUID) error { return s.err }

func authedServer(o OrgService) *Server {
	return &Server{
		auth: stubAuth{user: sqlc.User{Email: "a@b.co"}},
		orgs: o,
	}
}

func doAuthed(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	return rec
}

func TestCreateOrg(t *testing.T) {
	s := authedServer(stubOrgs{org: sqlc.Organization{Name: "Acme", Slug: "acme"}})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs", `{"name":"Acme"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["slug"] != "acme" {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateOrgRequiresName(t *testing.T) {
	s := authedServer(stubOrgs{})
	rec := doAuthed(t, s, http.MethodPost, "/api/v1/orgs", `{"name":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestOrgsRequireAuth(t *testing.T) {
	s := &Server{auth: stubAuth{err: orgs.ErrForbidden}, orgs: stubOrgs{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil)
	NewRouter(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetOrgNotFoundForNonMember(t *testing.T) {
	s := authedServer(stubOrgs{err: orgs.ErrNotFound})
	rec := doAuthed(t, s, http.MethodGet, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestDeleteOrgForbidden(t *testing.T) {
	s := authedServer(stubOrgs{err: orgs.ErrForbidden})
	rec := doAuthed(t, s, http.MethodDelete, "/api/v1/orgs/5f4c1c9e-0000-0000-0000-000000000000", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

- [ ] **Step 7: Write the API handlers**

`internal/api/orgs.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// orgIDParam parses the {orgID} URL param; writes 404 and returns false on bad UUIDs.
func orgIDParam(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, "orgID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "organization not found")
		return id, false
	}
	return id, true
}

// orgError maps service errors onto the API envelope.
func orgError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orgs.ErrNotFound):
		Error(w, http.StatusNotFound, "not_found", "organization not found")
	case errors.Is(err, orgs.ErrForbidden):
		Error(w, http.StatusForbidden, "forbidden", "insufficient role")
	case errors.Is(err, orgs.ErrLastOwner):
		Error(w, http.StatusConflict, "last_owner", "organization must keep at least one owner")
	case errors.Is(err, orgs.ErrBadRole):
		Error(w, http.StatusBadRequest, "invalid_role", "role must be owner, admin, member, or viewer")
	default:
		Error(w, http.StatusInternalServerError, "internal", "operation failed")
	}
}

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "name is required")
		return
	}
	org, err := s.orgs.Create(r.Context(), strings.TrimSpace(body.Name), userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	list, err := s.orgs.ListForUser(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetOrg(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	org, role, err := s.orgs.Get(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organization": org, "role": role})
}

func (s *Server) handleDeleteOrg(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	if err := s.orgs.Delete(r.Context(), id, userFrom(r.Context()).ID); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.orgs.ListMembers(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var target pgtype.UUID
	if err := target.Scan(chi.URLParam(r, "userID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	m, err := s.orgs.UpdateRole(r.Context(), id, userFrom(r.Context()).ID, target, body.Role)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var target pgtype.UUID
	if err := target.Scan(chi.URLParam(r, "userID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	if err := s.orgs.RemoveMember(r.Context(), id, userFrom(r.Context()).ID, target); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
```

In `internal/api/server.go` add the `OrgService` interface (mirroring the stub's method set above, satisfied by `*orgs.Service`), an `orgs OrgService` field, and wire `s.orgs = orgs.NewService(pool)` in `NewServer`.

In `internal/api/router.go` add inside `/api/v1`:

```go
r.Group(func(r chi.Router) {
	r.Use(s.requireAuth)
	r.Route("/orgs", func(r chi.Router) {
		r.Post("/", s.handleCreateOrg)
		r.Get("/", s.handleListOrgs)
		r.Route("/{orgID}", func(r chi.Router) {
			r.Get("/", s.handleGetOrg)
			r.Delete("/", s.handleDeleteOrg)
			r.Get("/members", s.handleListMembers)
			r.Patch("/members/{userID}", s.handleUpdateMemberRole)
			r.Delete("/members/{userID}", s.handleRemoveMember)
		})
	})
})
```

- [ ] **Step 8: Run all tests**

Run: `go test ./... && go build ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/orgs internal/api
git commit -m "feat: add organizations with roles and membership management"
```

---

### Task 6: Invites (create, list, revoke, accept)

**Files:**
- Create: `internal/orgs/invites.go`
- Test: extend `internal/orgs/invites_test.go`
- Create: `internal/api/invites.go`
- Modify: `internal/api/server.go` (extend `OrgService`), `internal/api/router.go`
- Test: extend `internal/api/orgs_test.go` stub with invite methods

**Interfaces:**
- Consumes: sqlc invite queries (Task 1), `auth.newToken` pattern — invites reuse `crypto/rand` + SHA-256 hashing (duplicate small helpers locally; do not export from auth)
- Produces (added to `orgs.Service` and the `api.OrgService` interface):
  - `CreateInvite(ctx, orgID, actor pgtype.UUID, role string, ttl time.Duration) (token string, inv sqlc.Invite, err error)` — actor must be admin+; returned `token` is shown once (link), only hash stored
  - `ListInvites(ctx, orgID, actor pgtype.UUID) ([]sqlc.Invite, error)` — admin+
  - `RevokeInvite(ctx, orgID, actor, inviteID pgtype.UUID) error` — admin+
  - `AcceptInvite(ctx, token string, userID pgtype.UUID) (sqlc.Organization, error)` — validates not revoked/expired, creates membership with invite's role
  - Sentinel error: `orgs.ErrInviteInvalid`
  - Routes: `POST/GET /api/v1/orgs/{orgID}/invites`, `DELETE /api/v1/orgs/{orgID}/invites/{inviteID}`, `POST /api/v1/invites/accept` (body `{"token":"..."}`)

- [ ] **Step 1: Write failing service tests**

`internal/orgs/invites_test.go`:

```go
package orgs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/auth"
)

func TestInviteLifecycle(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	joiner := register(t, authSvc, "joiner@x.co")

	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	token, inv, err := svc.CreateInvite(ctx, org.ID, owner, "member", 24*time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if token == "" || inv.Role != "member" {
		t.Fatalf("token=%q inv=%v", token, inv)
	}

	got, err := svc.AcceptInvite(ctx, token, joiner)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got.ID != org.ID {
		t.Fatal("accepted into wrong org")
	}
	_, role, err := svc.Get(ctx, org.ID, joiner)
	if err != nil || role != "member" {
		t.Fatalf("joiner role = %q, %v", role, err)
	}
	// Accepting again is a no-op, not an error.
	if _, err := svc.AcceptInvite(ctx, token, joiner); err != nil {
		t.Fatalf("re-accept: %v", err)
	}
}

func TestInviteRevokedAndExpired(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	joiner := register(t, authSvc, "joiner@x.co")
	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	token, inv, err := svc.CreateInvite(ctx, org.ID, owner, "member", 24*time.Hour)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := svc.RevokeInvite(ctx, org.ID, owner, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AcceptInvite(ctx, token, joiner); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("revoked accept err = %v", err)
	}

	expTok, _, err := svc.CreateInvite(ctx, org.ID, owner, "member", -time.Hour)
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if _, err := svc.AcceptInvite(ctx, expTok, joiner); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("expired accept err = %v", err)
	}
	if _, err := svc.AcceptInvite(ctx, "bogus-token", joiner); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("bogus accept err = %v", err)
	}
}

func TestInviteRequiresAdmin(t *testing.T) {
	pool := startPool(t)
	authSvc := auth.NewService(pool)
	svc := NewService(pool)
	ctx := context.Background()
	owner := register(t, authSvc, "owner@x.co")
	viewer := register(t, authSvc, "viewer@x.co")
	org, err := svc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, viewer, "viewer"); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	if _, _, err := svc.CreateInvite(ctx, org.ID, viewer, "member", time.Hour); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer invite err = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/orgs/ -run TestInvite -v`
Expected: FAIL to build — `CreateInvite` undefined

- [ ] **Step 3: Write the implementation**

`internal/orgs/invites.go`:

```go
package orgs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInviteInvalid = errors.New("invite is invalid, revoked, or expired")

func newInviteToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(tok))
	return tok, sum[:], nil
}

func (s *Service) CreateInvite(ctx context.Context, orgID, actor pgtype.UUID, role string, ttl time.Duration) (string, sqlc.Invite, error) {
	if !ValidRole(role) {
		return "", sqlc.Invite{}, ErrBadRole
	}
	m, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return "", sqlc.Invite{}, err
	}
	if !RoleAtLeast(m.Role, "admin") {
		return "", sqlc.Invite{}, ErrForbidden
	}
	token, hash, err := newInviteToken()
	if err != nil {
		return "", sqlc.Invite{}, err
	}
	inv, err := s.q.CreateInvite(ctx, sqlc.CreateInviteParams{
		OrgID:     orgID,
		TokenHash: hash,
		Role:      role,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
		CreatedBy: actor,
	})
	return token, inv, err
}

func (s *Service) ListInvites(ctx context.Context, orgID, actor pgtype.UUID) ([]sqlc.Invite, error) {
	m, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return nil, err
	}
	if !RoleAtLeast(m.Role, "admin") {
		return nil, ErrForbidden
	}
	rows, err := s.q.ListInvitesForOrg(ctx, orgID)
	if rows == nil {
		rows = []sqlc.Invite{}
	}
	return rows, err
}

func (s *Service) RevokeInvite(ctx context.Context, orgID, actor, inviteID pgtype.UUID) error {
	m, err := s.membership(ctx, orgID, actor)
	if err != nil {
		return err
	}
	if !RoleAtLeast(m.Role, "admin") {
		return ErrForbidden
	}
	return s.q.RevokeInvite(ctx, sqlc.RevokeInviteParams{ID: inviteID, OrgID: orgID})
}

func (s *Service) AcceptInvite(ctx context.Context, token string, userID pgtype.UUID) (sqlc.Organization, error) {
	sum := sha256.Sum256([]byte(token))
	inv, err := s.q.GetInviteByTokenHash(ctx, sum[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Organization{}, ErrInviteInvalid
	}
	if err != nil {
		return sqlc.Organization{}, err
	}
	if inv.RevokedAt.Valid || time.Now().After(inv.ExpiresAt.Time) {
		return sqlc.Organization{}, ErrInviteInvalid
	}
	if err := s.AddMember(ctx, inv.OrgID, userID, inv.Role); err != nil {
		return sqlc.Organization{}, err
	}
	return s.q.GetOrganizationByID(ctx, inv.OrgID)
}
```

- [ ] **Step 4: Run service tests**

Run: `go test ./internal/orgs/ -v`
Expected: PASS

- [ ] **Step 5: Add API handlers and routes**

`internal/api/invites.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const defaultInviteTTL = 7 * 24 * time.Hour

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	token, inv, err := s.orgs.CreateInvite(r.Context(), id, userFrom(r.Context()).ID, body.Role, defaultInviteTTL)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"invite": inv,
		"token":  token, // shown once; only the hash is stored
	})
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.orgs.ListInvites(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var inviteID pgtype.UUID
	if err := inviteID.Scan(chi.URLParam(r, "inviteID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}
	if err := s.orgs.RevokeInvite(r.Context(), id, userFrom(r.Context()).ID, inviteID); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "token is required")
		return
	}
	org, err := s.orgs.AcceptInvite(r.Context(), body.Token, userFrom(r.Context()).ID)
	if errors.Is(err, orgs.ErrInviteInvalid) {
		Error(w, http.StatusBadRequest, "invite_invalid", "invite is invalid, revoked, or expired")
		return
	}
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}
```

Extend the `OrgService` interface in `internal/api/server.go` with `CreateInvite`, `ListInvites`, `RevokeInvite`, `AcceptInvite` (signatures as in the Interfaces block). Extend `stubOrgs` in `internal/api/orgs_test.go` to satisfy them:

```go
func (s stubOrgs) CreateInvite(_ context.Context, _, _ pgtype.UUID, _ string, _ time.Duration) (string, sqlc.Invite, error) {
	return "tok", sqlc.Invite{}, s.err
}
func (s stubOrgs) ListInvites(_ context.Context, _, _ pgtype.UUID) ([]sqlc.Invite, error) {
	return []sqlc.Invite{}, s.err
}
func (s stubOrgs) RevokeInvite(_ context.Context, _, _, _ pgtype.UUID) error { return s.err }
func (s stubOrgs) AcceptInvite(_ context.Context, _ string, _ pgtype.UUID) (sqlc.Organization, error) {
	return s.org, s.err
}
```

Routes in `router.go` inside the existing `/orgs/{orgID}` route:

```go
r.Post("/invites", s.handleCreateInvite)
r.Get("/invites", s.handleListInvites)
r.Delete("/invites/{inviteID}", s.handleRevokeInvite)
```

And inside the same authed group, next to `/orgs`:

```go
r.Post("/invites/accept", s.handleAcceptInvite)
```

- [ ] **Step 6: Run all tests**

Run: `go test ./... && go build ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/orgs internal/api
git commit -m "feat: add shareable org invites with expiry and revocation"
```

---

### Task 7: Instance-admin listing endpoints (FR-2.5 / FR-7.2)

**Files:**
- Create: `internal/api/admin.go`
- Modify: `internal/api/server.go` (add `AdminStore` interface + field), `internal/api/router.go`
- Test: `internal/api/admin_test.go`

**Interfaces:**
- Consumes: sqlc `ListUsers`, `ListAllOrganizations` (Task 1); `requireAuth` + `requireInstanceAdmin` (Task 4)
- Produces: `GET /api/v1/admin/users`, `GET /api/v1/admin/orgs` — instance admin only; users listing must NOT include password hashes

- [ ] **Step 1: Write the failing tests**

`internal/api/admin_test.go`:

```go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
)

type stubAdmin struct{}

func (stubAdmin) ListUsers(_ context.Context) ([]sqlc.User, error) {
	return []sqlc.User{{Email: "a@b.co", PasswordHash: "SECRET-HASH"}}, nil
}
func (stubAdmin) ListAllOrganizations(_ context.Context) ([]sqlc.Organization, error) {
	return []sqlc.Organization{{Name: "Acme", Slug: "acme"}}, nil
}

func adminRequest(t *testing.T, isAdmin bool, path string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{
		auth:  stubAuth{user: sqlc.User{Email: "a@b.co", IsInstanceAdmin: isAdmin}},
		admin: stubAdmin{},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "cargo_access", Value: "acc"})
	NewRouter(s).ServeHTTP(rec, req)
	return rec
}

func TestAdminEndpointsForbiddenForNonAdmin(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/orgs"} {
		if rec := adminRequest(t, false, path); rec.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
	}
}

func TestAdminListUsersOmitsPasswordHash(t *testing.T) {
	rec := adminRequest(t, true, "/api/v1/admin/users")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SECRET-HASH") {
		t.Fatal("password hash leaked in admin users listing")
	}
	if !strings.Contains(rec.Body.String(), "a@b.co") {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestAdminListOrgs(t *testing.T) {
	rec := adminRequest(t, true, "/api/v1/admin/orgs")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "acme") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestAdmin -v`
Expected: FAIL to build — `Server` has no field `admin`

- [ ] **Step 3: Write the implementation**

`internal/api/admin.go`:

```go
package api

import "net/http"

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.admin.ListUsers(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "listing users failed")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, userJSON(u)) // userJSON never includes the password hash
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminListOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.admin.ListAllOrganizations(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal", "listing organizations failed")
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}
```

In `internal/api/server.go` add:

```go
// AdminStore is satisfied by *sqlc.Queries.
type AdminStore interface {
	ListUsers(ctx context.Context) ([]sqlc.User, error)
	ListAllOrganizations(ctx context.Context) ([]sqlc.Organization, error)
}
```

Add `admin AdminStore` field; in `NewServer` set `s.admin = sqlc.New(pool)` inside the pool block.

Routes in `router.go` inside `/api/v1`:

```go
r.Route("/admin", func(r chi.Router) {
	r.Use(s.requireAuth, s.requireInstanceAdmin)
	r.Get("/users", s.handleAdminListUsers)
	r.Get("/orgs", s.handleAdminListOrgs)
})
```

- [ ] **Step 4: Run all tests**

Run: `go test ./... && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "feat: add instance admin user and org listing endpoints"
```

---

### Task 8: Full-stack integration test + lint pass

**Files:**
- Create: `internal/api/integration_test.go`

**Interfaces:**
- Consumes: everything above — real `auth.Service` + `orgs.Service` against testcontainers Postgres, exercised through the router.

- [ ] **Step 1: Write the integration test**

`internal/api/integration_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/config"
	"github.com/bograh/cargo/internal/db"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startServer(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("cargo"),
		tcpostgres.WithUsername("cargo"),
		tcpostgres.WithPassword("cargo"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	cfg := config.Config{Env: "development"}
	return NewServer(cfg, pool).Handler()
}

// client carries cookies between requests like a browser.
type client struct {
	h       http.Handler
	cookies []*http.Cookie
}

func (c *client) do(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		replaced := false
		for i, old := range c.cookies {
			if old.Name == ck.Name {
				c.cookies[i] = ck
				replaced = true
			}
		}
		if !replaced {
			c.cookies = append(c.cookies, ck)
		}
	}
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func TestFullAuthOrgInviteFlow(t *testing.T) {
	h := startServer(t)

	// First user registers → instance admin.
	alice := &client{h: h}
	rec, body := alice.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"email":"alice@x.co","password":"password-123"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if body["is_instance_admin"] != true {
		t.Fatal("first user should be instance admin")
	}

	// Alice creates an org and an invite.
	rec, body = alice.do(t, http.MethodPost, "/api/v1/orgs", `{"name":"Acme"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org: %d %s", rec.Code, rec.Body)
	}
	orgID, _ := body["id"].(string)
	if orgID == "" {
		t.Fatalf("org id missing: %v", body)
	}
	rec, body = alice.do(t, http.MethodPost, "/api/v1/orgs/"+orgID+"/invites", `{"role":"member"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invite: %d %s", rec.Code, rec.Body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatal("invite token missing")
	}

	// Second user registers (not admin), sees no orgs, accepts the invite.
	bob := &client{h: h}
	rec, body = bob.do(t, http.MethodPost, "/api/v1/auth/register",
		`{"email":"bob@x.co","password":"password-123"}`)
	if rec.Code != http.StatusCreated || body["is_instance_admin"] != false {
		t.Fatalf("bob register: %d %v", rec.Code, body)
	}
	if rec, _ := bob.do(t, http.MethodGet, "/api/v1/orgs/"+orgID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob pre-invite org get: %d", rec.Code)
	}
	if rec, _ = bob.do(t, http.MethodPost, "/api/v1/invites/accept", `{"token":"`+token+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("accept invite: %d %s", rec.Code, rec.Body)
	}
	rec, body = bob.do(t, http.MethodGet, "/api/v1/orgs/"+orgID, "")
	if rec.Code != http.StatusOK || body["role"] != "member" {
		t.Fatalf("bob org get: %d %v", rec.Code, body)
	}

	// Bob (not instance admin) cannot use admin endpoints; Alice can.
	if rec, _ := bob.do(t, http.MethodGet, "/api/v1/admin/users", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("bob admin: %d", rec.Code)
	}
	if rec, _ := alice.do(t, http.MethodGet, "/api/v1/admin/users", ""); rec.Code != http.StatusOK {
		t.Fatalf("alice admin: %d", rec.Code)
	}

	// Refresh rotates and old refresh is rejected afterwards.
	var oldRefresh string
	for _, ck := range alice.cookies {
		if ck.Name == "cargo_refresh" {
			oldRefresh = ck.Value
		}
	}
	if rec, _ := alice.do(t, http.MethodPost, "/api/v1/auth/refresh", ""); rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d", rec.Code)
	}
	stale := &client{h: h, cookies: []*http.Cookie{{Name: "cargo_refresh", Value: oldRefresh}}}
	if rec, _ := stale.do(t, http.MethodPost, "/api/v1/auth/refresh", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale refresh: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run the integration test**

Run: `go test ./internal/api/ -run TestFullAuthOrgInviteFlow -v`
Expected: PASS

- [ ] **Step 3: Run the full suite + lint exactly as CI does**

Run: `go build ./... && go vet ./... && go test ./...`
Also run golangci-lint if installed locally: `golangci-lint run`
Expected: all green

- [ ] **Step 4: Commit**

```bash
git add internal/api
git commit -m "test: add end-to-end auth, org, and invite flow test"
```

---

## Out of scope for M2 (next milestones)

- Frontend pages (login/register, org dashboard) — M3, together with app CRUD UI
- Instance settings write API/UI (SMTP, GitHub App creds) — with GitHub integration milestone
- Session cleanup job (expired session rows) — with the River jobs milestone

## Self-review notes

- FR-1.1 argon2id → Task 2; FR-1.2 rotation + reuse detection → Task 3; FR-1.3 first-user-admin → Task 1 SQL + Task 3 test; FR-1.4 rate limiting → Task 4; FR-1.5 `AuthProvider` seam → Task 3 `provider.go`.
- FR-2.1 creator-becomes-owner → Task 5; FR-2.2 four roles → schema CHECK + `roleRank`; FR-2.3 invites with role/expiry/revocation → Task 6; FR-2.4 query scoping (404 for non-members, enforced in service) → Task 5 tests; FR-2.5 admin listing without implicit membership → Task 7.
- Type caveat: sqlc's default pgx/v5 codegen emits `pgtype.UUID`/`pgtype.Timestamptz`; if generated models differ, the executing engineer must adapt wrappers in Tasks 3/5/6 to the generated types (called out in Task 3 Step 6).
