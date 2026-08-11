package keyrotate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// One Postgres for the whole package; each test gets a truncated schema.
// Starting a container per test makes the suite flaky under load.
var (
	sharedURL  string
	sharedOnce sync.Once
	sharedErr  error
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	sharedOnce.Do(func() {
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("cargo"),
			tcpostgres.WithUsername("cargo"),
			tcpostgres.WithPassword("cargo"),
			// The postgres image starts a temporary server for initdb and then
			// restarts it, so "the port is open" is not "the database is up" --
			// waiting on the port raced that restart and failed migrations with
			// connection resets. The readiness line appears twice: once for the
			// init server, once for the real one.
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
		)
		if err != nil {
			sharedErr = err
			return
		}
		url, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedErr = err
			return
		}
		if err := db.Migrate(ctx, url); err != nil {
			sharedErr = err
			return
		}
		sharedURL = url
	})
	if sharedErr != nil {
		t.Fatalf("start postgres: %v", sharedErr)
	}
	pool, err := db.Open(ctx, sharedURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `TRUNCATE database_attachments, database_instances,
		env_vars, applications, memberships, organizations, users, instance_settings CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func jsonSecret(t *testing.T, box *crypto.Box, plain string) []byte {
	t.Helper()
	sealed, err := box.Seal([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"enc": base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fixture seeds one secret of every shape the rotation has to cover and
// returns the plaintexts keyed by a label, so a test can assert that each is
// still readable afterwards.
type fixture struct {
	pool   *pgxpool.Pool
	plain  map[string]string
	appID  string
	instID string
}

func seed(t *testing.T, box *crypto.Box) fixture {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	f := fixture{pool: pool, plain: map[string]string{}}

	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizations (name, slug) VALUES ('Acme','acme') RETURNING id::text`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}

	// applications.registry_creds_enc — raw BYTEA.
	f.plain["registry"] = `{"server":"ghcr.io","username":"u","password":"p@ss"}`
	regEnc, err := box.Seal([]byte(f.plain["registry"]))
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO applications (org_id, name, slug, source_type, image_ref, registry_creds_enc)
		 VALUES ($1,'web','web','image','nginx:alpine',$2) RETURNING id::text`,
		orgID, regEnc).Scan(&f.appID); err != nil {
		t.Fatal(err)
	}

	// env_vars.value_enc — raw BYTEA.
	f.plain["env"] = "super-secret-token"
	envEnc, err := box.Seal([]byte(f.plain["env"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO env_vars (app_id, key, value_enc) VALUES ($1,'TOKEN',$2)`, f.appID, envEnc); err != nil {
		t.Fatal(err)
	}

	// database_instances.admin_secret — JSONB wrapper.
	f.plain["dbadmin"] = "postgres-admin-pw"
	if err := pool.QueryRow(ctx,
		`INSERT INTO database_instances (org_id, name, engine, version, admin_secret)
		 VALUES ($1,'main','postgres','16',$2) RETURNING id::text`,
		orgID, jsonSecret(t, box, f.plain["dbadmin"])).Scan(&f.instID); err != nil {
		t.Fatal(err)
	}

	// database_attachments.secret — JSONB wrapper.
	f.plain["dbattach"] = "per-app-db-pw"
	if _, err := pool.Exec(ctx,
		`INSERT INTO database_attachments (instance_id, app_id, db_name, secret)
		 VALUES ($1,$2,'web',$3)`, f.instID, f.appID, jsonSecret(t, box, f.plain["dbattach"])); err != nil {
		t.Fatal(err)
	}

	// instance_settings — encrypted rows plus a plaintext one that must survive
	// untouched.
	f.plain["smtp"] = `{"host":"smtp.example.com","password":"mail-pw"}`
	f.plain["github_app"] = `{"private_key":"-----BEGIN RSA-----"}`
	for _, k := range []string{"smtp", "github_app"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO instance_settings (key, value) VALUES ($1,$2)`,
			k, jsonSecret(t, box, f.plain[k])); err != nil {
			t.Fatal(err)
		}
	}
	// A cleared setting: no secret to rotate, must not trip anything up.
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance_settings (key, value) VALUES ('oidc', '{"enc":""}')`); err != nil {
		t.Fatal(err)
	}
	// A plaintext setting that rotation must leave alone.
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance_settings (key, value) VALUES ('apps_domain_suffix', '"apps.example.com"')`); err != nil {
		t.Fatal(err)
	}
	return f
}

// assertReadable checks every seeded secret decrypts to its original plaintext
// under the given key.
func (f fixture) assertReadable(t *testing.T, box *crypto.Box) {
	t.Helper()
	ctx := context.Background()

	var regEnc []byte
	if err := f.pool.QueryRow(ctx,
		`SELECT registry_creds_enc FROM applications WHERE id::text = $1`, f.appID).Scan(&regEnc); err != nil {
		t.Fatal(err)
	}
	open := func(label string, sealed []byte) {
		t.Helper()
		got, err := box.Open(sealed)
		if err != nil {
			t.Fatalf("%s: decrypt failed: %v", label, err)
		}
		if string(got) != f.plain[label] {
			t.Fatalf("%s = %q, want %q", label, got, f.plain[label])
		}
	}
	open("registry", regEnc)

	var envEnc []byte
	if err := f.pool.QueryRow(ctx,
		`SELECT value_enc FROM env_vars WHERE app_id::text = $1 AND key = 'TOKEN'`, f.appID).Scan(&envEnc); err != nil {
		t.Fatal(err)
	}
	open("env", envEnc)

	openJSON := func(label, query string, args ...any) {
		t.Helper()
		var raw []byte
		if err := f.pool.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		sealed, ok, err := unwrapJSON(raw)
		if err != nil || !ok {
			t.Fatalf("%s: unwrap failed (ok=%v): %v", label, ok, err)
		}
		open(label, sealed)
	}
	openJSON("dbadmin", `SELECT admin_secret FROM database_instances WHERE id::text = $1`, f.instID)
	openJSON("dbattach", `SELECT secret FROM database_attachments WHERE app_id::text = $1`, f.appID)
	openJSON("smtp", `SELECT value FROM instance_settings WHERE key = 'smtp'`)
	openJSON("github_app", `SELECT value FROM instance_settings WHERE key = 'github_app'`)
}

func TestRotateResealsEverySecretShape(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	oldBox, err := crypto.New(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	f := seed(t, oldBox)
	ctx := context.Background()

	res, err := Run(ctx, f.pool, oldKey, newK)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if res.AlreadyRotated {
		t.Fatal("reported already-rotated on a fresh database")
	}
	// One of every shape: registry creds, env var, db instance, attachment,
	// and two settings.
	if res.RegistryCreds != 1 || res.EnvVars != 1 || res.DBInstances != 1 ||
		res.DBAttachments != 1 || res.Settings != 2 || len(res.Unreadable) != 0 {
		t.Fatalf("result = %+v, want one of each shape and two settings", res)
	}

	newBox, err := crypto.New(newK)
	if err != nil {
		t.Fatal(err)
	}
	f.assertReadable(t, newBox)

	// The whole point: the old key must no longer open anything.
	var regEnc []byte
	if err := f.pool.QueryRow(ctx,
		`SELECT registry_creds_enc FROM applications WHERE id::text = $1`, f.appID).Scan(&regEnc); err != nil {
		t.Fatal(err)
	}
	if _, err := oldBox.Open(regEnc); err == nil {
		t.Fatal("the old key still decrypts a rotated secret")
	}

	// Plaintext settings must be untouched, and a cleared one left cleared.
	var suffix, oidc []byte
	if err := f.pool.QueryRow(ctx,
		`SELECT value FROM instance_settings WHERE key = 'apps_domain_suffix'`).Scan(&suffix); err != nil {
		t.Fatal(err)
	}
	if string(suffix) != `"apps.example.com"` {
		t.Fatalf("plaintext setting was modified: %s", suffix)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT value FROM instance_settings WHERE key = 'oidc'`).Scan(&oidc); err != nil {
		t.Fatal(err)
	}
	if string(oidc) != `{"enc": ""}` && string(oidc) != `{"enc":""}` {
		t.Fatalf("cleared setting was modified: %s", oidc)
	}

	// key_version records the re-seal where the schema tracks it.
	var appVer, envVer int
	if err := f.pool.QueryRow(ctx,
		`SELECT key_version FROM applications WHERE id::text = $1`, f.appID).Scan(&appVer); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT key_version FROM env_vars WHERE app_id::text = $1`, f.appID).Scan(&envVer); err != nil {
		t.Fatal(err)
	}
	if appVer != 2 || envVer != 2 {
		t.Fatalf("key_version = app %d, env %d; want 2 and 2", appVer, envVer)
	}
}

// Re-running a completed rotation must be a no-op, not a failure — an operator
// who isn't sure whether the last run committed needs a safe way to check.
func TestRotateIsIdempotent(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	oldBox, err := crypto.New(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	f := seed(t, oldBox)
	ctx := context.Background()

	if _, err := Run(ctx, f.pool, oldKey, newK); err != nil {
		t.Fatalf("first rotate: %v", err)
	}
	res, err := Run(ctx, f.pool, oldKey, newK)
	if err != nil {
		t.Fatalf("second rotate: %v", err)
	}
	if !res.AlreadyRotated || res.Total() != 0 {
		t.Fatalf("second run = %+v, want AlreadyRotated with no work", res)
	}
	newBox, err := crypto.New(newK)
	if err != nil {
		t.Fatal(err)
	}
	f.assertReadable(t, newBox)
}

// Supplying the wrong current key must abort before writing anything. Getting
// this wrong would silently destroy every secret on the platform.
func TestRotateWrongOldKeyChangesNothing(t *testing.T) {
	realKey, wrongKey, newK := newKey(t), newKey(t), newKey(t)
	realBox, err := crypto.New(realKey)
	if err != nil {
		t.Fatal(err)
	}
	f := seed(t, realBox)
	ctx := context.Background()

	_, err = Run(ctx, f.pool, wrongKey, newK)
	if !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("err = %v, want ErrKeyMismatch", err)
	}
	// Everything must still be readable with the key it was sealed under.
	f.assertReadable(t, realBox)
}

// A released version stored the alerts webhook as raw ciphertext inside a JSON
// string, which encoding/json corrupted. Those rows decrypt under no key.
// Rotation must skip them and report them, not abort — otherwise an instance
// that ever configured a webhook could never rotate its master key.
func TestRotateSkipsLegacyCorruptWebhook(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	oldBox, err := crypto.New(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	f := seed(t, oldBox)
	ctx := context.Background()

	// A row as the old encoding left it. Raw sealed bytes were written into a
	// JSON string, so what landed in the column is mangled: encoding/json
	// replaced the invalid UTF-8 with the replacement character. (Ciphertext
	// containing a NUL byte could not be stored at all -- Postgres rejects a
	// \\u0000 escape in jsonb -- so the rows that do exist in the wild are
	// exactly the mangled ones modelled here.) The result is not valid base64
	// and decrypts under no key.
	corrupt := []byte(`{"enc": "\ufffd\ufffdnot-base64\ufffd"}`)
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO instance_settings (key, value) VALUES ('notify_webhook', $1)`, corrupt); err != nil {
		t.Fatal(err)
	}

	res, err := Run(ctx, f.pool, oldKey, newK)
	if err != nil {
		t.Fatalf("rotation aborted on a legacy webhook row: %v", err)
	}
	if len(res.Unreadable) != 1 {
		t.Fatalf("Unreadable = %v, want the webhook row reported", res.Unreadable)
	}
	// Every other secret still rotated.
	newBox, err := crypto.New(newK)
	if err != nil {
		t.Fatal(err)
	}
	f.assertReadable(t, newBox)
}

// An empty platform is a legitimate state (fresh install), not an error.
func TestRotateEmptyDatabase(t *testing.T) {
	pool := testPool(t)
	res, err := Run(context.Background(), pool, newKey(t), newKey(t))
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if res.Total() != 0 || res.AlreadyRotated {
		t.Fatalf("result = %+v, want an empty no-op", res)
	}
}
