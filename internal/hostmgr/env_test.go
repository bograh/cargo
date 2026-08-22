package hostmgr

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/ssh"
)

func fixture() sqlc.Host {
	id := pgtype.UUID{Valid: true}
	id.Bytes[15] = 7
	return sqlc.Host{
		ID: id, Name: "w1", Address: "deploy@10.0.0.5", Port: 2222,
		Status: "pending", AppsDomainSuffix: "w1.example.com",
		LetsencryptEmail: "ops@example.com",
	}
}

// stubKeys satisfies KeyOpener with a fixed plaintext.
type stubKeys struct{}

func (s stubKeys) OpenKey(ctx context.Context, h sqlc.Host) ([]byte, error) {
	return []byte("test-key-pem"), nil
}

// scanLine is a real, self-consistent known_hosts line: a freshly generated
// ed25519 public key in wire format, exactly what ssh-keyscan emits.
var scanLine = func() string {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		panic(err)
	}
	b64 := base64.StdEncoding.EncodeToString(pub.Marshal())
	return "[10.0.0.5]:2222 ssh-ed25519 " + b64
}()

// fingerprintOf hashes the key blob the same way the real implementation does,
// so the stub and the production path agree.
func fingerprintOf(t *testing.T, line string) string {
	t.Helper()
	fp, err := fingerprintFromScan(line)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}

func TestTargetMaterializesIsolatedDirs(t *testing.T) {
	dir := t.TempDir()
	h := fixture()
	scanned := false
	m := &Materializer{
		dataDir: dir,
		keys:    stubKeys{},
		scan: func(ctx context.Context, h sqlc.Host) (string, string, error) {
			scanned = true
			return fingerprintOf(t, scanLine), scanLine, nil
		},
		createContext: func(env []string, name, endpoint string) error { return nil },
	}

	tg, err := m.Target(context.Background(), h)
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if !scanned {
		t.Fatal("host key was never scanned")
	}
	if tg.HostID != h.ID.String() || tg.Endpoint != "ssh://deploy@10.0.0.5:2222" {
		t.Fatalf("target = %+v", tg)
	}
	env := map[string]string{}
	for _, kv := range tg.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	wantBase := filepath.Join(dir, "hosts", h.ID.String())
	if env["DOCKER_CONFIG"] != filepath.Join(wantBase, "docker") ||
		env["HOME"] != filepath.Join(wantBase, "home") ||
		env["DOCKER_CONTEXT"] != contextName {
		t.Fatalf("env = %v", env)
	}

	key, err := os.ReadFile(filepath.Join(wantBase, "home", ".ssh", "id_ed25519"))
	if err != nil || string(key) != "test-key-pem" {
		t.Fatalf("key file: %q (%v)", key, err)
	}
	st, err := os.Stat(filepath.Join(wantBase, "home", ".ssh", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("key perm = %o, want 600", got)
	}
	cfg, err := os.ReadFile(filepath.Join(wantBase, "home", ".ssh", "config"))
	if err != nil || !strings.Contains(string(cfg), "StrictHostKeyChecking yes") {
		t.Fatalf("ssh config: %q (%v)", cfg, err)
	}
	kh, err := os.ReadFile(filepath.Join(wantBase, "home", ".ssh", "known_hosts"))
	if err != nil || !strings.Contains(string(kh), scanLine) {
		t.Fatalf("known_hosts: %q (%v)", kh, err)
	}

	// Second call is stable and does not re-scan: onPin persisted the
	// fingerprint into the row, as the API layer does in production.
	h.HostKeyFingerprint = pgtype.Text{String: fingerprintOf(t, scanLine), Valid: true}
	m.scan = func(ctx context.Context, h sqlc.Host) (string, string, error) {
		t.Fatal("re-scanned a pinned host")
		return "", "", nil
	}
	if _, err := m.Target(context.Background(), h); err != nil {
		t.Fatalf("second Target: %v", err)
	}
}

func TestFingerprintMismatchAborts(t *testing.T) {
	dir := t.TempDir()
	h := fixture()
	m := &Materializer{
		dataDir: dir,
		keys:    stubKeys{},
		scan: func(ctx context.Context, h sqlc.Host) (string, string, error) {
			return fingerprintOf(t, scanLine), scanLine, nil
		},
		createContext: func(env []string, name, endpoint string) error { return nil },
	}
	if _, err := m.Target(context.Background(), h); err != nil {
		t.Fatalf("first Target: %v", err)
	}

	// Pin recorded: same fingerprint verifies without re-scanning...
	pinned := h
	pinned.HostKeyFingerprint = pgtype.Text{String: fingerprintOf(t, scanLine), Valid: true}
	m.scan = func(ctx context.Context, h sqlc.Host) (string, string, error) {
		t.Fatal("re-scanned a pinned host")
		return "", "", nil
	}
	if _, err := m.Target(context.Background(), pinned); err != nil {
		t.Fatalf("pinned Target: %v", err)
	}

	// ...and the host's key changing under us aborts every operation.
	rotated := h
	rotated.HostKeyFingerprint = pgtype.Text{String: "SHA256:someone-elses-key", Valid: true}
	m.scan = func(ctx context.Context, h sqlc.Host) (string, string, error) {
		return fingerprintOf(t, scanLine), scanLine, nil
	}
	if _, err := m.Target(context.Background(), rotated); !errors.Is(err, reconciler.ErrHostKeyMismatch) {
		t.Fatalf("err = %v, want ErrHostKeyMismatch", err)
	}
}
