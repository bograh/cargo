package jobs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/giturl"
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

// Timeout replaces River's 1-minute default. Domains are probed in bounded
// parallel (domainCheckConcurrency), each capped at domainCheckPerHost, so a
// pass over many domains needs more than a minute; at one minute a handful of
// unresponsive domains timed out every run and later domains never updated.
func (w *DomainCheckWorker) Timeout(*river.Job[DomainCheckArgs]) time.Duration {
	return 10 * time.Minute
}

const (
	domainCheckConcurrency = 8
	// domainCheckPerHost bounds DNS plus both probes for one domain.
	domainCheckPerHost = 15 * time.Second
)

func defaultLookup(ctx context.Context, host string) error {
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err
}

// errNonPublicAddress is returned by the probe dialer when a domain resolves
// to an address the control plane must not reach.
var errNonPublicAddress = errors.New("domain resolves to a non-public address")

// publicOnlyControl runs after DNS resolution, on the exact address being
// dialed, so a tenant domain pointed at 127.0.0.1, an RFC1918 range or the
// cloud metadata service cannot turn the domain check into a request from
// the control plane's network position — including via DNS rebinding.
func publicOnlyControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !giturl.PublicIP(ip) {
		return fmt.Errorf("%w: %s", errNonPublicAddress, host)
	}
	return nil
}

// newProbeClient builds the HTTP client for domain probes. control vets each
// dialed address (publicOnlyControl in production). Redirects are not
// followed: a 3xx already proves the domain answers, and following one would
// let a tenant steer the request to a host of their choosing.
func newProbeClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: control}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

var probeClient = newProbeClient(publicOnlyControl)

// defaultProbe reports whether the host answers HTTP(S) at all; any status
// code counts — routing is what we verify, not app health (FR-5.4). A domain
// that only resolves to non-public addresses never answers.
func defaultProbe(ctx context.Context, host string) bool {
	return probeWith(ctx, probeClient, host)
}

func probeWith(ctx context.Context, client *http.Client, host string) bool {
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
	type result struct {
		d      sqlc.Domain
		status string
	}
	results := make([]result, len(domains))
	sem := make(chan struct{}, domainCheckConcurrency)
	var wg sync.WaitGroup
	for i, d := range domains {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			hctx, cancel := context.WithTimeout(ctx, domainCheckPerHost)
			defer cancel()
			status := "pending"
			if err := lookup(hctx, d.Hostname); err != nil {
				status = "misconfigured"
			} else if probe(hctx, d.Hostname) {
				status = "active"
			}
			results[i] = result{d, status}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, r := range results {
		if err := q.UpdateDomainStatus(ctx, sqlc.UpdateDomainStatusParams{ID: r.d.ID, Status: r.status}); err != nil {
			return err
		}
	}
	return nil
}
