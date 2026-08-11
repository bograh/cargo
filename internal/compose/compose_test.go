package compose

import (
	"errors"
	"strings"
	"testing"
)

const safeFile = `
services:
  web:
    build: .
    environment:
      - NODE_ENV=production
    depends_on: [db]
  db:
    image: postgres:16
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata:
`

// root is a stand-in for the app's checkout: the directory a compose file is
// allowed to reference.
func root(t *testing.T) string { t.Helper(); return t.TempDir() }

func TestValidateAcceptsOrdinaryFile(t *testing.T) {
	if err := Validate([]byte(safeFile), root(t)); err != nil {
		t.Fatalf("rejected a safe compose file: %v", err)
	}
}

func TestParseListsServices(t *testing.T) {
	names, err := Parse([]byte(safeFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "db" || names[1] != "web" {
		t.Fatalf("services = %v, want [db web] sorted", names)
	}
}

// Each of these would let a tenant out of the isolation that 10.1/10.2 built.
// An overlay can only add to a service, so they have to be refused up front.
func TestValidateRejectsHostEscapes(t *testing.T) {
	for _, tc := range []struct{ name, yaml, wantSubstr string }{
		{"privileged", "services:\n  web:\n    image: x\n    privileged: true\n", "privileged"},
		{"network host", "services:\n  web:\n    image: x\n    network_mode: host\n", "network_mode: host"},
		{"network container", "services:\n  web:\n    image: x\n    network_mode: \"container:other\"\n", "container"},
		{"pid host", "services:\n  web:\n    image: x\n    pid: host\n", "pid: host"},
		{"ipc host", "services:\n  web:\n    image: x\n    ipc: host\n", "ipc: host"},
		{"userns host", "services:\n  web:\n    image: x\n    userns_mode: host\n", "userns_mode"},
		{"cap_add", "services:\n  web:\n    image: x\n    cap_add: [SYS_ADMIN]\n", "cap_add"},
		{"devices", "services:\n  web:\n    image: x\n    devices:\n      - /dev/sda:/dev/sda\n", "devices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.yaml), root(t))
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("err = %v, want ErrUnsafe", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q should name the offending directive %q", err, tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), `"web"`) {
				t.Fatalf("error %q should name the offending service", err)
			}
		})
	}
}

// The docker socket is the worst case: mounting it hands the tenant the whole
// host and the control plane with it.
func TestValidateRejectsDockerSocketMount(t *testing.T) {
	err := Validate([]byte("services:\n  web:\n    image: x\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n"), root(t))
	if !errors.Is(err, ErrUnsafe) {
		t.Fatalf("err = %v, want ErrUnsafe", err)
	}
	if !strings.Contains(err.Error(), "/var/run/docker.sock") {
		t.Fatalf("error should name the path: %v", err)
	}
}

func TestValidateVolumeSources(t *testing.T) {
	for _, tc := range []struct {
		name, volume string
		wantUnsafe   bool
	}{
		// Named volumes and paths inside the repository are fine.
		{"named volume", "pgdata:/var/lib/postgresql/data", false},
		// A lone container path with no colon is an anonymous volume, not a
		// bind — the leading slash is the *container* side.
		{"anonymous volume", "/var/lib/postgresql/data", false},
		{"relative path", "./config:/etc/app/config:ro", false},
		{"host absolute", "/etc/passwd:/etc/passwd:ro", true},
		{"home dir", "~/.ssh:/root/.ssh", true},
		{"escaping relative", "../../etc:/etc", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			y := "services:\n  web:\n    image: x\n    volumes:\n      - \"" + tc.volume + "\"\n"
			err := Validate([]byte(y), root(t))
			if tc.wantUnsafe && !errors.Is(err, ErrUnsafe) {
				t.Fatalf("volume %q accepted, want rejected (err=%v)", tc.volume, err)
			}
			if !tc.wantUnsafe && err != nil {
				t.Fatalf("volume %q rejected: %v", tc.volume, err)
			}
		})
	}
}

// Long-form volume syntax must be checked too, or the short-form rules are
// trivially bypassed.
func TestValidateLongFormBindMount(t *testing.T) {
	y := `
services:
  web:
    image: x
    volumes:
      - type: bind
        source: /etc
        target: /host-etc
`
	if err := Validate([]byte(y), root(t)); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("long-form bind mount accepted: %v", err)
	}
	// The same syntax naming a named volume is fine.
	ok := `
services:
  web:
    image: x
    volumes:
      - type: volume
        source: data
        target: /data
`
	if err := Validate([]byte(ok), root(t)); err != nil {
		t.Fatalf("long-form named volume rejected: %v", err)
	}
}

