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
}
