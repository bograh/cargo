package builder

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if got := Detect(dir, "Dockerfile"); got != "nixpacks" {
		t.Fatalf("no dockerfile → %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(dir, "Dockerfile"); got != "dockerfile" {
		t.Fatalf("with dockerfile → %q", got)
	}
}

func TestCloneAtBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("needs git")
	}
	src := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	dest := filepath.Join(t.TempDir(), "clone")
	var log bytes.Buffer
	sha, err := CloneAtBranch(context.Background(), "file://"+src, "main", dest, &log)
	if err != nil {
		t.Fatalf("clone: %v\n%s", err, log.String())
	}
	if len(sha) != 40 {
		t.Fatalf("sha = %q", sha)
	}
	if _, err := os.Stat(filepath.Join(dest, "hello.txt")); err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}
}

func TestDockerfileBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM alpine:3.21\nARG GREETING=hey\nRUN echo $GREETING\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	err := Dockerfile{}.Build(context.Background(), Input{
		WorkDir: dir, ImageTag: "cargo-test-build:t1", ContextPath: ".",
		DockerfilePath: "Dockerfile", BuildArgs: map[string]string{"GREETING": "cargo-42"}, Log: &log,
	})
	if err != nil {
		t.Fatalf("build: %v\n%s", err, log.String())
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", "cargo-test-build:t1").Run() })
}
