package builder

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// CloneAtBranch shallow-clones a branch head and returns its commit SHA.
func CloneAtBranch(ctx context.Context, repoURL, branch, dest string, log io.Writer) (string, error) {
	clone := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--single-branch",
		"--branch", branch, repoURL, dest)
	clone.Stdout, clone.Stderr = log, log
	if err := clone.Run(); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	rev := exec.CommandContext(ctx, "git", "-C", dest, "rev-parse", "HEAD")
	out, err := rev.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
