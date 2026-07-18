package databases

import (
	"context"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Attach provisions an isolated credential on the instance for the app and
// records the attachment. Returns the connection URL (host = the instance's
// stable container name) and the row.
func (s *Service) Attach(ctx context.Context, instanceID, appID, actor pgtype.UUID) (string, sqlc.DatabaseAttachment, error) {
	inst, err := s.instFor(ctx, instanceID, actor, "member")
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	app, err := s.q.GetApplication(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sqlc.DatabaseAttachment{}, ErrNotFound
	}
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	if app.OrgID != inst.OrgID {
		return "", sqlc.DatabaseAttachment{}, ErrNotFound
	}
	if inst.Status != "running" {
		return "", sqlc.DatabaseAttachment{}, fmt.Errorf("%w: instance is not running (%s)", ErrConflict, inst.Status)
	}
	// No existing attachment between this instance and app.
	if _, err := s.q.GetDatabaseAttachment(ctx, sqlc.GetDatabaseAttachmentParams{InstanceID: instanceID, AppID: appID}); err == nil {
		return "", sqlc.DatabaseAttachment{}, fmt.Errorf("%w: app already attached to this instance", ErrConflict)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", sqlc.DatabaseAttachment{}, err
	}
	// One attachment per engine per app, so DATABASE_URL/REDIS_URL never collide.
	appAtts, err := s.q.ListAttachmentsByApp(ctx, appID)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	for _, a := range appAtts {
		if a.Engine == inst.Engine {
			return "", sqlc.DatabaseAttachment{}, fmt.Errorf("%w: app already has a %s attachment", ErrConflict, inst.Engine)
		}
	}
	// A user-set env var of the target key would be shadowed; refuse.
	envVars, err := s.q.ListEnvVars(ctx, appID)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	wantKey := envKey(inst.Engine)
	for _, v := range envVars {
		if v.Key == wantKey {
			return "", sqlc.DatabaseAttachment{}, fmt.Errorf("%w: app already defines %s", ErrConflict, wantKey)
		}
	}

	switch {
	case inst.Engine == "postgres":
		return s.attachPostgres(ctx, inst, appID, app.Slug)
	case inst.RedisMode.String == "acl":
		return s.attachRedisACL(ctx, inst, appID, app.Slug)
	default: // redis shared
		return s.attachRedisShared(ctx, inst, appID)
	}
}

func envKey(engine string) string {
	if engine == "redis" {
		return "REDIS_URL"
	}
	return "DATABASE_URL"
}

func dbHost(inst sqlc.DatabaseInstance) string { return "cargo-db-" + uuidStr(inst.ID) }

func (s *Service) attachPostgres(ctx context.Context, inst sqlc.DatabaseInstance, appID pgtype.UUID, slug string) (string, sqlc.DatabaseAttachment, error) {
	name := dbIdent(slug)
	pass, err := genPassword()
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	// Identifiers come only from the validated slug (safe); the password is
	// escaped via quoteLiteral. CREATE DATABASE cannot run inside a
	// transaction/DO block, hence \gexec. Guards make re-attach idempotent
	// after a detach that keeps the database.
	q := quoteLiteral(pass)
	script := fmt.Sprintf(`DO $do$
BEGIN
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN
    ALTER ROLE %[1]s LOGIN PASSWORD '%[2]s';
  ELSE
    CREATE ROLE %[1]s LOGIN PASSWORD '%[2]s';
  END IF;
END
$do$;
SELECT format('CREATE DATABASE %%I OWNER %%I', '%[1]s', '%[1]s')
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '%[1]s')\gexec
ALTER DATABASE %[1]s OWNER TO %[1]s;
REVOKE CONNECT ON DATABASE %[1]s FROM PUBLIC;
`, name, q)
	if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "postgres", script,
		"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres"); err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	secret, err := s.sealSecret(pass)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	url := fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", name, pass, dbHost(inst), name)
	att, err := s.q.CreateDatabaseAttachment(ctx, sqlc.CreateDatabaseAttachmentParams{
		InstanceID: inst.ID, AppID: appID,
		DbName: txt(name), RoleName: txt(name), Secret: secret,
	})
	return url, att, err
}

func (s *Service) attachRedisACL(ctx context.Context, inst sqlc.DatabaseInstance, appID pgtype.UUID, slug string) (string, sqlc.DatabaseAttachment, error) {
	name := dbIdent(slug)
	pass, err := genPassword()
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	secret, err := s.sealSecret(pass)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	// Reserve the index in the DB first (the partial unique index makes
	// concurrent pickers collide), then create the ACL user for the reserved
	// index; on any failure the reservation row is deleted. This closes the
	// pick-then-insert race and guarantees the ACL user matches the stored
	// index even across retries.
	att, idx, err := s.reserveIndex(ctx, inst.ID, sqlc.CreateDatabaseAttachmentParams{
		InstanceID: inst.ID, AppID: appID, AclUser: txt(name), Secret: secret,
	})
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	// Restrict the user to a single logical db: -select drops SELECT on all
	// indexes, +select|<idx> re-grants only its own. It cannot AUTH as, or
	// read the keyspace of, any other user's index. -@dangerous keeps
	// FLUSHALL/SWAPDB/etc. away (SET/GET are not in @dangerous). The password
	// is fed via stdin, never on argv.
	cmd := fmt.Sprintf("ACL SETUSER %s on >%s allkeys allchannels +@all -@admin -@dangerous -acl -select +select|%d\n",
		name, pass, idx)
	if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "redis", cmd,
		"redis-cli", "--no-auth-warning"); err != nil {
		_ = s.q.DeleteDatabaseAttachment(ctx, att.ID)
		return "", sqlc.DatabaseAttachment{}, err
	}
	url := fmt.Sprintf("redis://%s:%s@%s:6379/%d", name, pass, dbHost(inst), idx)
	return url, att, nil
}

