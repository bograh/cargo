package reconciler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Retire removes every compose project an app may have on this provider's
// daemon, addressing them by project name alone.
//
// Teardown cannot do this job for a moved app: the compose files live in the
// control plane's data dir keyed only by app ID, so once the app deploys to
// its new target those files describe the new target, and running them
// against the old daemon misses the old containers. Compose can bring a
// project down from its labels without any file, which is what this does.
// Local files are left alone for the same reason — they belong to the new
// target now.
//
// Named volumes are kept: a compose-source app's data on the old target is
// not Cargo's to delete as a side effect of a move.
func (d *Docker) Retire(ctx context.Context, slug string, log io.Writer) error {
	var errs []error
	for _, project := range RetireProjects(slug) {
		cmd := d.buildCmd(ctx, "docker", "compose", "-p", project, "down", "--remove-orphans")
		// Compose falls back to a compose file in the working directory when
		// it finds one; never let that stand in for the named project.
		cmd.Dir = os.TempDir()
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Errorf("compose down %s: %w", project, err))
		}
	}
	return errors.Join(errs...)
}

// RetireProjects lists the compose project names an app can own: the legacy
// single project (also used by compose-source apps) and one per color.
func RetireProjects(slug string) []string {
	base := "cargo-app-" + slug
	return []string{base, base + "-blue", base + "-green"}
}
