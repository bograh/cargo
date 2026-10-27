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

// A symlink committed into the checkout must not redirect the control plane's
// write (.env, Dockerfile.cargo) onto a file outside it.
func TestWriteFileNoFollowReplacesSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "config")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".env")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileNoFollow(link, []byte("KEY=VALUE\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "original" {
		t.Fatalf("write followed the symlink: victim now %q", got)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		t.Fatalf("want a regular file at %s, got %v (err %v)", link, fi.Mode(), err)
	}
	if got, _ := os.ReadFile(link); string(got) != "KEY=VALUE\n" {
		t.Fatalf("content = %q", got)
	}
}
