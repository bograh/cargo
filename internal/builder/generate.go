package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Generator produces an optimized multi-stage Dockerfile for a project it
// recognizes. Generators are tried in registry order by Detect; the first
// Match wins, and anything unrecognized falls through to Nixpacks.
//
// Design rule: keep each generator thin and conservative. When a project's
// shape is ambiguous, Match should return false so the Nixpacks fallback
// handles it — that keeps the maintenance surface small and avoids shipping
// broken builds for edge cases.
type Generator interface {
	Name() string
	Match(dir string) bool
	Dockerfile(dir string) (string, error)
}

// generators is the ordered registry. Add new languages here.
var generators = []Generator{
	Node{},
	Go{},
	Java{},
}

func detectGenerator(dir string) (Generator, bool) {
	for _, g := range generators {
		if g.Match(dir) {
			return g, true
		}
	}
	return nil, false
}

func generatorByName(name string) (Generator, bool) {
	for _, g := range generators {
		if g.Name() == name {
			return g, true
		}
	}
	return nil, false
}

const generatedDockerfileName = "Dockerfile.cargo"

// generatedBuilder writes a generator's Dockerfile into the build context and
// builds it with the standard Dockerfile builder (BuildKit).
type generatedBuilder struct{ gen Generator }

func (g generatedBuilder) Build(ctx context.Context, in Input) error {
	ctxDir := filepath.Join(in.WorkDir, in.ContextPath)
	df, err := g.gen.Dockerfile(ctxDir)
	if err != nil {
		return fmt.Errorf("%s generator: %w", g.gen.Name(), err)
	}
	if in.Log != nil {
		_, _ = fmt.Fprintf(in.Log, "==> generated multi-stage Dockerfile (%s):\n%s\n", g.gen.Name(), df)
	}
	dfPath := filepath.Join(ctxDir, generatedDockerfileName)
	if err := os.WriteFile(dfPath, []byte(df), 0o644); err != nil {
		return err
	}
	defer func() { _ = os.Remove(dfPath) }()
	return Dockerfile{}.Build(ctx, Input{
		WorkDir:        in.WorkDir,
		ImageTag:       in.ImageTag,
		ContextPath:    in.ContextPath,
		DockerfilePath: filepath.Join(in.ContextPath, generatedDockerfileName),
		BuildArgs:      in.BuildArgs,
		Log:            in.Log,
	})
}
