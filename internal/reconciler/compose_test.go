package reconciler

import (
	"strings"
	"testing"
)

func TestGenerateCompose(t *testing.T) {
	got := GenerateCompose(Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d1", Port: 3000,
		HealthcheckPath: "/health",
		Domains:         []string{"my-api.apps.example.com", "api.example.com"},
	})
	want := `name: cargo-app-my-api
services:
  app:
    image: app-my-api:d1
    env_file: .env
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-proxy
    labels:
      - traefik.enable=true
      - traefik.http.routers.app-my-api.rule=Host(` + "`my-api.apps.example.com`" + `)
      - traefik.http.routers.app-my-api.entrypoints=websecure
      - traefik.http.routers.app-my-api.tls=true
      - traefik.http.routers.app-my-api.service=app-my-api
      - traefik.http.routers.app-my-api-custom.rule=Host(` + "`api.example.com`" + `)
      - traefik.http.routers.app-my-api-custom.entrypoints=websecure
      - traefik.http.routers.app-my-api-custom.tls.certresolver=le
      - traefik.http.routers.app-my-api-custom.service=app-my-api
      - traefik.http.services.app-my-api.loadbalancer.server.port=3000
networks:
  cargo-proxy:
    external: true
`
	if got != want {
		t.Fatalf("compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGenerateComposeWithLimits(t *testing.T) {
	got := GenerateCompose(Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d1", Port: 3000,
		Domains:     []string{"my-api.apps.example.com"},
		MemoryLimit: "512m", CPULimit: "1.5", PidsLimit: 256,
	})
	want := `name: cargo-app-my-api
services:
  app:
    image: app-my-api:d1
    env_file: .env
    restart: unless-stopped
    mem_limit: 512m
    cpus: 1.5
    pids_limit: 256
    security_opt:
      - no-new-privileges:true
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-proxy
    labels:
      - traefik.enable=true
      - traefik.http.routers.app-my-api.rule=Host(` + "`my-api.apps.example.com`" + `)
      - traefik.http.routers.app-my-api.entrypoints=websecure
      - traefik.http.routers.app-my-api.tls=true
      - traefik.http.routers.app-my-api.service=app-my-api
      - traefik.http.services.app-my-api.loadbalancer.server.port=3000
networks:
  cargo-proxy:
    external: true
`
	if got != want {
		t.Fatalf("compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGenerateComposeMultiNetwork(t *testing.T) {
	got := GenerateCompose(Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d1", Port: 3000,
		HealthcheckPath: "/health",
		Domains:         []string{"my-api.apps.example.com"},
		Networks:        []string{"cargo-proxy", "cargo-data"},
	})
	want := `name: cargo-app-my-api
services:
  app:
    image: app-my-api:d1
    env_file: .env
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-proxy
      - cargo-data
    labels:
      - traefik.enable=true
      - traefik.http.routers.app-my-api.rule=Host(` + "`my-api.apps.example.com`" + `)
      - traefik.http.routers.app-my-api.entrypoints=websecure
      - traefik.http.routers.app-my-api.tls=true
      - traefik.http.routers.app-my-api.service=app-my-api
      - traefik.http.services.app-my-api.loadbalancer.server.port=3000
networks:
  cargo-proxy:
    external: true
  cargo-data:
    external: true
`
	if got != want {
		t.Fatalf("compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A blue/green render scopes only the compose project name by color. The
// Traefik router and service names stay color-free so both colors land in one
// load-balanced pool, and the service healthcheck keeps a booting container out
// of that pool.
func TestGenerateComposeBlueGreen(t *testing.T) {
	got := GenerateCompose(Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d2", Port: 3000,
		HealthcheckPath: "/health",
		Domains:         []string{"my-api.apps.example.com"},
		BlueGreen:       true, Color: "green",
	})
	want := `name: cargo-app-my-api-green
services:
  app:
    image: app-my-api:d2
    env_file: .env
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-proxy
    labels:
      - traefik.enable=true
      - traefik.http.routers.app-my-api.rule=Host(` + "`my-api.apps.example.com`" + `)
      - traefik.http.routers.app-my-api.entrypoints=websecure
      - traefik.http.routers.app-my-api.tls=true
      - traefik.http.routers.app-my-api.service=app-my-api
      - traefik.http.services.app-my-api.loadbalancer.server.port=3000
      - traefik.http.services.app-my-api.loadbalancer.healthcheck.path=/health
      - traefik.http.services.app-my-api.loadbalancer.healthcheck.interval=3s
      - traefik.http.services.app-my-api.loadbalancer.healthcheck.timeout=2s
networks:
  cargo-proxy:
    external: true
`
	if got != want {
		t.Fatalf("compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Both colors of one app must agree on the router and service names (that is
// what makes the hand-off seamless) and disagree on the project name (that is
// what lets them run at the same time).
func TestGenerateComposeColorsShareTraefikNames(t *testing.T) {
	spec := Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d1", Port: 3000,
		HealthcheckPath: "/health",
		Domains:         []string{"my-api.apps.example.com"},
		BlueGreen:       true,
	}
	spec.Color = "blue"
	blue := GenerateCompose(spec)
	spec.Color = "green"
	green := GenerateCompose(spec)

	if !strings.Contains(blue, "name: cargo-app-my-api-blue") ||
		!strings.Contains(green, "name: cargo-app-my-api-green") {
		t.Fatal("colors must use distinct compose project names")
	}
	const svc = "traefik.http.services.app-my-api.loadbalancer.server.port=3000"
	if !strings.Contains(blue, svc) || !strings.Contains(green, svc) {
		t.Fatal("colors must share one Traefik service so Traefik pools them")
	}
}

// Without a healthcheck path there is nothing for Traefik to probe, so the
// healthcheck labels must be omitted rather than rendered empty (an empty path
// would make Traefik mark every backend down and serve 503s).
func TestGenerateComposeBlueGreenNoHealthcheckPath(t *testing.T) {
	got := GenerateCompose(Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d2", Port: 3000,
		Domains:   []string{"my-api.apps.example.com"},
		BlueGreen: true, Color: "blue",
	})
	if strings.Contains(got, "loadbalancer.healthcheck") {
		t.Fatalf("healthcheck labels emitted without a path:\n%s", got)
	}
}

// The recreate strategy must render exactly as it did before blue/green
// existed, so an in-place upgrade doesn't churn every running app's project.
func TestGenerateComposeRecreateUnchangedByBlueGreen(t *testing.T) {
	got := GenerateCompose(Spec{
		AppID: "a1", Slug: "my-api", Image: "app-my-api:d1", Port: 3000,
		HealthcheckPath: "/health",
		Domains:         []string{"my-api.apps.example.com"},
	})
	if !strings.Contains(got, "name: cargo-app-my-api\n") {
		t.Fatal("recreate strategy must keep the unsuffixed project name")
	}
	if strings.Contains(got, "loadbalancer.healthcheck") {
		t.Fatal("recreate strategy must not gain healthcheck labels")
	}
}

func TestNextColor(t *testing.T) {
	// A legacy app (no color yet) moves to blue first, then alternates.
	for _, tc := range []struct{ active, want string }{
		{"", "blue"},
		{"blue", "green"},
		{"green", "blue"},
	} {
		if got := nextColor(tc.active); got != tc.want {
			t.Errorf("nextColor(%q) = %q, want %q", tc.active, got, tc.want)
		}
	}
}

func TestGenerateDBComposePostgresNoPort(t *testing.T) {
	got := GenerateDBCompose(DBSpec{
		InstanceID: "abc123", Engine: "postgres", Version: "16",
		AdminPass: "supersecret",
	})
	want := `name: cargo-db-abc123
services:
  db:
    image: postgres:16
    container_name: cargo-db-abc123
    env_file: .env
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-data
    volumes:
      - data:/var/lib/postgresql/data
volumes:
  data:
networks:
  cargo-data:
    external: true
`
	if got != want {
		t.Fatalf("db compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if strings.Contains(got, "supersecret") {
		t.Fatal("secret leaked into compose.yaml")
	}
}

func TestGenerateDBComposePostgresWithPort(t *testing.T) {
	got := GenerateDBCompose(DBSpec{
		InstanceID: "abc123", Engine: "postgres", Version: "16",
		AdminPass: "supersecret", HostPort: 55432,
	})
	want := `name: cargo-db-abc123
services:
  db:
    image: postgres:16
    container_name: cargo-db-abc123
    env_file: .env
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-data
    volumes:
      - data:/var/lib/postgresql/data
    ports:
      - "55432:5432"
volumes:
  data:
networks:
  cargo-data:
    external: true
`
	if got != want {
		t.Fatalf("db compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if strings.Contains(got, "supersecret") {
		t.Fatal("secret leaked into compose.yaml")
	}
}

func TestGenerateDBComposeRedis(t *testing.T) {
	got := GenerateDBCompose(DBSpec{
		InstanceID: "xyz789", Engine: "redis", Version: "7",
		AdminPass: "redispass", HostPort: 16379,
	})
	want := `name: cargo-db-xyz789
services:
  db:
    image: redis:7
    container_name: cargo-db-xyz789
    env_file: .env
    command: ["redis-server", "/etc/cargo/redis.conf"]
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - cargo-data
    volumes:
      - data:/data
      - ./redis.conf:/etc/cargo/redis.conf:ro
    ports:
      - "16379:6379"
volumes:
  data:
networks:
  cargo-data:
    external: true
`
	if got != want {
		t.Fatalf("db compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if strings.Contains(got, "redispass") {
		t.Fatal("secret leaked into compose.yaml")
	}
}

func TestGenerateEnvFile(t *testing.T) {
	out, err := generateEnvFile(map[string]string{"B": "2", "A": "1"})
	if err != nil || out != "A=1\nB=2\n" {
		t.Fatalf("env = %q, %v", out, err)
	}
	if _, err := generateEnvFile(map[string]string{"X": "a\nb"}); err == nil {
		t.Fatal("newline value accepted")
	}
	if _, err := generateEnvFile(map[string]string{"OK\nINJECTED": "1"}); err == nil {
		t.Fatal("expected a newline in a key to be rejected")
	}
}
