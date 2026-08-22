package reconciler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bograh/cargo/internal/compose"
)

// writeComposeEnv puts the app's environment in the project directory before
// validation, because compose interpolates `${VAR}` from it — validating
// without it would check a different configuration than the one that runs.
func (d *Docker) writeComposeEnv(spec Spec) error {
	content, err := generateEnvFile(spec.Env)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(spec.SourceDir, ".env"), []byte(content), 0o600)
}

// validateComposeSource is the authoritative safety gate for a user-supplied
// compose file. It runs immediately before `up`, against the configuration
// compose itself resolves rather than the file as written.
//
// That distinction is the whole point. `docker compose config` expands `${VAR}`
// interpolation, follows `extends: {file: …}`, and resolves anchors and merge
// keys — each of which can otherwise hide a directive from anything that reads
// the source text. Interpolation matters most: Cargo writes the app's own
// environment into the project's .env, so validating pre-interpolation would
// let a tenant set a variable through the env-var UI and expand it into a bind
// mount of the host root.
func (d *Docker) validateComposeSource(ctx context.Context, spec Spec) error {
	if spec.ComposeFile == "" {
		return nil
	}
	userFile := filepath.Join(spec.SourceDir, spec.ComposeFile)
	// The user's file alone: the overlay's own additions are Cargo's and need
	// no checking, and excluding it keeps the rules free of special cases.
	rendered, err := d.output(ctx, "docker", "compose", "-f", userFile, "config")
	if err != nil {
		// compose reports the actual problem (bad YAML, an unknown `extends`
		// target) on stderr; without it the user just sees "exit status 1".
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return fmt.Errorf("%s is not valid: %s", spec.ComposeFile, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return fmt.Errorf("could not read %s: %w", spec.ComposeFile, err)
	}
	if err := compose.Validate([]byte(rendered), spec.SourceDir); err != nil {
		return err
	}
	return nil
}
