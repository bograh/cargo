package config

import "testing"

const validMasterKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func envWith(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(envWith(map[string]string{
		"CARGO_DATABASE_URL": "postgres://cargo:cargo@localhost:5432/cargo",
		"CARGO_MASTER_KEY":   validMasterKey,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.DataDir != "/var/lib/cargo" {
		t.Errorf("DataDir = %q, want /var/lib/cargo", cfg.DataDir)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if len(cfg.MasterKey) != 32 {
		t.Errorf("MasterKey len = %d, want 32", len(cfg.MasterKey))
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := Load(envWith(map[string]string{"CARGO_MASTER_KEY": validMasterKey}))
	if err == nil {
		t.Fatal("expected error for missing CARGO_DATABASE_URL")
	}
}

func TestLoadRejectsShortMasterKey(t *testing.T) {
	_, err := Load(envWith(map[string]string{
		"CARGO_DATABASE_URL": "postgres://x",
		"CARGO_MASTER_KEY":   "abcd",
	}))
	if err == nil {
		t.Fatal("expected error for short master key")
	}
}

func TestLoadRejectsInvalidEnv(t *testing.T) {
	_, err := Load(envWith(map[string]string{
		"CARGO_DATABASE_URL": "postgres://x",
		"CARGO_MASTER_KEY":   validMasterKey,
		"CARGO_ENV":          "staging",
	}))
	if err == nil {
		t.Fatal("expected error for invalid CARGO_ENV")
	}
}
