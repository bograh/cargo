package hostmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/hosts"
	"github.com/bograh/cargo/internal/reconciler"
)

const (
	verifyTimeout  = 30 * time.Second
	statusOnline   = "online"
	statusPending  = "pending"
	statusDegraded = "degraded"
)

// runFunc executes one command in the host's target environment and returns
// stdout. The default shells out through the materialized context (the docker
// CLI's spawned ssh does transport); tests inject canned outputs so no SSH
// daemon or Docker engine is needed.
type runFunc func(ctx context.Context, env []string, name string, args ...string) (string, error)

func defaultRun(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Manager owns worker-host verification and target materialization.
type Manager struct {
	mat     *Materializer
	hostSvc *hosts.Service
	run     runFunc
}

func NewManager(dataDir string, hostSvc *hosts.Service) *Manager {
	m := &Manager{hostSvc: hostSvc, run: defaultRun}
	m.mat = NewMaterializer(dataDir, hostSvc)
	m.mat.onPin = func(ctx context.Context, h sqlc.Host, fp string) error {
		return m.hostSvc.PinFingerprint(ctx, h.ID.String(), fp)
	}
	return m
}

// SetRun overrides remote command execution (tests).
func (m *Manager) SetRun(fn runFunc) { m.run = fn }

// The CRUD delegates below let API consumers treat the Manager as the single
// worker-hosts collaborator.

func (m *Manager) Create(ctx context.Context, in hosts.CreateInput) (sqlc.Host, error) {
	return m.hostSvc.Create(ctx, in)
}

func (m *Manager) List(ctx context.Context) ([]sqlc.Host, error) {
	return m.hostSvc.List(ctx)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.hostSvc.Delete(ctx, id)
}

func (m *Manager) ReplaceKey(ctx context.Context, id, keyPEM string) error {
	return m.hostSvc.ReplaceKey(ctx, id, keyPEM)
}

// Target materializes the host's isolated execution environment.
func (m *Manager) Target(ctx context.Context, h sqlc.Host) (reconciler.Target, error) {
	return m.mat.Target(ctx, h)
}

// VerifyResult reports one verification pass.
type VerifyResult struct {
	Status        string // online | degraded | unreachable
	EngineVersion string
	CPUCount      int
	MemTotalMB    int64
	// InstallHint carries a copy-paste bootstrap command when the worker is
	// missing Docker or the compose plugin. Cargo never auto-installs.
	InstallHint string
}

var ErrUnreachable = errors.New("host is unreachable")

// Verify connects to the host through its materialized Target, records
// liveness plus capacity, and returns the result. A missing daemon or
// compose plugin degrades to 'degraded' plus an install hint rather than
// failing: the host exists, it just cannot take work yet.
func (m *Manager) Verify(ctx context.Context, h sqlc.Host) (VerifyResult, error) {
	tg, err := m.mat.Target(ctx, h)
	res := VerifyResult{Status: statusDegraded}
	if err != nil {
		if errors.Is(err, reconciler.ErrHostKeyMismatch) {
			_ = m.hostSvc.SetStatus(ctx, h.ID.String(), statusDegraded)
			return res, fmt.Errorf("%w (verify aborted)", err)
		}
		_ = m.hostSvc.SetStatus(ctx, h.ID.String(), "unreachable")
		return res, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}

	engine, err := m.dockerVersion(ctx, tg.Env)
	if err != nil {
		_ = m.hostSvc.SetStatus(ctx, h.ID.String(), "unreachable")
		return res, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if engine == "" {
		res.InstallHint = "curl -fsSL https://get.docker.com | sh"
		_ = m.hostSvc.SetStatus(ctx, h.ID.String(), statusDegraded)
		return res, nil
	}
	if _, err := m.run(ctx, tg.Env, "docker", "compose", "version"); err != nil {
		res.EngineVersion = engine
		res.InstallHint = "apt-get update && apt-get install -y docker-compose-plugin"
		_ = m.hostSvc.SetStatusVersioned(ctx, h.ID.String(), statusDegraded, engine)
		return res, nil
	}
	res.Status = statusOnline
	res.EngineVersion = engine
	res.CPUCount = m.remoteNproc(ctx, tg.Env)
	res.MemTotalMB = m.remoteMemMB(ctx, tg.Env)
	err = m.hostSvc.UpdateCapacity(ctx, h.ID.String(), statusOnline, engine,
		int32(res.CPUCount), res.MemTotalMB)
	return res, err
}

// dockerVersion returns the Engine version, "" when the binary answers but no
// server responds (daemon down vs CLI missing are indistinguishable enough
// here: both mean "cannot take deploys").
func (m *Manager) dockerVersion(ctx context.Context, env []string) (string, error) {
	out, err := m.run(ctx, env, "docker", "version", "--format", "{{json .}}")
	if err != nil {
		return "", err
	}
	var v struct {
		Server struct {
			Version string `json:"Version"`
		} `json:"Server"`
	}
	if json.Unmarshal([]byte(out), &v) != nil || v.Server.Version == "" {
		return "", nil
	}
	return v.Server.Version, nil
}

func (m *Manager) remoteNproc(ctx context.Context, env []string) int {
	out, err := m.run(ctx, env, "nproc")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

func (m *Manager) remoteMemMB(ctx context.Context, env []string) int64 {
	out, err := m.run(ctx, env, "free", "-m")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// Mem:  15000  4200  6100 ...
		if strings.HasPrefix(line, "Mem:") && len(fields) >= 2 {
			mb, _ := strconv.ParseInt(fields[1], 10, 64)
			return mb
		}
	}
	return 0
}
