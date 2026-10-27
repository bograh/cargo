// Package builder turns application sources into local Docker images.
package builder

import (
	"context"
	"io"
	"os"
	"os/exec"
)

type Input struct {
	WorkDir        string
	ImageTag       string
	ContextPath    string
	DockerfilePath string
	BuildArgs      map[string]string
	Log            io.Writer
}

type Builder interface {
	Build(ctx context.Context, in Input) error
}

// Detect chooses a builder for auto mode: a user Dockerfile wins, then a
// language generator (a lean multi-stage Dockerfile), then Nixpacks as the
// universal fallback (FR-3.3).
//
// workDir is both the repository root and the build context here; DetectIn
// handles a build context below the root.
func Detect(workDir, dockerfilePath string) string {
	return DetectIn(workDir, ".", dockerfilePath)
}

// DetectIn is Detect for a checkout whose build context is a subdirectory.
// The Dockerfile is looked up the way the build resolves it — relative to the
// repository root (`docker build -f`, see Dockerfile.Build) — while language
// generators inspect the build context. Looking the Dockerfile up under the
// context instead picked "dockerfile" for build_context=api with only
// api/Dockerfile present, and the build then failed on a missing file.
func DetectIn(root, contextPath, dockerfilePath string) string {
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	if p, err := Within(root, dockerfilePath); err == nil {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return "dockerfile"
		}
	}
	ctxDir, err := Within(root, contextPath)
	if err != nil {
		return "nixpacks" // the build itself reports the unsafe path
	}
	if g, ok := detectGenerator(ctxDir); ok {
		return g.Name()
	}
	return "nixpacks"
}

func ForName(name string) Builder {
	switch name {
	case "nixpacks":
		return Nixpacks{}
	case "dockerfile":
		return Dockerfile{}
	}
	if g, ok := generatorByName(name); ok {
		return generatedBuilder{gen: g}
	}
	return Dockerfile{}
}

func NixpacksAvailable() bool {
	_, err := exec.LookPath("nixpacks")
	return err == nil
}
