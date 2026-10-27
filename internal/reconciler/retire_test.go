package reconciler

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Retire must find an app's containers by project name alone: after a move,
// the compose files on disk describe the new target, not the old one.
func TestRetireRemovesProjectsWithoutComposeFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("needs docker")
	}
	ctx := context.Background()
	slug := "retire-test"
	project := "cargo-app-" + slug + "-blue"
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte("name: "+project+"\nservices:\n  app:\n    image: nginx:alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := output(ctx, "docker", "compose", "-f", file, "up", "-d"); err != nil {
		t.Fatalf("up: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = output(ctx, "docker", "compose", "-p", project, "down") })
	// The file is gone, as it would be once the new target's deploy replaced it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	d := NewDocker(t.TempDir())
	var log bytes.Buffer
	if err := d.Retire(ctx, slug, &log); err != nil {
		t.Fatalf("retire: %v\n%s", err, log.String())
	}
	ids, err := output(ctx, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(ids) != "" {
		t.Fatalf("containers still present after retire: %s\n%s", ids, log.String())
	}
}
