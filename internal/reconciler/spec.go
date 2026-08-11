// Package reconciler turns application specs into running per-app Docker
// Compose projects. It is the only package that talks to Docker (the
// DeployProvider seam — a Kubernetes provider slots in later).
package reconciler

import (
	"context"
	"io"
)

type Spec struct {
	AppID           string
	Slug            string
	Image           string
	Port            int32
	HealthcheckPath string
	Env             map[string]string
	Domains         []string
	// Networks lists the compose networks the app service joins. Empty
	// defaults to ["cargo-proxy"] to keep existing output unchanged.
	Networks []string
	// Resource caps rendered into the compose file. Empty/zero fields are
	// omitted (the deploy pipeline fills them from instance defaults, so in
	// practice they are always set for real deploys).
	MemoryLimit string // docker mem_limit, e.g. "512m"
	CPULimit    string // docker cpus, e.g. "1" or "1.5"
	PidsLimit   int    // docker pids_limit

	// BlueGreen selects the zero-downtime apply strategy: the new color is
	// started alongside the running one and only replaces it once healthy.
	BlueGreen bool
	// ComposeFile, when set, switches to the user-supplied compose source: the
	// path (inside SourceDir) of the user's own compose file. Cargo applies it
	// together with a generated overlay rather than its own single-service
	// file. ComposeService names the service that receives traffic.
	ComposeFile    string
	ComposeService string
	// SourceDir is the checkout the pipeline placed on disk for a compose
	// source. It becomes the compose project directory, so relative paths in
	// the user's file resolve against their own repository.
	SourceDir string

	// Color is the deployment color this spec renders ("blue"/"green"), which
	// scopes the compose project name so both colors can run side by side. It
	// is set by Apply from on-disk state; an empty value renders the original
	// single-project layout used by the recreate strategy.
	Color string
}

// nextColor returns the color to deploy into given the currently active one.
// A legacy (pre-blue/green) app has no color and moves to blue first.
func nextColor(active string) string {
	if active == "blue" {
		return "green"
	}
	return "blue"
}

type DeployProvider interface {
	Apply(ctx context.Context, spec Spec, log io.Writer) error
	Teardown(ctx context.Context, appID, slug string, log io.Writer) error
	// Stop halts the app's running container(s) without discarding the compose
	// project, so a later Start can bring it back. Start resumes a stopped app.
	Stop(ctx context.Context, appID string, log io.Writer) error
	Start(ctx context.Context, appID string, log io.Writer) error
}

// DBSpec describes a managed database instance.
type DBSpec struct {
	InstanceID string
	Engine     string // postgres | redis
	Version    string
	AdminPass  string // superuser / requirepass credential
	HostPort   int32  // 0 = not published
}

// DatabaseProvider manages the lifecycle of managed database instances. It
// is implemented by Docker (dbprovider.go) alongside DeployProvider.
type DatabaseProvider interface {
	ProvisionDB(ctx context.Context, spec DBSpec, log io.Writer) error
	ExecDB(ctx context.Context, instanceID, engine string, stdin string, args ...string) (string, error)
	SnapshotDB(ctx context.Context, instanceID, engine, adminPass, destPath string) error
	TeardownDB(ctx context.Context, instanceID string, log io.Writer) error
}
