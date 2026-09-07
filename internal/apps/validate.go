package apps

import (
	"fmt"
	"path/filepath"
	"strings"
)

// maxPort is the highest TCP port an app can serve on. The check used to be
// `< 1` only, while the error message already promised a range — so a value
// like 70000 was accepted and rendered straight into a Traefik service label.
const maxPort = 65535

// validatePort checks the port an app serves on against the range its own
// error message advertises.
func validatePort(p int32) error {
	if p < 1 || p > maxPort {
		return fmt.Errorf("%w: exposed_port must be 1-%d", ErrValidation, maxPort)
	}
	return nil
}

// validateRepoPath checks a path an app points at inside its own checkout —
// dockerfile_path, build_context, compose_path. All three were stored
// unvalidated and joined onto the checkout directory, so "../../.." aimed
// `docker build -f` or `docker compose -f` at control-plane files.
//
// This is the lexical half, run at write time so the user is told at the point
// they typed it. The half that resolves symlinks against the real tree runs at
// build time (builder.Within) — a path can only be checked properly once the
// checkout exists.
func validateRepoPath(field, p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("%w: %s must not contain control characters", ErrValidation, field)
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return fmt.Errorf("%w: %s must be relative to the repository root, got %q",
			ErrValidation, field, p)
	}
	// Clean collapses "a/../.." to ".."; anything that still starts there
	// leaves the checkout.
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s %q points outside the repository", ErrValidation, field, p)
	}
	return nil
}
