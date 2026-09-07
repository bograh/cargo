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