func (s *Service) attachRedisShared(ctx context.Context, inst sqlc.DatabaseInstance, appID pgtype.UUID) (string, sqlc.DatabaseAttachment, error) {
	adminPass, err := s.openSecret(inst.AdminSecret)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	att, idx, err := s.reserveIndex(ctx, inst.ID, sqlc.CreateDatabaseAttachmentParams{
		InstanceID: inst.ID, AppID: appID,
	})
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	url := fmt.Sprintf("redis://:%s@%s:6379/%d", adminPass, dbHost(inst), idx)
	return url, att, nil
}

// reserveIndex picks a free logical db index and inserts the attachment row
// with it, retrying on the partial-unique-index conflict (concurrent pickers).
// params.DbIndex is set by this function.
func (s *Service) reserveIndex(ctx context.Context, instanceID pgtype.UUID, params sqlc.CreateDatabaseAttachmentParams) (sqlc.DatabaseAttachment, int32, error) {
	const attempts = 3
	for i := 0; i < attempts; i++ {
		idx, err := s.nextIndex(ctx, instanceID)
		if err != nil {
			return sqlc.DatabaseAttachment{}, 0, err
		}
		params.DbIndex = i4(idx)
		att, err := s.q.CreateDatabaseAttachment(ctx, params)
		if err == nil {
			return att, idx, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "database_attachments_instance_db_index" {
			continue // index was taken concurrently; re-pick
		}
		return sqlc.DatabaseAttachment{}, 0, err
	}
	return sqlc.DatabaseAttachment{}, 0, fmt.Errorf("%w: could not allocate a free redis database index", ErrConflict)
}

func (s *Service) nextIndex(ctx context.Context, instanceID pgtype.UUID) (int32, error) {
	used, err := s.q.ListUsedDbIndexes(ctx, instanceID)
	if err != nil {
		return 0, err
	}
	idxs := make([]int32, 0, len(used))
	for _, u := range used {
		if u.Valid {
			idxs = append(idxs, u.Int32)
		}
	}
	return pickIndex(idxs)
}

// Detach removes the app's credential from the instance and deletes the row.
// For postgres the database is kept (data survives); only the role is dropped.
func (s *Service) Detach(ctx context.Context, instanceID, appID, actor pgtype.UUID) error {
	inst, err := s.instFor(ctx, instanceID, actor, "member")
	if err != nil {
		return err
	}
	att, err := s.q.GetDatabaseAttachment(ctx, sqlc.GetDatabaseAttachmentParams{InstanceID: instanceID, AppID: appID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	switch {
	case inst.Engine == "postgres":
		name := att.RoleName.String
		// Terminate the role's sessions, revoke connect, reassign owned
		// objects (in postgres and in the app db) to postgres, then drop the
		// role. The database itself is kept.
		script := fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '%[1]s';
REVOKE CONNECT ON DATABASE %[1]s FROM %[1]s;
\connect %[1]s
REASSIGN OWNED BY %[1]s TO postgres;
DROP OWNED BY %[1]s;
\connect postgres
REASSIGN OWNED BY %[1]s TO postgres;
DROP ROLE IF EXISTS %[1]s;
`, name)
		if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "postgres", script,
			"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres"); err != nil {
			return err
		}
	case inst.RedisMode.String == "acl":
		if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "redis", "",
			"redis-cli", "--no-auth-warning", "ACL", "DELUSER", att.AclUser.String); err != nil {
			return err
		}
	}
	return s.q.DeleteDatabaseAttachment(ctx, att.ID)
}

// EnvFor rebuilds the injected env vars for an app's attachments. Pipeline-
// facing (no actor check).
func (s *Service) EnvFor(ctx context.Context, appID pgtype.UUID) (map[string]string, error) {
	rows, err := s.q.ListAttachmentsByApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(rows))
	for _, r := range rows {
		inst, err := s.q.GetDatabaseInstance(ctx, r.DatabaseAttachment.InstanceID)
		if err != nil {
			return nil, err
		}
		url, err := s.attachmentURL(inst, r.DatabaseAttachment)
		if err != nil {
			return nil, err
		}
		key := envKey(inst.Engine)
		if _, dup := env[key]; dup {
			return nil, fmt.Errorf("%w: app %s has multiple %s attachments", ErrConflict, uuidStr(appID), key)
		}
		env[key] = url
	}
	return env, nil
}

// attachmentURL reconstructs the connection URL from the stored (sealed)
// secret and the attachment shape.
func (s *Service) attachmentURL(inst sqlc.DatabaseInstance, att sqlc.DatabaseAttachment) (string, error) {
	host := dbHost(inst)
	switch {
	case inst.Engine == "postgres":
		pass, err := s.openSecret(att.Secret)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", att.RoleName.String, pass, host, att.DbName.String), nil
	case att.AclUser.Valid && len(att.Secret) > 0: // redis acl
		pass, err := s.openSecret(att.Secret)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("redis://%s:%s@%s:6379/%d", att.AclUser.String, pass, host, att.DbIndex.Int32), nil
	default: // redis shared
		adminPass, err := s.openSecret(inst.AdminSecret)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("redis://:%s@%s:6379/%d", adminPass, host, att.DbIndex.Int32), nil
	}
}
