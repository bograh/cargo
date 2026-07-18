package jobs

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type DomainCheckArgs struct{}

func (DomainCheckArgs) Kind() string { return "domain_check" }

type DomainCheckWorker struct {
	river.WorkerDefaults[DomainCheckArgs]
	Pool *pgxpool.Pool
}

func (w *DomainCheckWorker) Work(ctx context.Context, _ *river.Job[DomainCheckArgs]) error {
	return RunDomainCheck(ctx, w.Pool, defaultLookup, defaultProbe)
}

func defaultLookup(ctx context.Context, host string) error {
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err
}

// defaultProbe reports whether the host answers HTTP(S) at all; any status
// code counts — routing is what we verify, not app health (FR-5.4).
func defaultProbe(ctx context.Context, host string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	for _, scheme := range []string{"https://", "http://"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+host, nil)
		if err != nil {
			continue
		}
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			return true
		}
	}
	return false
}

// RunDomainCheck sets each custom domain's status: DNS failure →
// misconfigured; HTTP(S) response → active; resolves but no response →
// pending. Lookup/probe are injectable for tests.
func RunDomainCheck(
	ctx context.Context,
	pool *pgxpool.Pool,
	lookup func(ctx context.Context, host string) error,
	probe func(ctx context.Context, host string) bool,
) error {
	q := sqlc.New(pool)
	domains, err := q.ListAllDomains(ctx)
	if err != nil {
		return err
	}
	for _, d := range domains {
		status := "pending"
		if err := lookup(ctx, d.Hostname); err != nil {
			status = "misconfigured"
		} else if probe(ctx, d.Hostname) {
			status = "active"
		}
		if err := q.UpdateDomainStatus(ctx, sqlc.UpdateDomainStatusParams{ID: d.ID, Status: status}); err != nil {
			return err
		}
	}
	return nil
}
