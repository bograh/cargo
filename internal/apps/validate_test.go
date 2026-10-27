package apps

import (
	"strings"
	"testing"
)

func TestValidateRepoPath(t *testing.T) {
	ok := []string{"", "Dockerfile", ".", "./Dockerfile", "svc/api/Dockerfile", "a/../Dockerfile", "docker-compose.yml"}
	for _, p := range ok {
		if err := validateRepoPath("dockerfile_path", p); err != nil {
			t.Errorf("%q: want accepted, got %v", p, err)
		}
	}
	bad := []string{
		"../Dockerfile",
		"..",
		"a/../../etc/passwd",
		"/etc/passwd",
		"~/.ssh/id_rsa",
		"ok\nDockerfile",
	}
	for _, p := range bad {
		err := validateRepoPath("dockerfile_path", p)
		if err == nil {
			t.Errorf("%q: want rejected, got nil", p)
			continue
		}
		if !strings.Contains(err.Error(), "dockerfile_path") {
			t.Errorf("%q: error should name the field, got %v", p, err)
		}
	}
}

func TestValidateEnvKey(t *testing.T) {
	for _, k := range []string{"PORT", "_x", "DB_URL_2", "a"} {
		if err := validateEnvKey(k); err != nil {
			t.Errorf("%q: want accepted, got %v", k, err)
		}
	}
	for _, k := range []string{"", "2PORT", "DB-URL", "A=B", "OK\nINJECTED=1", "with space", "e\x00"} {
		if err := validateEnvKey(k); err == nil {
			t.Errorf("%q: want rejected, got nil", k)
		}
	}
}

func TestValidateImageRef(t *testing.T) {
	ok := []string{
		"nginx", "nginx:alpine", "library/nginx:1.27", "ghcr.io/org/app:v1.2.3",
		"localhost:5000/team/app", "registry.example.com:443/a/b/c:latest",
		"redis@sha256:" + strings.Repeat("a", 64),
		"ghcr.io/org/app:tag@sha256:" + strings.Repeat("0", 64),
	}
	for _, r := range ok {
		if err := ValidateImageRef(r); err != nil {
			t.Errorf("%q: want accepted, got %v", r, err)
		}
	}
	bad := []string{
		"", "nginx\n    privileged: true", "nginx # comment", "Nginx", "nginx:",
		"nginx:tag with space", "-nginx", "nginx\"", "a/../b", "nginx:{x}",
	}
	for _, r := range bad {
		if err := ValidateImageRef(r); err == nil {
			t.Errorf("%q: want rejected, got nil", r)
		}
	}
}

func TestValidateHealthcheckPath(t *testing.T) {
	for _, p := range []string{"", "/", "/healthz", "/api/v1/health?full=1", "/a%20b"} {
		if err := validateHealthcheckPath(p); err != nil {
			t.Errorf("%q: want accepted, got %v", p, err)
		}
	}
	for _, p := range []string{"healthz", "/x\n    privileged: true", "/a b", "/a#b", "/a\"b", "/a'b", "/a`b"} {
		if err := validateHealthcheckPath(p); err == nil {
			t.Errorf("%q: want rejected, got nil", p)
		}
	}
}
