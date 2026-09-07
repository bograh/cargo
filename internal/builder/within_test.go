package builder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithinContainsPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"Dockerfile", ".", "svc", "svc/../Dockerfile"} {
		if _, err := Within(root, p); err != nil {
			t.Errorf("%q: want contained, got %v", p, err)
		}
	}
	for _, p := range []string{"../escape", "/etc/passwd", "~/.ssh/id_rsa", "svc/../../escape"} {
		if _, err := Within(root, p); err == nil {
			t.Errorf("%q: want rejected, got nil", p)
		}
	}
}

func TestWithinFollowsSymlinksOutOfTheCheckout(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A symlink committed into the repository is the case a lexical check
	// cannot see: "link" contains no "..".
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Within(root, "link"); err == nil {
		t.Fatal("symlink out of the checkout was accepted")
	}
}
