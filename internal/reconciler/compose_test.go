package reconciler

import "testing"

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
      - traefik.http.routers.app-my-api.rule=Host(` + "`my-api.apps.example.com`" + `) || Host(` + "`api.example.com`" + `)
      - traefik.http.routers.app-my-api.entrypoints=websecure
      - traefik.http.routers.app-my-api.tls=true
      - traefik.http.services.app-my-api.loadbalancer.server.port=3000
networks:
  cargo-proxy:
    external: true
`
	if got != want {
		t.Fatalf("compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
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
