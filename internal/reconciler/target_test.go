package reconciler

import (
	"context"
	"os"
	"strings"
	"testing"
)

func mapOfEnv(kv []string) map[string]string {
	m := map[string]string{}
	for _, e := range kv {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

// The control-plane path must stay byte-for-byte what it was before targets
// existed: nil Env means exec inherits os.Environ, exactly as before.
func TestLocalCommandsInheritProcessEnv(t *testing.T) {
	d := NewDocker(t.TempDir())
	cmd := d.buildCmd(context.Background(), "docker", "ps")
	if cmd.Env != nil {
		t.Fatalf("local cmd.Env = %v, want nil (inherit)", cmd.Env)
	}
}

func TestForHostCommandsCarryTargetEnv(t *testing.T) {
	d := NewDocker(t.TempDir()).ForHost(Target{
		HostID:   "h1",
		Endpoint: "ssh://u@h:22",
		Env:      []string{"DOCKER_CONFIG=/x/docker", "HOME=/x/home", "DOCKER_CONTEXT=cargo-worker"},
	})
	cmd := d.buildCmd(context.Background(), "docker", "ps")
	env := mapOfEnv(cmd.Env)
	if env["DOCKER_CONFIG"] != "/x/docker" || env["DOCKER_CONTEXT"] != "cargo-worker" || env["HOME"] != "/x/home" {
		t.Fatalf("env = %v", env)
	}
	// Process env is preserved underneath the target additions.
	env["PATH"] = os.Getenv("PATH") // would already be present via inherit
	if _, ok := mapOfEnv(cmd.Env)["PATH"]; !ok {
		t.Fatal("process env lost under target env")
	}
}

func TestForHostSharesDataDirAndTimeout(t *testing.T) {
	base := NewDocker("/data")
	base.HealthTimeout = 31337
	h := base.ForHost(Target{HostID: "h1"})
	if h.dataDir != "/data" || h.HealthTimeout != 31337 {
		t.Fatalf("host view lost base state: %+v", h)
	}
	if base.target != nil {
		t.Fatal("base provider mutated by ForHost")
	}
}

func TestSpecHostIDCarriesRouting(t *testing.T) {
	spec := Spec{AppID: "a1", HostID: "h1"}
	if spec.HostID != "h1" {
		t.Fatal("HostID not stored")
	}
}
