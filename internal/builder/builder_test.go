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
		"npm install -g pnpm@9", // no packageManager pin → install pinned pnpm, not corepack-latest
		"pnpm install --frozen-lockfile",
		"pnpm run build",
		"pnpm prune --prod",
		"rm -rf .git .next/cache", // drop caches/.git so the runtime image stays lean
		"USER app",
		`CMD ["npm","run","start"]`, // runtime uses npm (bundled), not pnpm
	} {
		if strings.Contains(df, "corepack enable") {
			t.Errorf("should not use corepack-latest without a packageManager pin:\n%s", df)
		}
		if !strings.Contains(df, want) {
			t.Errorf("generated Dockerfile missing %q:\n%s", want, df)
		}
	}
}

func TestNodePnpmPinnedUsesCorepack(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"packageManager":"pnpm@8.15.0","scripts":{"start":"node x"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '6.0'\n"), 0o644)
	df, err := (Node{}).Dockerfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(df, "corepack enable") {
		t.Errorf("pinned packageManager should use corepack:\n%s", df)
	}
	if strings.Contains(df, "npm install -g pnpm") {
		t.Errorf("pinned packageManager should not npm-install pnpm:\n%s", df)
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

func TestDetectGo(t *testing.T) {
	// go.mod + root main package → go
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	if got := Detect(root, "Dockerfile"); got != "go" {
		t.Fatalf("root main → %q, want go", got)
	}

	// go.mod but no main package anywhere → falls through to nixpacks
	lib := t.TempDir()
	_ = os.WriteFile(filepath.Join(lib, "go.mod"), []byte("module x\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(lib, "lib.go"), []byte("package lib\n"), 0o644)
	if got := Detect(lib, "Dockerfile"); got != "nixpacks" {
		t.Fatalf("no main → %q, want nixpacks", got)
	}

	// single cmd/<name>/main.go → go, built as ./cmd/<name>
	cmd := t.TempDir()
	_ = os.WriteFile(filepath.Join(cmd, "go.mod"), []byte("module x\ngo 1.23\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(cmd, "cmd", "server"), 0o755)
	_ = os.WriteFile(filepath.Join(cmd, "cmd", "server", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	if got := Detect(cmd, "Dockerfile"); got != "go" {
		t.Fatalf("cmd main → %q, want go", got)
	}
	df, err := (Go{}).Dockerfile(cmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM golang:1.23-alpine AS build", "CGO_ENABLED=0 go build", "-o /out/app ./cmd/server", "FROM alpine:3.21 AS run", "USER app"} {
		if !strings.Contains(df, want) {
			t.Errorf("go Dockerfile missing %q:\n%s", want, df)
		}
	}

	// two cmd mains → ambiguous → not matched
	amb := t.TempDir()
	_ = os.WriteFile(filepath.Join(amb, "go.mod"), []byte("module x\ngo 1.22\n"), 0o644)
	for _, n := range []string{"a", "b"} {
		_ = os.MkdirAll(filepath.Join(amb, "cmd", n), 0o755)
		_ = os.WriteFile(filepath.Join(amb, "cmd", n, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	}
	if got := Detect(amb, "Dockerfile"); got != "nixpacks" {
		t.Fatalf("ambiguous cmd → %q, want nixpacks", got)
	}
}

func TestDetectJava(t *testing.T) {
	mvn := t.TempDir()
	_ = os.WriteFile(filepath.Join(mvn, "pom.xml"), []byte("<project><properties><java.version>17</java.version></properties></project>"), 0o644)
	if got := Detect(mvn, "Dockerfile"); got != "java" {
		t.Fatalf("pom.xml → %q, want java", got)
	}
	df, err := (Java{}).Dockerfile(mvn)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM maven:3.9-eclipse-temurin-17 AS build", "mvn -B -q -DskipTests package", "FROM eclipse-temurin:17-jre-alpine AS run", `CMD ["java","-jar","app.jar"]`} {
		if !strings.Contains(df, want) {
			t.Errorf("maven Dockerfile missing %q:\n%s", want, df)
		}
	}

	gr := t.TempDir()
	_ = os.WriteFile(filepath.Join(gr, "build.gradle"), []byte("plugins { id 'org.springframework.boot' }\nsourceCompatibility = 21\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gr, "gradlew"), []byte("#!/bin/sh\n"), 0o755)
	if got := Detect(gr, "Dockerfile"); got != "java" {
		t.Fatalf("build.gradle → %q, want java", got)
	}
	df, err = (Java{}).Dockerfile(gr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM eclipse-temurin:21-jdk AS build", "./gradlew", "bootJar", "-jre-alpine AS run"} {
		if !strings.Contains(df, want) {
			t.Errorf("gradle Dockerfile missing %q:\n%s", want, df)
		}
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
