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
