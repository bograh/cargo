package builder

import (
	"context"
	"fmt"
	"os/exec"
)

type Nixpacks struct{}

func (Nixpacks) Build(ctx context.Context, in Input) error {
	ctxDir, err := Within(in.WorkDir, in.ContextPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "nixpacks", "build", ctxDir, "--name", in.ImageTag)
	cmd.Stdout, cmd.Stderr = in.Log, in.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nixpacks build: %w", err)
	}
	return nil
}
