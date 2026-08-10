package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/bograh/cargo/internal/db"
	"github.com/bograh/cargo/internal/keyrotate"
)

// runGenKey prints a fresh 32-byte master key as hex. Kept separate from
// rotation so the operator saves the key somewhere durable *before* anything
// is re-sealed under it — a key generated and lost mid-rotation would take the
// whole platform's secrets with it.
func runGenKey() int {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		fmt.Fprintf(os.Stderr, "generating key: %v\n", err)
		return 1
	}
	fmt.Println(hex.EncodeToString(key))
	return 0
}

const rotateUsage = `cargod rotate-key — re-seal every stored secret under a new master key

Environment:
  CARGO_DATABASE_URL      (required) control-plane database
  CARGO_MASTER_KEY        (required) the CURRENT key, 64 hex chars
  CARGO_NEW_MASTER_KEY    (required) the NEW key, 64 hex chars — see 'cargod gen-key'

Stop the control plane first, run this, then set CARGO_MASTER_KEY to the new
key and start it again. Back up both the database and the new key beforehand.
`

func runRotateKey(ctx context.Context) int {
	dbURL := os.Getenv("CARGO_DATABASE_URL")
	oldHex := os.Getenv("CARGO_MASTER_KEY")
	newHex := os.Getenv("CARGO_NEW_MASTER_KEY")
	if dbURL == "" || oldHex == "" || newHex == "" {
		fmt.Fprint(os.Stderr, rotateUsage)
		return 2
	}
	oldKey, err := decodeKey(oldHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CARGO_MASTER_KEY: %v\n", err)
		return 2
	}
	newKey, err := decodeKey(newHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CARGO_NEW_MASTER_KEY: %v\n", err)
		return 2
	}
	// Rotating to the same key would report success while leaving a suspected
	// leaked key in place — the one outcome an operator must never get.
	if string(oldKey) == string(newKey) {
		fmt.Fprintln(os.Stderr, "CARGO_NEW_MASTER_KEY is identical to CARGO_MASTER_KEY — nothing would change")
		return 2
	}

	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database unavailable: %v\n", err)
		return 1
	}
	defer pool.Close()

	// Rotation deliberately does not migrate: it must never change the schema
	// of a database it is about to rewrite every secret in. Say so plainly
	// rather than surfacing a bare "relation does not exist".
	var migrated bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.applications') IS NOT NULL`).Scan(&migrated); err != nil {
		fmt.Fprintf(os.Stderr, "database unreadable: %v\n", err)
		return 1
	}
	if !migrated {
		fmt.Fprintln(os.Stderr,
			"This database has no Cargo schema yet. Start the control plane once to run\nmigrations, then rotate.")
		return 1
	}

	res, err := keyrotate.Run(ctx, pool, oldKey, newKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotation failed, nothing was changed: %v\n", err)
		return 1
	}
	reportUnreadable(res.Unreadable)
	if res.AlreadyRotated {
		fmt.Println("Every secret is already sealed under the new key — nothing to do.")
		return 0
	}
	fmt.Printf(`Re-sealed %d secret(s):
  registry credentials  %d
  environment variables %d
  database instances    %d
  database attachments  %d
  instance settings     %d

Now set CARGO_MASTER_KEY to the new key and restart the control plane.
The old key no longer opens anything in this database.
`, res.Total(), res.RegistryCreds, res.EnvVars, res.DBInstances, res.DBAttachments, res.Settings)
	return 0
}

// reportUnreadable warns about secrets that were left untouched because they
// decrypt under no key. The operator has to re-enter them; staying silent would
// leave a setting that looks configured but can never be used.
func reportUnreadable(names []string) {
	if len(names) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\nWarning: these stored secrets could not be decrypted and were left unchanged:")
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %s\n", n)
	}
	fmt.Fprintln(os.Stderr,
		"A bug in an earlier version corrupted the alerts webhook on save.\n"+
			"Re-enter it under Admin -> Alerts webhook; nothing else is affected.")
}

func decodeKey(s string) ([]byte, error) {
	key, err := hex.DecodeString(s)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("must decode to 32 bytes (64 hex chars)")
	}
	return key, nil
}
