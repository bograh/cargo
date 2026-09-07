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

func TestMaxMemLimitResolvesToBytes(t *testing.T) {
	base := map[string]string{
		"CARGO_DATABASE_URL": "postgres://cargo:cargo@localhost:5432/cargo",
		"CARGO_MASTER_KEY":   validMasterKey,
	}
	cfg, err := Load(envWith(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxMemBytes != 0 {
		t.Fatalf("unset ceiling should stay 0, got %d", cfg.MaxMemBytes)
	}
	base["CARGO_MAX_MEM_LIMIT"] = "4g"
	cfg, err = Load(envWith(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxMemBytes != 4<<30 {
		t.Fatalf("MaxMemBytes = %d, want %d", cfg.MaxMemBytes, 4<<30)
	}
}

func TestAllowPrivateGitHosts(t *testing.T) {
	base := map[string]string{
		"CARGO_DATABASE_URL": "postgres://cargo:cargo@localhost:5432/cargo",
		"CARGO_MASTER_KEY":   validMasterKey,
	}
	cfg, err := Load(envWith(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowPrivateGitHosts {
		t.Fatal("private git hosts should be off unless asked for")
	}
	base["CARGO_ALLOW_PRIVATE_GIT_HOSTS"] = "true"
	if cfg, err = Load(envWith(base)); err != nil || !cfg.AllowPrivateGitHosts {
		t.Fatalf("cfg.AllowPrivateGitHosts = %v, err = %v", cfg.AllowPrivateGitHosts, err)
	}
	base["CARGO_ALLOW_PRIVATE_GIT_HOSTS"] = "yes please"
	if _, err = Load(envWith(base)); err == nil {
		t.Fatal("a non-boolean should be rejected")
	}
}
