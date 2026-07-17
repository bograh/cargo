package builder

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

type Nixpacks struct{}

func (Nixpacks) Build(ctx context.Context, in Input) error {
	cmd := exec.CommandContext(ctx, "nixpacks", "build",
		filepath.Join(in.WorkDir, in.ContextPath), "--name", in.ImageTag)
	cmd.Stdout, cmd.Stderr = in.Log, in.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nixpacks build: %w", err)
	}
	return nil
}
