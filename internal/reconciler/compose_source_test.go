package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each case below is a confirmed bypass of validating the compose file as
// *written*: compose resolves something afterwards that the source text does
// not show. They are driven through the real `docker compose config` to prove
// the rendered-configuration gate closes them.
func TestValidateComposeSourceClosesRenderBypasses(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	cases := []struct {
		name       string
		compose    string
		envFile    string
		extraFile  string
		wantSubstr string
	}{
		{
			// The worst of them: Cargo writes the app's own environment into the
			// project .env, so a tenant could set this variable through the
			// env-var UI and expand it into a bind mount of the host root.
			name:       "interpolated bind mount",
			compose:    "services:\n  web:\n    image: nginx:alpine\n    volumes:\n      - ${HOSTPATH}:/host\n",
			envFile:    "HOSTPATH=/\n",
			wantSubstr: "outside the repository",
		},
		{
			name:       "extends pulls privileged from another file",
			compose:    "services:\n  web:\n    extends:\n      file: base.yml\n      service: evil\n",
			extraFile:  "services:\n  evil:\n    image: nginx:alpine\n    privileged: true\n",
			wantSubstr: "privileged",
		},
		{
			// A "named" volume that is really a bind mount. The service's own
			// reference to it looks entirely ordinary.
			name: "named volume with bind driver_opts",
			compose: "services:\n  web:\n    image: nginx:alpine\n    volumes:\n      - sneaky:/host-etc\n" +
				"volumes:\n  sneaky:\n    driver: local\n    driver_opts:\n      type: none\n      device: /etc\n      o: bind\n",
			wantSubstr: "driver_opts",
		},
		{
			name: "secret reads a host file",
			compose: "services:\n  web:\n    image: nginx:alpine\n    secrets:\n      - hostfile\n" +
				"secrets:\n  hostfile:\n    file: /etc/hostname\n",
			wantSubstr: "outside the repository",
		},
		{
			// Compose appends security_opt on merge, so this survives alongside
			// Cargo's no-new-privileges and hands back syscall filtering.
			name:       "security_opt disables seccomp",
			compose:    "services:\n  web:\n    image: nginx:alpine\n    security_opt:\n      - seccomp:unconfined\n",
			wantSubstr: "security_opt",
		},
		{
			name:       "yaml anchor hides a directive",
			compose:    "x-base: &base\n  privileged: true\nservices:\n  web:\n    image: nginx:alpine\n    <<: *base\n",
			wantSubstr: "privileged",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			if err := os.WriteFile(filepath.Join(src, "docker-compose.yml"), []byte(tc.compose), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.envFile != "" {
				if err := os.WriteFile(filepath.Join(src, ".env"), []byte(tc.envFile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.extraFile != "" {
				if err := os.WriteFile(filepath.Join(src, "base.yml"), []byte(tc.extraFile), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			d := NewDocker(t.TempDir())
			spec := Spec{
				AppID: "v-1", Slug: "v-test", Port: 80,
				ComposeFile: "docker-compose.yml", ComposeService: "web", SourceDir: src,
			}
			err := d.validateComposeSource(context.Background(), spec)
			if err == nil {
				t.Fatalf("bypass NOT closed: compose config for %q validated clean", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q should mention %q", err, tc.wantSubstr)
			}
		})
	}
}

// The gate must not become so strict that ordinary multi-service apps stop
// working — a rejection here costs a user their deploy.
func TestValidateComposeSourceAcceptsRealisticApp(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := `services:
  web:
    image: nginx:alpine
    depends_on: [db, cache]
    environment:
      - DATABASE_URL=postgres://app@db/app
    volumes:
      - ./config:/etc/nginx/conf.d:ro
  worker:
    image: nginx:alpine
    command: ["sleep", "infinity"]
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: local
    volumes:
      - pgdata:/var/lib/postgresql/data
  cache:
    image: redis:7-alpine
volumes:
  pgdata:
`
	if err := os.WriteFile(filepath.Join(src, "docker-compose.yml"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewDocker(t.TempDir())
	spec := Spec{
		AppID: "v-2", Slug: "v-ok", Port: 80,
		ComposeFile: "docker-compose.yml", ComposeService: "web", SourceDir: src,
	}
	if err := d.validateComposeSource(context.Background(), spec); err != nil {
		t.Fatalf("realistic multi-service app rejected: %v", err)
	}
}

// A malformed compose file should report what compose actually said, not a
// bare exit status the user cannot act on.
func TestValidateComposeSourceSurfacesComposeError(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "docker-compose.yml"),
		[]byte("services:\n  web:\n    extends:\n      file: missing.yml\n      service: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := NewDocker(t.TempDir())
	err := d.validateComposeSource(context.Background(), Spec{
		AppID: "v-3", Slug: "v-err", ComposeFile: "docker-compose.yml",
		ComposeService: "web", SourceDir: src,
	})
	if err == nil {
		t.Fatal("malformed compose file accepted")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Fatalf("error is not actionable: %v", err)
	}
	if !strings.Contains(err.Error(), "missing.yml") {
		t.Fatalf("error should surface compose's own message, got: %v", err)
	}
}
