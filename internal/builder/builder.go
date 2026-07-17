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

// Detect implements FR-3.3: Dockerfile if present, otherwise Nixpacks.
func Detect(workDir, dockerfilePath string) string {
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	if _, err := os.Stat(filepath.Join(workDir, dockerfilePath)); err == nil {
		return "dockerfile"
	}
	return "nixpacks"
}

func ForName(name string) Builder {
	if name == "nixpacks" {
		return Nixpacks{}
	}
	return Dockerfile{}
}

func NixpacksAvailable() bool {
	_, err := exec.LookPath("nixpacks")
	return err == nil
}
