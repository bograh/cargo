package databases

import (
	"context"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Attach provisions an isolated credential on the instance for the app and
// records the attachment. Returns the connection URL (host = the instance's
// stable container name) and the row.
func (s *Service) Attach(ctx context.Context, instanceID, appID, actor pgtype.UUID) (string, sqlc.DatabaseAttachment, error) {
	inst, err := s.instFor(ctx, instanceID, actor, "admin")
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

	switch inst.Engine {
	case "postgres":
		return s.attachPostgres(ctx, inst, appID, app.Slug)
	case "mysql":
		return s.attachMySQL(ctx, inst, appID, app.Slug)
	case "mongodb":
		return s.attachMongo(ctx, inst, appID, app.Slug)
	default: // redis
		if inst.RedisMode.String == "acl" {
			return s.attachRedisACL(ctx, inst, appID, app.Slug)
		}
		return s.attachRedisShared(ctx, inst, appID)
	}
}

// EnvKey returns the environment variable an engine's connection URL is
// injected under. Distinct per engine so an app may attach one of each
// without the keys colliding.
func EnvKey(engine string) string {
	switch engine {
	case "redis":
		return "REDIS_URL"
	case "mysql":
		return "MYSQL_URL"
	case "mongodb":
		return "MONGODB_URL"
	default: // postgres
		return "DATABASE_URL"
	}
}

func envKey(engine string) string { return EnvKey(engine) }

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
	return url, att, dupAttachErr(err)
}

// dupAttachErr maps the UNIQUE(instance_id, app_id) violation (a concurrent
// duplicate attach that races past the pre-check) to ErrConflict so it
// surfaces as 409 rather than a generic 500. Other errors pass through.
func dupAttachErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == "database_attachments_instance_id_app_id_key" {
		return fmt.Errorf("%w: app already attached to this instance", ErrConflict)
	}
	return err
}

