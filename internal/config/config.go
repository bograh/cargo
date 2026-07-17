package config

import (
	"encoding/hex"
	"errors"
	"fmt"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	MasterKey   []byte
	DataDir     string
	Env         string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:    ":8080",
		DatabaseURL: getenv("CARGO_DATABASE_URL"),
		DataDir:     "/var/lib/cargo",
		Env:         "development",
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
