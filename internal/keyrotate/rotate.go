// Package keyrotate re-seals every secret Cargo holds at rest under a new
// master key.
//
// Without it, CARGO_MASTER_KEY is forever: a key you suspect has leaked cannot
// be replaced without losing every env var, registry credential, database
// password, and instance setting on the platform. Rotation turns "back up the
// key and never lose it" into a recoverable story.
//
// Secrets live in two shapes, both sealed by internal/crypto:
//
//   - raw BYTEA columns — applications.registry_creds_enc, env_vars.value_enc
//   - JSONB {"enc": base64} wrappers — database_instances.admin_secret,
//     database_attachments.secret, and the encrypted instance_settings rows
//
// Everything happens in one transaction, so a failure part-way through leaves
// the database sealed under the old key rather than half-rotated.
package keyrotate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// encryptedSettingKeys are the instance_settings rows whose value is a sealed
// {"enc": base64} wrapper. Every other row (the apps-domain suffix, disk
// status, and other bookkeeping) is plaintext and must be left alone.
var encryptedSettingKeys = []string{"smtp", "github_app", "oidc", "notify_webhook"}

// Result counts what was re-sealed, per location.
type Result struct {
	RegistryCreds int
	EnvVars       int
	DBInstances   int
	DBAttachments int
	Settings      int
	HostKeys      int
	// AlreadyRotated is true when every secret was already readable with the
	// new key and none with the old — a rerun of a rotation that succeeded.
	AlreadyRotated bool
	// Unreadable names secrets that could not be decrypted with either key and
	// were left as-is rather than aborting the rotation. Only values known to
	// have been corrupted by a past storage bug qualify; see tolerateUnreadable.
	Unreadable []string
}

func (r Result) Total() int {
	return r.RegistryCreds + r.EnvVars + r.DBInstances + r.DBAttachments + r.Settings + r.HostKeys
}

// ErrKeyMismatch means at least one secret could not be decrypted with either
// key, so the supplied old key is not the one the database was sealed with.
var ErrKeyMismatch = errors.New("a stored secret could not be decrypted with the supplied key")

// secret is one encrypted value found in the database, addressed well enough
// to write it back.
type secret struct {
	table string // for error messages
	// update is called with the re-sealed bytes, in the shape the column wants.
	update func(ctx context.Context, tx pgx.Tx, resealed []byte) error
	sealed []byte // raw sealed bytes (already unwrapped from JSONB if needed)
	// wrap re-wraps resealed bytes for storage; nil means "store raw".
	wrap func([]byte) ([]byte, error)
	slot *int // which Result counter this belongs to
	// tolerateUnreadable allows this secret to be skipped instead of aborting
	// the rotation when it decrypts with neither key. Set only for values a
	// released version is known to have stored corrupted, where failing hard
	// would leave the operator permanently unable to rotate.
	tolerateUnreadable bool
}

// wrapJSON re-wraps sealed bytes as the {"enc": base64} JSONB shape.
func wrapJSON(sealed []byte) ([]byte, error) {
	return json.Marshal(map[string]string{"enc": base64.StdEncoding.EncodeToString(sealed)})
}

