package builder

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrUnsafePath is returned when a tenant-supplied path resolves outside the
// checkout it is meant to be relative to.
var ErrUnsafePath = errors.New("path escapes the repository")

// Within resolves a repository-relative path against root and confirms the
// result stays inside it.
//
// dockerfile_path, build_context and compose_path are tenant-controlled and
// were joined onto the checkout with no containment check, so "../../.." aimed
// `docker build -f` at control-plane files. The lexical half of this is also
// enforced when the fields are written (apps.validateRepoPath); this is the
// half that runs against the real tree, where a symlink committed into the
// repository can be followed out of it.
//
// The rules match compose/validate.go:checkPath, which does exactly this for
// paths named *inside* a compose file — the same check simply was never
// applied to the path of the file itself.
func Within(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if strings.HasPrefix(rel, "~") {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, rel)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = real
	}
	resolved := rel
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(absRoot, resolved)
	}
	resolved = filepath.Clean(resolved)
	// Follow symlinks where the path already exists; a path that does not
	// (a Dockerfile about to be generated) is checked lexically.
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	if resolved != absRoot && !strings.HasPrefix(resolved, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q resolves outside the checkout", ErrUnsafePath, rel)
	}
	return resolved, nil
}
