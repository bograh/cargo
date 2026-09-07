package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// memLimitRe matches a docker memory value, e.g. "512m", "1g", "268435456".
var memLimitRe = regexp.MustCompile(`^[1-9][0-9]*[bkmgBKMG]?$`)

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
	// Instance-wide ceiling on the resource caps an app may set for itself.
	// Empty/zero means no ceiling. Without one the per-app caps are advisory:
	// an org admin can raise their own app's memory to whatever they like, so
	// the defaults above bound only the tenants who leave them alone. A
	// single-operator install has nobody to bound, which is why this is off
	// unless set.
	MaxMemLimit string
	// MaxMemBytes is MaxMemLimit resolved to bytes, so no consumer has to
	// re-parse a value this package already validated.
	MaxMemBytes  int64
	MaxCPULimit  float64
	MaxPidsLimit int
	// Instance-wide default deployment strategy ("bluegreen" or "recreate")
	// applied to any app that does not override it.
	DefaultDeployStrategy string
	// Control-plane backup settings.
	PlatformBackupKeep int    // backups retained (default 14)
	ComposeProject     string // compose project for container discovery ("" = self-discover)
	PlatformDBService  string // compose service name of the platform DB (default "db")
	// Disk guardrail: warn/alert below this free-space percentage (default 10).
	DiskMinFreePct float64
	// General API rate limit (requests/sec per user/IP; burst = 2×). Default 20.
	APIRateLimitRPS float64
	// Internal-only listener for the Prometheus /metrics endpoint (default :9090).
	MetricsAddr string
	// Audit-log retention in days (default 180).
	AuditRetentionDays int
	// AllowPrivateGitHosts lets an app clone from a host that resolves inside
	// the deployment (loopback, RFC1918, link-local). Off by default: a repo
	// URL is tenant-controlled and reaches git running on the control plane's
	// network, so leaving it open is a blind SSRF at the metadata endpoint and
	// anything else in reach. Turn it on for an install whose git server is on
	// the same private network — an ordinary self-hosted setup.
	AllowPrivateGitHosts bool
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:         ":8080",
		DatabaseURL:      getenv("CARGO_DATABASE_URL"),
		DataDir:          "/var/lib/cargo",
		Env:              "development",
		DefaultMemLimit:  "512m",
		DefaultCPULimit:  "1",
		DefaultPidsLimit: 512,

		DefaultDeployStrategy: "bluegreen",
		PlatformBackupKeep:    14,
		PlatformDBService:     "db",
		DiskMinFreePct:        10,
		APIRateLimitRPS:       20,
		MetricsAddr:           ":9090",
		AuditRetentionDays:    180,
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
	if v := getenv("CARGO_MAX_MEM_LIMIT"); v != "" {
		if !memLimitRe.MatchString(v) {
			return Config{}, fmt.Errorf("CARGO_MAX_MEM_LIMIT must be a positive integer with an optional b/k/m/g suffix, got %q", v)
		}
		cfg.MaxMemLimit = v
		cfg.MaxMemBytes = memBytes(v)
	}
	if v := getenv("CARGO_MAX_CPU_LIMIT"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			cfg.MaxCPULimit = n
		} else {
			return Config{}, fmt.Errorf("CARGO_MAX_CPU_LIMIT must be a positive number, got %q", v)
		}
	}
	if v := getenv("CARGO_MAX_PIDS_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxPidsLimit = n
		} else {
			return Config{}, fmt.Errorf("CARGO_MAX_PIDS_LIMIT must be a positive integer, got %q", v)
		}
	}
	if v := getenv("CARGO_DEPLOY_STRATEGY"); v != "" {
		if v != "bluegreen" && v != "recreate" {
			return Config{}, fmt.Errorf("CARGO_DEPLOY_STRATEGY must be bluegreen or recreate, got %q", v)
		}
		cfg.DefaultDeployStrategy = v
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
	if v := getenv("CARGO_API_RATELIMIT_RPS"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			cfg.APIRateLimitRPS = n
		} else {
			return Config{}, fmt.Errorf("CARGO_API_RATELIMIT_RPS must be a positive number, got %q", v)
		}
	}
	if v := getenv("CARGO_ALLOW_PRIVATE_GIT_HOSTS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("CARGO_ALLOW_PRIVATE_GIT_HOSTS must be true or false, got %q", v)
		}
		cfg.AllowPrivateGitHosts = b
	}
	if v := getenv("CARGO_METRICS_ADDR"); v != "" {
		cfg.MetricsAddr = v
	}
	if v := getenv("CARGO_AUDIT_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.AuditRetentionDays = n
		} else {
			return Config{}, fmt.Errorf("CARGO_AUDIT_RETENTION_DAYS must be a positive integer, got %q", v)
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

// memUnits maps docker's memory suffixes to their multiplier.
var memUnits = map[byte]int64{'b': 1, 'k': 1 << 10, 'm': 1 << 20, 'g': 1 << 30}

// memBytes converts a docker memory value to bytes. The input must already
// have matched memLimitRe, so it cannot fail.
func memBytes(s string) int64 {
	digits, mult := s, int64(1)
	if m, ok := memUnits[s[len(s)-1]|0x20]; ok { // |0x20 lowercases an ASCII letter
		digits, mult = s[:len(s)-1], m
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}
