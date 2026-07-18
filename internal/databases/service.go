// Package databases is the control-plane service for managed database
// instances (postgres/redis) and their per-app attachments. It owns
// credential generation, engine-level provisioning of roles/ACL users via
// the reconciler.DatabaseProvider seam, and snapshot management.
package databases

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"crypto/rand"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("database not found")
	ErrForbidden  = errors.New("insufficient role")
	ErrValidation = errors.New("validation failed")
	ErrConflict   = errors.New("conflict")
)

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}

// maxRedisDBs is the number of logical redis databases (indexes 0..15).
const maxRedisDBs = 16

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	versionRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

type Service struct {
	q        *sqlc.Queries
	pool     *pgxpool.Pool
	box      *crypto.Box
	provider reconciler.DatabaseProvider
	dataDir  string
}

func NewService(pool *pgxpool.Pool, box *crypto.Box, provider reconciler.DatabaseProvider, dataDir string) *Service {
	return &Service{q: sqlc.New(pool), pool: pool, box: box, provider: provider, dataDir: dataDir}
}

// CreateInput describes a new managed database instance.
type CreateInput struct {
	Name       string
	Engine     string // postgres | redis
	Version    string
	RedisMode  string // acl | shared (required iff engine==redis)
	ExposePort bool   // publish a host port (needed for host-side access/tests)
}

// InstanceSummary is a list-row: the instance plus its attachment count.
type InstanceSummary struct {
	Instance        sqlc.DatabaseInstance
	AttachmentCount int64
}

// Detail is a single-instance view including attachments and engine size.
type Detail struct {
	Instance    sqlc.DatabaseInstance
	Attachments []sqlc.DatabaseAttachment
	SizeBytes   int64
}

// SnapshotInfo describes one on-disk snapshot file.
type SnapshotInfo struct {
	Name    string
	Size    int64
	Created time.Time
}

// ---- role gating ---------------------------------------------------------

func (s *Service) roleIn(ctx context.Context, orgID, actor pgtype.UUID) (string, error) {
	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: orgID, UserID: actor})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return m.Role, err
}

// instFor loads an instance and verifies the actor holds at least minRole in
// its org. Non-members get ErrNotFound (scoping), members below minRole get
// ErrForbidden.
func (s *Service) instFor(ctx context.Context, id, actor pgtype.UUID, minRole string) (sqlc.DatabaseInstance, error) {
	inst, err := s.q.GetDatabaseInstance(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.DatabaseInstance{}, ErrNotFound
	}
	if err != nil {
		return sqlc.DatabaseInstance{}, err
	}
	role, err := s.roleIn(ctx, inst.OrgID, actor)
	if err != nil {
		return sqlc.DatabaseInstance{}, err
	}
	if roleRank[role] < roleRank[minRole] {
		return sqlc.DatabaseInstance{}, ErrForbidden
	}
	return inst, nil
}

// ---- secret helpers ------------------------------------------------------

// genPassword returns a 128-bit base64 raw-url credential.
func genPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sealSecret seals plaintext and wraps it as {"enc": base64} for the JSONB
// column (matches internal/settings).
func (s *Service) sealSecret(plain string) ([]byte, error) {
	sealed, err := s.box.Seal([]byte(plain))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"enc": base64.StdEncoding.EncodeToString(sealed)})
}