func (s *Service) attachMySQL(ctx context.Context, inst sqlc.DatabaseInstance, appID pgtype.UUID, slug string) (string, sqlc.DatabaseAttachment, error) {
	name := dbIdent(slug)
	pass, err := genPassword()
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	// Identifiers come from the validated slug (safe [a-z0-9_]); the password is
	// base64url (no quotes/backslashes). IF NOT EXISTS + ALTER USER make re-attach
	// idempotent. Statements are fed on stdin (never argv); root auth is via
	// MYSQL_PWD in the container env_file.
	script := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%[1]s`;\n"+
			"CREATE USER IF NOT EXISTS '%[1]s'@'%%' IDENTIFIED BY '%[2]s';\n"+
			"ALTER USER '%[1]s'@'%%' IDENTIFIED BY '%[2]s';\n"+
			"GRANT ALL PRIVILEGES ON `%[1]s`.* TO '%[1]s'@'%%';\n"+
			"FLUSH PRIVILEGES;\n",
		name, pass)
	if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "mysql", script,
		"sh", "-c", reconciler.MySQLClient); err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	secret, err := s.sealSecret(pass)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	url := fmt.Sprintf("mysql://%s:%s@%s:3306/%s", name, pass, dbHost(inst), name)
	att, err := s.q.CreateDatabaseAttachment(ctx, sqlc.CreateDatabaseAttachmentParams{
		InstanceID: inst.ID, AppID: appID,
		DbName: txt(name), RoleName: txt(name), Secret: secret,
	})
	return url, att, dupAttachErr(err)
}

func (s *Service) attachMongo(ctx context.Context, inst sqlc.DatabaseInstance, appID pgtype.UUID, slug string) (string, sqlc.DatabaseAttachment, error) {
	name := dbIdent(slug)
	pass, err := genPassword()
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	// The per-app user is scoped to readWrite on its own database only. The
	// script (with the generated password) is fed on stdin; admin auth is via
	// MongoshAdmin, which reads MONGO_ADMIN_* from the container env. Re-attach
	// updates the existing user's password.
	script := fmt.Sprintf(
		"var d = db.getSiblingDB('%[1]s');"+
			"if (d.getUser('%[1]s')) { d.updateUser('%[1]s', { pwd: '%[2]s', roles: [{ role: 'readWrite', db: '%[1]s' }] }); }"+
			" else { d.createUser({ user: '%[1]s', pwd: '%[2]s', roles: [{ role: 'readWrite', db: '%[1]s' }] }); }\n",
		name, pass)
	if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "mongodb", script,
		"sh", "-c", reconciler.MongoshAdmin); err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	secret, err := s.sealSecret(pass)
	if err != nil {
		return "", sqlc.DatabaseAttachment{}, err
	}
	url := fmt.Sprintf("mongodb://%s:%s@%s:27017/%s?authSource=%s", name, pass, dbHost(inst), name, name)
	att, err := s.q.CreateDatabaseAttachment(ctx, sqlc.CreateDatabaseAttachmentParams{
		InstanceID: inst.ID, AppID: appID,
		DbName: txt(name), RoleName: txt(name), Secret: secret,
	})
	return url, att, dupAttachErr(err)
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
	//
	// resetchannels denies ALL pub/sub channel access: redis pub/sub channels
	// are global (not scoped to the logical db index), so granting channels
	// here would let acl-mode tenants PUBLISH/SUBSCRIBE across each other's
	// channels. acl mode is therefore intentionally key-isolated with NO
	// pub/sub; use shared mode if an app needs pub/sub.
	cmd := fmt.Sprintf("ACL SETUSER %s on >%s allkeys resetchannels +@all -@admin -@dangerous -acl -select +select|%d\n",
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
		return sqlc.DatabaseAttachment{}, 0, dupAttachErr(err)
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
	inst, err := s.instFor(ctx, instanceID, actor, "admin")
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
	if err := s.detachEngine(ctx, inst, att); err != nil {
		return err
	}
	return s.q.DeleteDatabaseAttachment(ctx, att.ID)
}

// detachEngine performs the engine-level credential teardown for one
// attachment: for postgres it terminates sessions, reassigns/drops owned
// objects and drops the role (the database itself is kept so data survives);
// for redis acl it removes the ACL user. Shared by Detach (role-gated) and
// DetachAllForApp (internal, app-deletion path) so both do identical cleanup.
func (s *Service) detachEngine(ctx context.Context, inst sqlc.DatabaseInstance, att sqlc.DatabaseAttachment) error {
	switch {
	case inst.Engine == "postgres":
		name := att.RoleName.String
		id := uuidStr(inst.ID)
		// Terminate the role's sessions, revoke connect, reassign owned
		// objects (in the app db and in postgres) to postgres, then drop the
		// role. The database itself is kept so data survives.
		//
		// Each step is a separate psql invocation rather than one script with
		// `\connect` mid-way: when psql reads a piped script it discards the
		// unread stdin on reconnect, so everything after `\connect` silently
		// never runs (the role was left behind), and a failed reconnect under
		// ON_ERROR_STOP aborts the whole detach with a generic error.
		revoke := fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '%[1]s';
REVOKE CONNECT ON DATABASE %[1]s FROM %[1]s;
`, name)
		if _, err := s.provider.ExecDB(ctx, id, "postgres", revoke,
			"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres"); err != nil {
			return err
		}
		// Reassign/drop the role's objects while connected to its own db.
		inApp := fmt.Sprintf("REASSIGN OWNED BY %[1]s TO postgres;\nDROP OWNED BY %[1]s;\n", name)
		if _, err := s.provider.ExecDB(ctx, id, "postgres", inApp,
			"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", name); err != nil {
			return err
		}
		// Back in postgres, take over the database it owns (a shared object),
		// then drop the now-unreferenced role.
		dropRole := fmt.Sprintf("REASSIGN OWNED BY %[1]s TO postgres;\nDROP ROLE IF EXISTS %[1]s;\n", name)
		if _, err := s.provider.ExecDB(ctx, id, "postgres", dropRole,
			"psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres"); err != nil {
			return err
		}
	case inst.Engine == "mysql":
		name := att.RoleName.String
		// Drop the app's user; the database (its data) is kept.
		script := fmt.Sprintf("DROP USER IF EXISTS '%[1]s'@'%%';\nFLUSH PRIVILEGES;\n", name)
		if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "mysql", script,
			"sh", "-c", reconciler.MySQLClient); err != nil {
			return err
		}
	case inst.Engine == "mongodb":
		name := att.RoleName.String
		// Drop the app's user; the database (its data) is kept.
		script := fmt.Sprintf("var d = db.getSiblingDB('%[1]s'); if (d.getUser('%[1]s')) { d.dropUser('%[1]s'); }\n", name)
		if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "mongodb", script,
			"sh", "-c", reconciler.MongoshAdmin); err != nil {
			return err
		}
	case inst.RedisMode.String == "acl":
		if _, err := s.provider.ExecDB(ctx, uuidStr(inst.ID), "redis", "",
			"redis-cli", "--no-auth-warning", "ACL", "DELUSER", att.AclUser.String); err != nil {
			return err
		}
	}
	return nil
}

// DetachAllForApp tears down every managed-database credential belonging to an
// app and deletes its attachment rows. It is the app-deletion cleanup path:
// database_attachments rows cascade-delete with the app (FK ON DELETE
// CASCADE), but that leaves the postgres role / redis ACL user alive on the
// engine. For redis acl that is a cross-tenant breach — the freed db_index is
// later reassigned to a new attachment while the deleted app's ACL user still
// holds +select|<idx>, so a retained old REDIS_URL could read the new tenant's
// keys. Internal (no actor check), like MarkError: it runs from apps.Delete
// after the caller has already been authorized to delete the app.
func (s *Service) DetachAllForApp(ctx context.Context, appID pgtype.UUID) error {
	rows, err := s.q.ListAttachmentsByApp(ctx, appID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		inst, err := s.q.GetDatabaseInstance(ctx, r.DatabaseAttachment.InstanceID)
		if err != nil {
			return err
		}
		if err := s.detachEngine(ctx, inst, r.DatabaseAttachment); err != nil {
			return err
		}
		if err := s.q.DeleteDatabaseAttachment(ctx, r.DatabaseAttachment.ID); err != nil {
			return err
		}
	}
	return nil
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
	case inst.Engine == "mysql":
		pass, err := s.openSecret(att.Secret)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("mysql://%s:%s@%s:3306/%s", att.RoleName.String, pass, host, att.DbName.String), nil
	case inst.Engine == "mongodb":
		pass, err := s.openSecret(att.Secret)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("mongodb://%s:%s@%s:27017/%s?authSource=%s", att.RoleName.String, pass, host, att.DbName.String, att.DbName.String), nil
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
