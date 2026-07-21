// Package builder turns application sources into local Docker images.
package builder

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
func Detect(workDir, dockerfilePath string) string {
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	if _, err := os.Stat(filepath.Join(workDir, dockerfilePath)); err == nil {
		return "dockerfile"
	}
	if g, ok := detectGenerator(workDir); ok {
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
