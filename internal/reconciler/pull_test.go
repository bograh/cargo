package reconciler

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v // last wins, as exec does
	}
	return m
}

// Registry logins must land in a throwaway config, never the control plane's
// global one that every other tenant's pull would then use.
func TestIsolatedEnvLocal(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "/root/.docker")
	t.Setenv("DOCKER_CONTEXT", "something")
	env := envMap(NewDocker(t.TempDir()).isolatedEnv("/tmp/cfg"))
	if env["DOCKER_CONFIG"] != "/tmp/cfg" {
		t.Fatalf("DOCKER_CONFIG = %q", env["DOCKER_CONFIG"])
	}
	if _, ok := env["DOCKER_CONTEXT"]; ok {
		t.Fatal("DOCKER_CONTEXT leaked into the isolated env")
	}
}

// A worker host loses its context along with its DOCKER_CONFIG, so it must be
// addressed by DOCKER_HOST — keeping its PATH (the ssh wrapper).
func TestIsolatedEnvHost(t *testing.T) {
	d := NewDocker(t.TempDir()).ForHost(Target{
		HostID: "h", Endpoint: "ssh://deploy@10.0.0.5",
		Env: []string{"DOCKER_CONFIG=/data/hosts/h/docker", "DOCKER_CONTEXT=cargo-worker", "PATH=/data/hosts/h/bin:/usr/bin"},
	})
	env := envMap(d.isolatedEnv("/tmp/cfg"))
	if env["DOCKER_CONFIG"] != "/tmp/cfg" || env["DOCKER_HOST"] != "ssh://deploy@10.0.0.5" {
		t.Fatalf("env = %v", env)
	}
	if _, ok := env["DOCKER_CONTEXT"]; ok {
		t.Fatal("DOCKER_CONTEXT would conflict with DOCKER_HOST")
	}
	if !strings.HasPrefix(env["PATH"], "/data/hosts/h/bin") {
		t.Fatalf("PATH = %q, want the host's ssh wrapper first", env["PATH"])
	}
}