func (s *Service) openSecret(data []byte) (string, error) {
	var w struct {
		Enc string `json:"enc"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return "", err
	}
	sealed, err := base64.StdEncoding.DecodeString(w.Enc)
	if err != nil {
		return "", err
	}
	pt, err := s.box.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// ---- small helpers -------------------------------------------------------

func uuidStr(id pgtype.UUID) string {
	v, _ := id.Value()
	s, _ := v.(string)
	return s
}

func txt(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
func i4(i int32) pgtype.Int4   { return pgtype.Int4{Int32: i, Valid: true} }

// quoteLiteral escapes a string for use inside a single-quoted SQL literal by
// doubling embedded single quotes. Callers wrap the result in quotes.
func quoteLiteral(s string) string { return strings.ReplaceAll(s, "'", "''") }

// dbIdent maps an app slug to a postgres/redis identifier: app_<slug> with
// dashes turned into underscores. The slug is already validated (DNS-safe),
// so the result contains only [a-z0-9_].
func dbIdent(slug string) string {
	return "app_" + strings.ReplaceAll(slug, "-", "_")
}

// freePort binds :0, reads the assigned port, and releases it. There is an
// inherent race between release and container publish; acceptable for our
// single-host dev/self-host target.
func freePort() (int32, error) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return int32(l.Addr().(*net.TCPAddr).Port), nil //nolint:gosec // ephemeral port fits int32
}

// pickIndex returns the lowest free logical redis db index in [0,maxRedisDBs),
// or ErrConflict when all are in use.
func pickIndex(used []int32) (int32, error) {
	seen := make(map[int32]bool, len(used))
	for _, u := range used {
		seen[u] = true
	}
	for i := int32(0); i < maxRedisDBs; i++ {
		if !seen[i] {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: no free redis database index (max %d)", ErrConflict, maxRedisDBs)
}

// ---- create / provision --------------------------------------------------

func validateCreate(in CreateInput) error {
	if !nameRe.MatchString(in.Name) {
		return fmt.Errorf("%w: name must be 3-40 chars, lowercase alphanumeric and dashes", ErrValidation)
	}
	switch in.Engine {
	case "postgres", "redis":
	default:
		return fmt.Errorf("%w: engine must be postgres or redis", ErrValidation)
	}
	if !versionRe.MatchString(in.Version) {
		return fmt.Errorf("%w: version is required", ErrValidation)
	}
	if in.Engine == "redis" {
		switch in.RedisMode {
		case "acl", "shared":
		default:
			return fmt.Errorf("%w: redis_mode must be acl or shared", ErrValidation)
		}
	} else if in.RedisMode != "" {
		return fmt.Errorf("%w: redis_mode is only valid for redis", ErrValidation)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, orgID, actor pgtype.UUID, in CreateInput) (sqlc.DatabaseInstance, error) {
	role, err := s.roleIn(ctx, orgID, actor)
	if err != nil {
		return sqlc.DatabaseInstance{}, err
	}
	if roleRank[role] < roleRank["member"] {
		return sqlc.DatabaseInstance{}, ErrForbidden
	}
	if err := validateCreate(in); err != nil {
		return sqlc.DatabaseInstance{}, err
	}
	adminPass, err := genPassword()
	if err != nil {
		return sqlc.DatabaseInstance{}, err
	}
	adminSecret, err := s.sealSecret(adminPass)
	if err != nil {
		return sqlc.DatabaseInstance{}, err
	}
	params := sqlc.CreateDatabaseInstanceParams{
		OrgID:       orgID,
		Name:        in.Name,
		Engine:      in.Engine,
		Version:     in.Version,
		Status:      "provisioning",
		AdminSecret: adminSecret,
	}
	if in.Engine == "redis" {
		params.RedisMode = txt(in.RedisMode)
	}
	if in.ExposePort {
		port, err := freePort()
		if err != nil {
			return sqlc.DatabaseInstance{}, err
		}
		params.HostPort = i4(port)
	}
	inst, err := s.q.CreateDatabaseInstance(ctx, params)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sqlc.DatabaseInstance{}, fmt.Errorf("%w: an instance named %q already exists", ErrConflict, in.Name)
	}
	return inst, err
}

// LogPath returns the provision log path for an instance id string.
func (s *Service) LogPath(id string) string {
	return filepath.Join(s.dataDir, "db-logs", id+".log")
}

// Provision runs the container-level provisioning for an instance. It is
// called by the provision job (no actor check) and records running/error.
func (s *Service) Provision(ctx context.Context, id pgtype.UUID) error {
	inst, err := s.q.GetDatabaseInstance(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	adminPass, err := s.openSecret(inst.AdminSecret)
	if err != nil {
		return err
	}
	idStr := uuidStr(inst.ID)
	if err := os.MkdirAll(filepath.Dir(s.LogPath(idStr)), 0o700); err != nil {
		return err
	}
	logf, err := os.Create(s.LogPath(idStr))
	if err != nil {
		return err
	}
	defer func() { _ = logf.Close() }()

	spec := reconciler.DBSpec{
		InstanceID: idStr,
		Engine:     inst.Engine,
		Version:    inst.Version,
		AdminPass:  adminPass,
		HostPort:   inst.HostPort.Int32,
	}
	provErr := s.provider.ProvisionDB(ctx, spec, logf)
	status := "running"
	if provErr != nil {
		status = "error"
	}
	if err := s.q.SetDatabaseInstanceStatus(ctx, sqlc.SetDatabaseInstanceStatusParams{ID: inst.ID, Status: status}); err != nil {
		return err
	}
	return provErr
}

// ---- list / get ----------------------------------------------------------

func (s *Service) List(ctx context.Context, orgID, actor pgtype.UUID) ([]InstanceSummary, error) {
	if _, err := s.roleIn(ctx, orgID, actor); err != nil {
		return nil, err
	}
	rows, err := s.q.ListDatabaseInstancesByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]InstanceSummary, 0, len(rows))
	for _, inst := range rows {
		count, err := s.q.CountAttachmentsByInstance(ctx, inst.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, InstanceSummary{Instance: inst, AttachmentCount: count})
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id, actor pgtype.UUID) (Detail, error) {
	inst, err := s.instFor(ctx, id, actor, "viewer")
	if err != nil {
		return Detail{}, err
	}
	atts, err := s.q.ListAttachmentsByInstance(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Instance: inst, Attachments: atts, SizeBytes: s.sizeBytes(ctx, inst)}, nil
}

// sizeBytes reads on-disk size from the engine. Any failure degrades to 0 so
// a read hiccup never fails the request.
func (s *Service) sizeBytes(ctx context.Context, inst sqlc.DatabaseInstance) int64 {
	id := uuidStr(inst.ID)
	switch inst.Engine {
	case "redis":
		out, err := s.provider.ExecDB(ctx, id, "redis", "",
			"redis-cli", "--no-auth-warning", "INFO", "memory")
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if v, ok := strings.CutPrefix(line, "used_memory:"); ok {
				n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				return n
			}
		}
		return 0
	default:
		out, err := s.provider.ExecDB(ctx, id, "postgres",
			"SELECT COALESCE(sum(pg_database_size(datname)),0) FROM pg_database;",
			"psql", "-At", "-U", "postgres")
		if err != nil {
			return 0
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		return n
	}
}

// Delete tears down an instance. It refuses (ErrConflict) while attachments
// remain.
func (s *Service) Delete(ctx context.Context, id, actor pgtype.UUID) error {
	inst, err := s.instFor(ctx, id, actor, "admin")
	if err != nil {
		return err
	}
	count, err := s.q.CountAttachmentsByInstance(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: instance has %d attachment(s); detach first", ErrConflict, count)
	}
	if err := s.provider.TeardownDB(ctx, uuidStr(inst.ID), os.Stderr); err != nil {
		return err
	}
	return s.q.DeleteDatabaseInstance(ctx, id)
}