// Apps are reached through Traefik; a published port would also collide with
// every other app on a single-host install.
func TestValidateRejectsPublishedPorts(t *testing.T) {
	err := Validate([]byte("services:\n  web:\n    image: x\n    ports:\n      - \"8080:80\"\n"), root(t))
	if !errors.Is(err, ErrUnsafe) {
		t.Fatalf("err = %v, want ErrUnsafe", err)
	}
	if !strings.Contains(err.Error(), "exposed port") {
		t.Fatalf("error should tell the user what to do instead: %v", err)
	}
}

func TestValidateRejectsUnparseableAndEmpty(t *testing.T) {
	if err := Validate([]byte("services: [oh no\n"), root(t)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if err := Validate([]byte("version: \"3\"\n"), root(t)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a file with no services should be ErrInvalid, got %v", err)
	}
}

// A file with several problems must report the same one every time, so a user
// fixing them one at a time gets a stable sequence.
func TestValidateIsDeterministic(t *testing.T) {
	y := "services:\n  zeta:\n    image: x\n    privileged: true\n  alpha:\n    image: y\n    pid: host\n"
	first := Validate([]byte(y), root(t)).Error()
	for i := 0; i < 20; i++ {
		if got := Validate([]byte(y), root(t)).Error(); got != first {
			t.Fatalf("error varies between runs: %q vs %q", got, first)
		}
	}
	if !strings.Contains(first, "alpha") {
		t.Fatalf("expected services checked in sorted order, got %q", first)
	}
}

func TestGenerateOverlay(t *testing.T) {
	got := GenerateOverlay(OverlaySpec{
		Slug: "my-api", Service: "web", Port: 3000,
		Domains:     []string{"my-api.apps.example.com", "api.example.com"},
		MemoryLimit: "512m", CPULimit: "1", PidsLimit: 256,
	})
	want := `name: cargo-app-my-api
services:
  web:
    env_file: .env
    restart: unless-stopped
    mem_limit: 512m
    cpus: 1
    pids_limit: 256
    security_opt:
      - no-new-privileges:true
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    networks:
      - default
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
		t.Fatalf("overlay mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The overlay must name `default` alongside cargo-proxy: attaching a service to
// any network removes compose's implicit default, which would cut the web
// service off from the database in the user's own file.
func TestOverlayKeepsDefaultNetwork(t *testing.T) {
	got := GenerateOverlay(OverlaySpec{
		Slug: "s", Service: "web", Port: 80, Domains: []string{"s.apps.example.com"},
	})
	if !strings.Contains(got, "    networks:\n      - default\n      - cargo-proxy\n") {
		t.Fatalf("overlay must keep the default network:\n%s", got)
	}
}

// The overlay only ever targets the web service — capping or relabelling a
// database the user configured themselves would be a surprising side effect.
func TestOverlayTouchesOnlyTheWebService(t *testing.T) {
	got := GenerateOverlay(OverlaySpec{
		Slug: "s", Service: "web", Port: 80, Domains: []string{"s.apps.example.com"},
		MemoryLimit: "256m",
	})
	if strings.Contains(got, "db:") {
		t.Fatalf("overlay should not mention other services:\n%s", got)
	}
	if strings.Count(got, "mem_limit") != 1 {
		t.Fatalf("overlay should cap exactly one service:\n%s", got)
	}
}

// env_file and label_file read a file's contents into the container, so a host
// path there leaks just as effectively as a bind mount.
func TestValidateRejectsHostEnvFile(t *testing.T) {
	for _, y := range []string{
		"services:\n  web:\n    image: x\n    env_file: /etc/passwd\n",
		"services:\n  web:\n    image: x\n    env_file:\n      - ../../secrets.env\n",
		"services:\n  web:\n    image: x\n    env_file:\n      - path: /etc/passwd\n        required: true\n",
		"services:\n  web:\n    image: x\n    label_file: /etc/hostname\n",
	} {
		if err := Validate([]byte(y), root(t)); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("accepted a host file read:\n%s\nerr=%v", y, err)
		}
	}
	// A file inside the repository is the ordinary case and must still work.
	ok := "services:\n  web:\n    image: x\n    env_file: .env.production\n"
	if err := Validate([]byte(ok), root(t)); err != nil {
		t.Fatalf("rejected a repo-relative env_file: %v", err)
	}
}
