// Package hostmgr materializes and verifies worker-host execution
// environments. It turns a sealed hosts row into everything a docker CLI
// invocation needs to run against that host — an isolated DOCKER_CONFIG with
// a dedicated context, and a managed HOME whose .ssh holds the decrypted key
// and pins the host's fingerprint — without ever touching the control
// plane's own ~/.docker or ~/.ssh.
package hostmgr

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	contextName      = "cargo-worker"
	scanTimeout      = 15 * time.Second
	ctxCreateTimeout = 30 * time.Second
)

// ErrNoKey means the hosts row has no stored private key (the operator must
// re-enter it; see hosts.ErrKeyUnreadable for the unreadable case).
var ErrNoKey = errors.New("host has no stored SSH key")

// KeyOpener decrypts a host's sealed private key. Satisfied by
// *hosts.Service via a setter to avoid an import cycle in the other
// direction.
type KeyOpener interface {
	OpenKey(ctx context.Context, h sqlc.Host) ([]byte, error)
}

// scanFunc probes a host once for its public host key. It returns the
// SHA256:" fingerprint and the raw known_hosts line(s) from ssh-keyscan.
type scanFunc func(ctx context.Context, h sqlc.Host) (fingerprint string, knownHosts string, err error)

// createContextFunc ensures the docker context exists under env.
type createFunc func(env []string, name, endpoint string) error

type Materializer struct {
	dataDir string
	keys    KeyOpener
	scan    scanFunc
	// createContext defaults to `docker context create`; injectable so tests
	// never shell out.
	createContext createFunc
	// onPin persists a TOFU fingerprint (wired to PinHostFingerprint); nil in
	// tests.
	onPin func(ctx context.Context, h sqlc.Host, fingerprint string) error

	mu     sync.Mutex
	pinned map[string]string // hostID -> verified fingerprint this process
}

func NewMaterializer(dataDir string, keys KeyOpener) *Materializer {
	return &Materializer{dataDir: dataDir, keys: keys, pinned: map[string]string{}}
}

// Target materializes the per-host environment and returns what a provider
// invocation needs to use it. The DB row is the source of truth for the
// pinned fingerprint: pass the row as currently read.
//
// TOFU: an empty fingerprint is pinned from this first scan; any later
// mismatch aborts with reconciler.ErrHostKeyMismatch.
func (m *Materializer) Target(ctx context.Context, h sqlc.Host) (reconciler.Target, error) {
	base := m.hostDir(h.ID)
	if err := os.MkdirAll(filepath.Join(base, "docker"), 0o700); err != nil {
		return reconciler.Target{}, fmt.Errorf("docker config dir: %w", err)
	}
	sshDir := filepath.Join(base, "home", ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return reconciler.Target{}, fmt.Errorf("home ssh dir: %w", err)
	}

	keyPEM, err := m.keys.OpenKey(ctx, h)
	if err != nil {
		return reconciler.Target{}, err // hosts.ErrKeyUnreadable / ErrNoKey pass through
	}
	keyPath := filepath.Join(sshDir, "id_ed25519")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return reconciler.Target{}, fmt.Errorf("write key: %w", err)
	}
	sshCfg := strings.Join([]string{
		"Host *",
		"\tStrictHostKeyChecking yes",
		"\tIdentitiesOnly yes",
		"\tIdentityFile ~/.ssh/id_ed25519",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(sshCfg), 0o600); err != nil {
		return reconciler.Target{}, fmt.Errorf("write ssh config: %w", err)
	}

	endpoint := endpointFor(h)
	if err := m.pinKnownHosts(ctx, h, filepath.Join(sshDir, "known_hosts")); err != nil {
		return reconciler.Target{}, err
	}

	env := []string{
		"DOCKER_CONFIG=" + filepath.Join(base, "docker"),
		"HOME=" + filepath.Join(base, "home"),
		"DOCKER_CONTEXT=" + contextName,
	}
	if err := m.ensureContext(ctx, env, endpoint); err != nil {
		return reconciler.Target{}, err
	}
	return reconciler.Target{HostID: h.ID.String(), Endpoint: endpoint, Env: env}, nil
}

