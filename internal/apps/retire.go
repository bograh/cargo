package apps

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/hosts"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
)

// ControlPlaneTarget names the control plane in app_retirements; any other
// target is a hosts.id.
const ControlPlaneTarget = "control-plane"

// TargetKey is the app_retirements key for where an app runs.
func TargetKey(hostID pgtype.UUID) string {
	if !hostID.Valid {
		return ControlPlaneTarget
	}
	v, _ := hostID.Value()
	s, _ := v.(string)
	return s
}

// HostTargets materializes a worker host by its ID. *hostmgr.Manager
// satisfies it; a nil HostTargets means no worker hosts are configured.
type HostTargets interface {
	ResolveHost(ctx context.Context, hostID string) (*reconciler.Target, error)
}

// retirer is the provider capability that removes an app's projects by name.
type retirer interface {
	Retire(ctx context.Context, slug string, log io.Writer) error
}

// RecordMove notes that an app left from (its old target) for to, so a later
// retire pass removes what still runs on from. Moving back to a target that
// was waiting to be retired cancels that: the app is about to run there.
func RecordMove(ctx context.Context, q *sqlc.Queries, appID pgtype.UUID, from, to string) error {
	if from == to {
		return nil
	}
	if err := q.DeleteAppRetirement(ctx, sqlc.DeleteAppRetirementParams{AppID: appID, Target: to}); err != nil {
		return err
	}
	return q.AddAppRetirement(ctx, sqlc.AddAppRetirementParams{AppID: appID, Target: from})
}

// RetireStale tears the app down on every target it has left, except keep —
// where it runs now. A target is forgotten once its teardown succeeds, or
// when its host no longer exists; a failed teardown stays recorded so the
// next pass retries it. It reports every failure, joined.
func RetireStale(ctx context.Context, q *sqlc.Queries, base reconciler.DeployProvider, hostTargets HostTargets,
	appID pgtype.UUID, slug, keep string, log io.Writer) error {
	targets, err := q.ListAppRetirements(ctx, appID)
	if err != nil {
		return fmt.Errorf("list previous targets: %w", err)
	}
	return RetireTargets(ctx, q, base, hostTargets, appID, slug, keep, targets, log)
}

// RetireTargets is RetireStale over an already-read target list — for a
// caller that must read the list before the app row (and with it the
// records) goes away.
func RetireTargets(ctx context.Context, q *sqlc.Queries, base reconciler.DeployProvider, hostTargets HostTargets,
	appID pgtype.UUID, slug, keep string, targets []string, log io.Writer) error {
	var errs []error
	for _, t := range targets {
		forget := func() {
			if err := q.DeleteAppRetirement(ctx, sqlc.DeleteAppRetirementParams{AppID: appID, Target: t}); err != nil {
				errs = append(errs, fmt.Errorf("forget previous target %s: %w", t, err))
			}
		}
		if t == keep {
			forget()
			continue
		}
		provider, err := providerForTarget(ctx, base, hostTargets, t)
		if errors.Is(err, hosts.ErrNotFound) {
			_, _ = fmt.Fprintf(log, "==> previous host %s no longer exists; nothing to remove there\n", shortTarget(t))
			forget()
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("previous target %s: %w", shortTarget(t), err))
			continue
		}
		r, ok := provider.(retirer)
		if !ok {
			errs = append(errs, fmt.Errorf("previous target %s: provider cannot remove apps by name", shortTarget(t)))
			continue
		}
		_, _ = fmt.Fprintf(log, "==> removing the app from its previous location (%s)\n", shortTarget(t))
		if err := r.Retire(ctx, slug, log); err != nil {
			errs = append(errs, fmt.Errorf("remove from %s: %w", shortTarget(t), err))
			continue
		}
		forget()
	}
	return errors.Join(errs...)
}

func providerForTarget(ctx context.Context, base reconciler.DeployProvider, hostTargets HostTargets, t string) (reconciler.DeployProvider, error) {
	if t == ControlPlaneTarget {
		return base, nil
	}
	if hostTargets == nil {
		return nil, errors.New("worker hosts are not configured")
	}
	tg, err := hostTargets.ResolveHost(ctx, t)
	if err != nil {
		return nil, err
	}
	fb, ok := base.(interface {
		ForTarget(reconciler.Target) reconciler.DeployProvider
	})
	if !ok {
		return nil, errors.New("deploy provider does not support worker hosts")
	}
	return fb.ForTarget(*tg), nil
}

// shortTarget names a target in logs the way deploy logs name hosts.
func shortTarget(t string) string {
	if t == ControlPlaneTarget {
		return "control plane"
	}
	if len(t) >= 8 {
		return "worker host " + t[:8]
	}
	return "worker host " + t
}
