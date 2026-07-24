package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// diskHardFreePct is the free-space floor below which we aggressively reclaim
// space (dangling image prune) regardless of the configured soft threshold.
const diskHardFreePct = 5.0

// diskStatusKey is the instance_settings key holding the last disk snapshot,
// used both to dedupe alerts (fire once on crossing) and to feed the admin UI.
const diskStatusKey = "disk_status"

type DiskCheckArgs struct{}

func (DiskCheckArgs) Kind() string { return "disk_check" }

type DiskCheckWorker struct {
	river.WorkerDefaults[DiskCheckArgs]
	D *DiskChecker
}

func (w *DiskCheckWorker) Work(ctx context.Context, _ *river.Job[DiskCheckArgs]) error {
	return w.D.Run(ctx)
}

// DiskChecker watches free space on the data directory (which, for the default
// local-volume install, sits on the same host filesystem as Docker's images and
// container layers). Below the soft threshold it warns + alerts once; below the
// hard floor it also reclaims dangling images.
type DiskChecker struct {
	Pool       *pgxpool.Pool
	DataDir    string
	MinFreePct float64
	Alerter    Alerter

	// Test seams; nil in production.
	statfs func(path string) (DiskUsage, error)
	prune  func(ctx context.Context) error
}

// DiskUsage is a point-in-time filesystem snapshot.
type DiskUsage struct {
	Path       string  `json:"path"`
	FreePct    float64 `json:"free_pct"`
	FreeBytes  uint64  `json:"free_bytes"`
	TotalBytes uint64  `json:"total_bytes"`
}

type diskStatus struct {
	DiskUsage
	Low       bool      `json:"low"`
	CheckedAt time.Time `json:"checked_at"`
}

type diskDecision struct {
	alert   bool // fire a disk_low alert now (crossing into low)
	prune   bool // reclaim space now (below hard floor)
	lowFlag bool // the low state to persist
}

// evalDisk is the pure decision: alert only on the transition into low (so a
// sustained low condition doesn't re-alert every tick), prune below the hard
// floor, and record the current low state.
func evalDisk(freePct, minFreePct float64, wasLow bool) diskDecision {
	low := freePct < minFreePct
	return diskDecision{
		alert:   low && !wasLow,
		prune:   freePct < diskHardFreePct,
		lowFlag: low,
	}
}

// StatfsUsage reports free space for the filesystem backing path.
func StatfsUsage(path string) (DiskUsage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskUsage{}, err
	}
	bsize := uint64(st.Bsize)
	total := st.Blocks * bsize
	free := st.Bavail * bsize
	var pct float64
	if st.Blocks > 0 {
		pct = float64(st.Bavail) / float64(st.Blocks) * 100
	}
	return DiskUsage{Path: path, FreePct: pct, FreeBytes: free, TotalBytes: total}, nil
}

func (d *DiskChecker) Run(ctx context.Context) error {
	statfs := d.statfs
	if statfs == nil {
		statfs = StatfsUsage
	}
	usage, err := statfs(d.DataDir)
	if err != nil {
		return fmt.Errorf("statfs %s: %w", d.DataDir, err)
	}
	prev := d.load(ctx)
	dec := evalDisk(usage.FreePct, d.MinFreePct, prev.Low)

	if dec.alert {
		slog.Warn("disk space low", "path", d.DataDir, "free_pct", usage.FreePct, "threshold", d.MinFreePct)
		if d.Alerter != nil {
			d.Alerter.Alert(ctx, "disk_low",
				fmt.Sprintf("%.1f%% free on %s (threshold %.0f%%)", usage.FreePct, d.DataDir, d.MinFreePct))
		}
	}
	if dec.prune {
		prune := d.prune
		if prune == nil {
			prune = pruneDanglingImages
		}
		if err := prune(ctx); err != nil {
			slog.Warn("aggressive prune failed", "err", err)
		}
	}
	d.save(ctx, diskStatus{DiskUsage: usage, Low: dec.lowFlag, CheckedAt: time.Now().UTC()})
	return nil
}

func (d *DiskChecker) load(ctx context.Context) diskStatus {
	if d.Pool == nil {
		return diskStatus{}
	}
	row, err := sqlc.New(d.Pool).GetInstanceSetting(ctx, diskStatusKey)
	if err != nil {
		if !isNoRows(err) {
			slog.Warn("read disk status", "err", err)
		}
		return diskStatus{}
	}
	var s diskStatus
	if err := json.Unmarshal(row.Value, &s); err != nil {
		return diskStatus{}
	}
	return s
}

func (d *DiskChecker) save(ctx context.Context, s diskStatus) {
	if d.Pool == nil {
		return
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	if _, err := sqlc.New(d.Pool).UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{
		Key: diskStatusKey, Value: raw,
	}); err != nil {
		slog.Warn("persist disk status", "err", err)
	}
}

func isNoRows(err error) bool { return err == pgx.ErrNoRows || err.Error() == pgx.ErrNoRows.Error() }

// pruneDanglingImages reclaims space by removing dangling (untagged) images.
func pruneDanglingImages(ctx context.Context) error {
	return exec.CommandContext(ctx, "docker", "image", "prune", "-f").Run()
}
