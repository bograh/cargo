package builder

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestDetectNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(dir, "Dockerfile"); got != "node" {
		t.Fatalf("package.json → %q, want node", got)
	}
	// A user Dockerfile still wins.
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Detect(dir, "Dockerfile"); got != "dockerfile" {
		t.Fatalf("dockerfile present → %q, want dockerfile", got)
	}
	// A bun project defers to Nixpacks.
	bun := t.TempDir()
	_ = os.WriteFile(filepath.Join(bun, "package.json"), []byte(`{}`), 0o644)
	_ = os.WriteFile(filepath.Join(bun, "bun.lockb"), []byte("x"), 0o644)
	if got := Detect(bun, "Dockerfile"); got != "nixpacks" {
		t.Fatalf("bun project → %q, want nixpacks", got)
	}
}

func TestNodeDockerfile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"build":"next build","start":"next start"},"engines":{"node":">=20"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o644)

	df, err := (Node{}).Dockerfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# syntax=docker/dockerfile:1",
		"FROM node:20-alpine AS build",
		"FROM node:20-alpine AS run",
		"corepack enable",
		"pnpm install --frozen-lockfile",
		"pnpm run build",
		"pnpm prune --prod",
		"USER app",
		`CMD ["pnpm","run","start"]`,
	} {
		if !strings.Contains(df, want) {
			t.Errorf("generated Dockerfile missing %q:\n%s", want, df)
		}
	}
}

func TestNodeDockerfileNpmNoStart(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"main":"server.js"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{}`), 0o644)
	df, err := (Node{}).Dockerfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"npm ci", "node:22-alpine", `CMD ["node","server.js"]`} {
		if !strings.Contains(df, want) {
			t.Errorf("missing %q:\n%s", want, df)
		}
	}
	if strings.Contains(df, "run build") {
		t.Error("should not build without a build script")
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
