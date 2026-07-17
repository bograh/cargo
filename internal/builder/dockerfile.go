package builder

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
)

type Dockerfile struct{}

func (Dockerfile) Build(ctx context.Context, in Input) error {
	args := []string{"build", "-t", in.ImageTag, "-f", filepath.Join(in.WorkDir, in.DockerfilePath)}
	keys := make([]string, 0, len(in.BuildArgs))
	for k := range in.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, in.BuildArgs[k]))
	}
	args = append(args, filepath.Join(in.WorkDir, in.ContextPath))
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = in.Log, in.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}
