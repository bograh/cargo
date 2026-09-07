package builder

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
)

type Dockerfile struct{}

func (Dockerfile) Build(ctx context.Context, in Input) error {
	dfPath, err := Within(in.WorkDir, in.DockerfilePath)
	if err != nil {
		return err
	}
	ctxDir, err := Within(in.WorkDir, in.ContextPath)
	if err != nil {
		return err
	}
	args := []string{"build", "-t", in.ImageTag, "-f", dfPath}
	keys := make([]string, 0, len(in.BuildArgs))
	for k := range in.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, in.BuildArgs[k]))
	}
	args = append(args, ctxDir)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = in.Log, in.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}