// unwrapJSON extracts sealed bytes from an {"enc": base64} value. ok is false
// for a cleared setting ({"enc": ""}), which holds no secret to rotate.
func unwrapJSON(raw []byte) (sealed []byte, ok bool, err error) {
	var w struct {
		Enc string `json:"enc"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, false, err
	}
	if w.Enc == "" {
		return nil, false, nil
	}
	sealed, err = base64.StdEncoding.DecodeString(w.Enc)
	if err != nil {
		return nil, false, err
	}
	return sealed, true, nil
}

// Run re-seals every secret under newKey. It is safe to re-run: a rotation
// that already completed reports AlreadyRotated and changes nothing.
func Run(ctx context.Context, pool *pgxpool.Pool, oldKey, newKey []byte) (Result, error) {
	var res Result
	oldBox, err := crypto.New(oldKey)
	if err != nil {
		return res, fmt.Errorf("old key: %w", err)
	}
	newBox, err := crypto.New(newKey)
	if err != nil {
		return res, fmt.Errorf("new key: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	secrets, err := collect(ctx, tx, &res)
	if err != nil {
		return res, err
	}
	if len(secrets) == 0 {
		return res, tx.Commit(ctx)
	}

	// Classify before writing anything. A secret that opens with neither key
	// means the operator supplied the wrong old key, and continuing would
	// silently drop it.
	// Anything already sealed under the new key is skipped rather than
	// rejected, so a rotation interrupted before the operator swapped keys can
	// simply be re-run.
	var pending []secret
	var unreadable []string
	for _, s := range secrets {
		if _, err := oldBox.Open(s.sealed); err == nil {
			pending = append(pending, s)
			continue
		}
		if _, err := newBox.Open(s.sealed); err == nil {
			continue
		}
		if s.tolerateUnreadable {
			unreadable = append(unreadable, s.table)
			continue
		}
		return Result{}, fmt.Errorf("%w: %s", ErrKeyMismatch, s.table)
	}
	// collect() may already have recorded unreadable legacy rows, so merge
	// rather than replace.
	res.Unreadable = append(res.Unreadable, unreadable...)
	if len(pending) == 0 {
		return Result{AlreadyRotated: true, Unreadable: res.Unreadable}, tx.Commit(ctx)
	}

	for _, s := range pending {
		plain, err := oldBox.Open(s.sealed)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", s.table, err)
		}
		resealed, err := newBox.Seal(plain)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", s.table, err)
		}
		// Verify before storing: a value that cannot be read back with the new
		// key must never replace one that is still readable with the old.
		if roundTripped, err := newBox.Open(resealed); err != nil {
			return Result{}, fmt.Errorf("%s: re-sealed value failed verification: %w", s.table, err)
		} else if string(roundTripped) != string(plain) {
			return Result{}, fmt.Errorf("%s: re-sealed value did not round-trip", s.table)
		}
		stored := resealed
		if s.wrap != nil {
			if stored, err = s.wrap(resealed); err != nil {
				return Result{}, fmt.Errorf("%s: %w", s.table, err)
			}
		}
		if err := s.update(ctx, tx, stored); err != nil {
			return Result{}, fmt.Errorf("%s: %w", s.table, err)
		}
		*s.slot++
	}

	// Bump key_version where the schema tracks it, so a row's provenance is
	// visible even though Cargo only ever decrypts with the current key.
	if _, err := tx.Exec(ctx,
		`UPDATE applications SET key_version = key_version + 1 WHERE registry_creds_enc IS NOT NULL`); err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE env_vars SET key_version = key_version + 1`); err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE hosts SET key_version = key_version + 1 WHERE private_key_enc IS NOT NULL`); err != nil {
		return Result{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return res, nil
}

// collect gathers every encrypted value in the database.
func collect(ctx context.Context, tx pgx.Tx, res *Result) ([]secret, error) {
	var out []secret

	// applications.registry_creds_enc — raw BYTEA, nullable.
	rows, err := tx.Query(ctx,
		`SELECT id::text, registry_creds_enc FROM applications WHERE registry_creds_enc IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	type appRow struct {
		id     string
		sealed []byte
	}
	var appRows []appRow
	for rows.Next() {
		var r appRow
		if err := rows.Scan(&r.id, &r.sealed); err != nil {
			rows.Close()
			return nil, err
		}
		appRows = append(appRows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range appRows {
		out = append(out, secret{
			table: "applications.registry_creds_enc", sealed: r.sealed, slot: &res.RegistryCreds,
			update: func(ctx context.Context, tx pgx.Tx, b []byte) error {
				_, err := tx.Exec(ctx, `UPDATE applications SET registry_creds_enc = $2 WHERE id::text = $1`, r.id, b)
				return err
			},
		})
	}

	// hosts.private_key_enc — raw BYTEA, nullable. Worker SSH keys rotate
	// like any other secret; a host whose key cannot be re-sealed aborts the
	// rotation (it is recoverable by re-entering the key, but rotation should
	// not silently strand it).
	hostRowsRows, err := tx.Query(ctx,
		`SELECT id::text, private_key_enc FROM hosts WHERE private_key_enc IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	type hostRow struct {
		id     string
		sealed []byte
	}
	var hostRows []hostRow
	for hostRowsRows.Next() {
		var r hostRow
		if err := hostRowsRows.Scan(&r.id, &r.sealed); err != nil {
			hostRowsRows.Close()
			return nil, err
		}
		hostRows = append(hostRows, r)
	}
	hostRowsRows.Close()
	if err := hostRowsRows.Err(); err != nil {
		return nil, err
	}
	for _, r := range hostRows {
		out = append(out, secret{
			table: "hosts.private_key_enc", sealed: r.sealed, slot: &res.HostKeys,
			update: func(ctx context.Context, tx pgx.Tx, b []byte) error {
				_, err := tx.Exec(ctx, `UPDATE hosts SET private_key_enc = $2 WHERE id::text = $1`, r.id, b)
				return err
			},
		})
	}

	// env_vars.value_enc — raw BYTEA, keyed by (app_id, key).
	rows, err = tx.Query(ctx, `SELECT app_id::text, key, value_enc FROM env_vars`)
	if err != nil {
		return nil, err
	}
	type envRow struct {
		appID, key string
		sealed     []byte
	}
	var envRows []envRow
	for rows.Next() {
		var r envRow
		if err := rows.Scan(&r.appID, &r.key, &r.sealed); err != nil {
			rows.Close()
			return nil, err
		}
		envRows = append(envRows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range envRows {
		out = append(out, secret{
			table: "env_vars.value_enc", sealed: r.sealed, slot: &res.EnvVars,
			update: func(ctx context.Context, tx pgx.Tx, b []byte) error {
				_, err := tx.Exec(ctx,
					`UPDATE env_vars SET value_enc = $3 WHERE app_id::text = $1 AND key = $2`, r.appID, r.key, b)
				return err
			},
		})
	}

	// JSONB {"enc": base64} columns.
	jsonCols := []struct {
		table, col, idCol string
		slot              *int
	}{
		{"database_instances", "admin_secret", "id", &res.DBInstances},
		{"database_attachments", "secret", "id", &res.DBAttachments},
	}
	for _, c := range jsonCols {
		q := fmt.Sprintf(`SELECT %s::text, %s FROM %s WHERE %s IS NOT NULL`, c.idCol, c.col, c.table, c.col)
		rows, err := tx.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		type jrow struct {
			id  string
			raw []byte
		}
		var jrows []jrow
		for rows.Next() {
			var r jrow
			if err := rows.Scan(&r.id, &r.raw); err != nil {
				rows.Close()
				return nil, err
			}
			jrows = append(jrows, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, r := range jrows {
			sealed, ok, err := unwrapJSON(r.raw)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", c.table, c.col, err)
			}
			if !ok {
				continue
			}
			update := fmt.Sprintf(`UPDATE %s SET %s = $2 WHERE %s::text = $1`, c.table, c.col, c.idCol)
			out = append(out, secret{
				table: c.table + "." + c.col, sealed: sealed, slot: c.slot, wrap: wrapJSON,
				update: func(ctx context.Context, tx pgx.Tx, b []byte) error {
					_, err := tx.Exec(ctx, update, r.id, b)
					return err
				},
			})
		}
	}

	// instance_settings — only the known-encrypted keys.
	rows, err = tx.Query(ctx,
		`SELECT key, value FROM instance_settings WHERE key = ANY($1)`, encryptedSettingKeys)
	if err != nil {
		return nil, err
	}
	type setRow struct {
		key string
		raw []byte
	}
	var setRows []setRow
	for rows.Next() {
		var r setRow
		if err := rows.Scan(&r.key, &r.raw); err != nil {
			rows.Close()
			return nil, err
		}
		setRows = append(setRows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range setRows {
		// Cargo stored the alerts webhook as raw ciphertext inside a JSON string
		// before the base64 fix, which encoding/json corrupted on the way in.
		// Such a row decrypts under no key and often is not even valid base64;
		// aborting on it would leave affected instances unable to rotate at all.
		legacyCorruptible := r.key == "notify_webhook"
		sealed, ok, err := unwrapJSON(r.raw)
		if err != nil {
			if legacyCorruptible {
				res.Unreadable = append(res.Unreadable, "instance_settings["+r.key+"]")
				continue
			}
			return nil, fmt.Errorf("instance_settings[%s]: %w", r.key, err)
		}
		if !ok {
			continue
		}
		out = append(out, secret{
			table: "instance_settings[" + r.key + "]", sealed: sealed, slot: &res.Settings, wrap: wrapJSON,
			tolerateUnreadable: legacyCorruptible,
			update: func(ctx context.Context, tx pgx.Tx, b []byte) error {
				_, err := tx.Exec(ctx, `UPDATE instance_settings SET value = $2 WHERE key = $1`, r.key, b)
				return err
			},
		})
	}

	return out, nil
}
