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
}

type DeployProvider interface {
	Apply(ctx context.Context, spec Spec, log io.Writer) error
	Teardown(ctx context.Context, appID, slug string, log io.Writer) error
}
