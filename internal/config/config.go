package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	MasterKey   []byte
	DataDir     string
	Env         string
	// Instance-wide default container resource caps applied to any app that
	// does not override them. Rendered into each app's compose file.
	DefaultMemLimit  string
	DefaultCPULimit  string
	DefaultPidsLimit int
	// Control-plane backup settings.
	PlatformBackupKeep int    // backups retained (default 14)
	ComposeProject     string // compose project for container discovery ("" = self-discover)
	PlatformDBService  string // compose service name of the platform DB (default "db")
	// Disk guardrail: warn/alert below this free-space percentage (default 10).
	DiskMinFreePct float64
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:           ":8080",
		DatabaseURL:        getenv("CARGO_DATABASE_URL"),
		DataDir:            "/var/lib/cargo",
		Env:                "development",
		DefaultMemLimit:    "512m",
		DefaultCPULimit:    "1",
		DefaultPidsLimit:   512,
		PlatformBackupKeep: 14,
		PlatformDBService:  "db",
		DiskMinFreePct:     10,
	}
	if v := getenv("CARGO_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := getenv("CARGO_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := getenv("CARGO_ENV"); v != "" {
		cfg.Env = v
	}
	if v := getenv("CARGO_DEFAULT_MEM_LIMIT"); v != "" {
		cfg.DefaultMemLimit = v
	}
	if v := getenv("CARGO_DEFAULT_CPU_LIMIT"); v != "" {
		cfg.DefaultCPULimit = v
	}
	if v := getenv("CARGO_DEFAULT_PIDS_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DefaultPidsLimit = n
		} else {
			return Config{}, fmt.Errorf("CARGO_DEFAULT_PIDS_LIMIT must be a positive integer, got %q", v)
		}
	}
	if v := getenv("CARGO_PLATFORM_BACKUP_KEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PlatformBackupKeep = n
		} else {
			return Config{}, fmt.Errorf("CARGO_PLATFORM_BACKUP_KEEP must be a positive integer, got %q", v)
		}
	}
	if v := getenv("CARGO_COMPOSE_PROJECT"); v != "" {
		cfg.ComposeProject = v
	}
	if v := getenv("CARGO_PLATFORM_DB_CONTAINER"); v != "" {
		cfg.PlatformDBService = v
	}
	if v := getenv("CARGO_DISK_MIN_FREE_PCT"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 && n < 100 {
			cfg.DiskMinFreePct = n
		} else {
			return Config{}, fmt.Errorf("CARGO_DISK_MIN_FREE_PCT must be a number between 0 and 100, got %q", v)
		}
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("CARGO_DATABASE_URL is required")
	}
	keyHex := getenv("CARGO_MASTER_KEY")
	if keyHex == "" {
		return Config{}, errors.New("CARGO_MASTER_KEY is required (64 hex chars)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("CARGO_MASTER_KEY must decode to 32 bytes")
	}
	cfg.MasterKey = key
	if cfg.Env != "development" && cfg.Env != "production" {
		return Config{}, fmt.Errorf("CARGO_ENV must be development or production, got %q", cfg.Env)
	}
	return cfg, nil
}
