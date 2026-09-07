package builder

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/bograh/cargo/internal/giturl"
)

// CloneFunc returns a clone function that refuses a repository host pointing
// inside the deployment, unless the operator has opted in.
//
// The address check belongs here rather than only at app-creation time: the
// name in git_repo_url can be repointed after the app is created, and this is
// the moment the request is actually made. It narrows the window rather than
// closing it — git resolves the name again itself a moment later.
func CloneFunc(allowPrivateHosts bool) func(context.Context, string, string, string, io.Writer) (string, error) {
	return func(ctx context.Context, repoURL, branch, dest string, log io.Writer) (string, error) {
		if err := giturl.CheckHost(repoURL, allowPrivateHosts); err != nil {
			return "", fmt.Errorf("git clone: %w", err)
		}
		return CloneAtBranch(ctx, repoURL, branch, dest, log)
	}
}

// CloneAtBranch shallow-clones a branch head and returns its commit SHA. It
// does no URL screening of its own; CloneFunc is the wrapper the server wires.
func CloneAtBranch(ctx context.Context, repoURL, branch, dest string, log io.Writer) (string, error) {
	// Two belt-and-braces measures. protocol.ext.allow=never: current git
	// already refuses the ext transport (which runs a shell command), but that
	// is a default a system gitconfig can change. "--": git clone happens to
	// treat a leading-dash first positional as the repository rather than as a
	// flag, which is why argument injection does not land today — an accident
	// worth not depending on.
	clone := exec.CommandContext(ctx, "git",
		"-c", "protocol.ext.allow=never",
		"clone", "--depth", "1", "--single-branch",
		"--branch", branch, "--", repoURL, dest)
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