// pinKnownHosts scans the host (when needed), enforces the pinned
// fingerprint, and writes the known_hosts file.
func (m *Materializer) pinKnownHosts(ctx context.Context, h sqlc.Host, khPath string) error {
	pinned := ""
	if h.HostKeyFingerprint.Valid {
		pinned = h.HostKeyFingerprint.String
	}
	if pinned == "" {
		fp, line, err := m.scanHost(ctx, h)
		if err != nil {
			return fmt.Errorf("scan host key: %w", err)
		}
		if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
			return fmt.Errorf("write known_hosts: %w", err)
		}
		if m.onPin != nil {
			if err := m.onPin(ctx, h, fp); err != nil {
				return fmt.Errorf("pin fingerprint: %w", err)
			}
		}
		m.remember(h.ID.String(), fp)
		return nil
	}
	if m.verified(h.ID.String(), pinned) {
		return nil // verified on a previous call this process; nothing to redo
	}
	// Verify even when already written to disk: the file could have been
	// tampered with independently of the database.
	fp, line, err := m.scanHost(ctx, h)
	if err != nil {
		return fmt.Errorf("verify host key: %w", err)
	}
	if fp != pinned {
		return fmt.Errorf("%w: got %s, want %s", reconciler.ErrHostKeyMismatch, fp, pinned)
	}
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		return fmt.Errorf("write known_hosts: %w", err)
	}
	m.remember(h.ID.String(), fp)
	return nil
}

func (m *Materializer) remember(hostID, fp string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pinned == nil {
		m.pinned = map[string]string{}
	}
	m.pinned[hostID] = fp
}

func (m *Materializer) verified(hostID, want string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinned[hostID] == want && want != ""
}

func (m *Materializer) scanHost(ctx context.Context, h sqlc.Host) (string, string, error) {
	if m.scan != nil {
		return m.scan(ctx, h)
	}
	cctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ssh-keyscan",
		"-p", fmt.Sprint(h.Port), hostAddress(h)).Output()
	if err != nil {
		return "", "", fmt.Errorf("ssh-keyscan %s: %w", hostAddress(h), err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "#") && strings.Count(l, " ") >= 2 {
			fp, err := fingerprintFromScan(l)
			if err != nil {
				continue
			}
			return fp, l, nil
		}
	}
	return "", "", errors.New("ssh-keyscan returned no usable host key")
}

// ensureContext creates the docker context inside the isolated DOCKER_CONFIG,
// idempotently ("already exists" is success).
func (m *Materializer) ensureContext(ctx context.Context, env []string, endpoint string) error {
	if m.createContext != nil {
		return m.createContext(env, contextName, endpoint)
	}
	cctx, cancel := context.WithTimeout(ctx, ctxCreateTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "context", "create",
		contextName, "--docker", "host="+endpoint)
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		// Idempotent: the context persists across calls in the same config dir.
		check := exec.CommandContext(cctx, "docker", "context", "inspect", contextName)
		check.Env = append(os.Environ(), env...)
		if checkErr := check.Run(); checkErr == nil {
			return nil
		}
		return fmt.Errorf("docker context create %s: %w", endpoint, err)
	}
	return nil
}

func (m *Materializer) hostDir(id pgtype.UUID) string {
	return filepath.Join(m.dataDir, "hosts", id.String())
}

func endpointFor(h sqlc.Host) string {
	if h.Port == 22 || h.Port == 0 {
		return "ssh://" + h.Address
	}
	return fmt.Sprintf("ssh://%s:%d", h.Address, h.Port)
}

func hostAddress(h sqlc.Host) string {
	// user@host -> host for keyscan purposes (the key belongs to the host).
	if i := strings.IndexByte(h.Address, '@'); i >= 0 {
		return h.Address[i+1:]
	}
	return h.Address
}

// fingerprintFromScan computes SHA256:<base64> over the base64-decoded key
// blob of one ssh-keyscan output line — the same format
// `ssh-keygen -lf` prints.
func fingerprintFromScan(line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", fmt.Errorf("malformed known_hosts line")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		return "", fmt.Errorf("decode key blob: %w", err)
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(sum[:]), nil
}
