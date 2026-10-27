package builder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// WriteFileNoFollow writes a file into a tenant checkout without following a
// symlink at the final path component.
//
// os.WriteFile opens with O_CREATE and follows links, so a repository that
// commits `.env -> /root/.ssh/config` or `Dockerfile.cargo -> /etc/…` turned the
// control plane's own write into a write anywhere it has permission — with
// tenant-chosen contents. Whatever already sits at path (a link, a regular
// file the repo shipped) is removed first, and the new file is created
// exclusively with O_NOFOLLOW, so a link swapped in between the two steps
// makes the open fail rather than redirect it. Parent directories must be
// checked separately (Within); this guards the last hop.
func WriteFileNoFollow(path string, data []byte, perm os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil {
		if fi.IsDir() {
			return fmt.Errorf("%w: %s is a directory", ErrUnsafePath, filepath.Base(path))
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
