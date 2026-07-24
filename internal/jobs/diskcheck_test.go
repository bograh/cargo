package jobs

import (
	"context"
	"testing"
)

func TestEvalDisk(t *testing.T) {
	cases := []struct {
		name             string
		freePct, minPct  float64
		wasLow           bool
		alert, prune, lo bool
	}{
		{"healthy", 50, 10, false, false, false, false},
		{"cross into low", 8, 10, false, true, false, true},
		{"still low, no re-alert", 8, 10, true, false, false, true},
		{"low and below hard floor from healthy", 3, 10, false, true, true, true},
		{"below hard floor, already low", 3, 10, true, false, true, true},
		{"recovery clears flag", 50, 10, true, false, false, false},
		{"exactly at threshold is not low", 10, 10, false, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := evalDisk(c.freePct, c.minPct, c.wasLow)
			if d.alert != c.alert || d.prune != c.prune || d.lowFlag != c.lo {
				t.Fatalf("evalDisk(%v,%v,%v) = %+v; want alert=%v prune=%v low=%v",
					c.freePct, c.minPct, c.wasLow, d, c.alert, c.prune, c.lo)
			}
		})
	}
}

func TestStatfsUsageRealDir(t *testing.T) {
	u, err := StatfsUsage(t.TempDir())
	if err != nil {
		t.Fatalf("StatfsUsage: %v", err)
	}
	if u.TotalBytes == 0 || u.FreePct <= 0 || u.FreePct > 100 {
		t.Fatalf("implausible usage: %+v", u)
	}
}

func TestDiskCheckerRunAlertsAndPrunes(t *testing.T) {
	al := &recordingAlerter{}
	pruned := false
	d := &DiskChecker{
		DataDir:    "/data",
		MinFreePct: 10,
		Alerter:    al,
		statfs: func(string) (DiskUsage, error) {
			return DiskUsage{Path: "/data", FreePct: 3, TotalBytes: 100, FreeBytes: 3}, nil
		},
		prune: func(context.Context) error { pruned = true; return nil },
	}
	// Pool is nil → load() returns zero (wasLow=false), save() is a no-op.
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(al.kinds) != 1 || al.kinds[0] != "disk_low" {
		t.Fatalf("alerts = %v; want [disk_low]", al.kinds)
	}
	if !pruned {
		t.Fatal("expected aggressive prune below the hard floor")
	}
}

func TestDiskCheckerRunHealthyNoAction(t *testing.T) {
	al := &recordingAlerter{}
	pruned := false
	d := &DiskChecker{
		DataDir:    "/data",
		MinFreePct: 10,
		Alerter:    al,
		statfs:     func(string) (DiskUsage, error) { return DiskUsage{FreePct: 80, TotalBytes: 100}, nil },
		prune:      func(context.Context) error { pruned = true; return nil },
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(al.kinds) != 0 || pruned {
		t.Fatalf("healthy disk should take no action: alerts=%v pruned=%v", al.kinds, pruned)
	}
}
