package hosts

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/ssh"
)

func contains(haystack, needle []byte) bool {
	return bytes.Contains(haystack, needle)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	pool := startPool(t)
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	return NewService(pool, box)
}

func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "cargo test")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func TestCreateSealsKeyAndOpenRoundTrips(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	key := testKeyPEM(t)

	h, err := svc.Create(ctx, CreateInput{
		Name: "w1", Address: "deploy@10.0.0.5", Port: 22,
		KeyPEM:           key,
		DomainSuffix:     "w1.example.com",
		LetsEncryptEmail: "ops@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if h.Status != "pending" {
		t.Fatalf("status = %q, want pending", h.Status)
	}

	var raw []byte
	if err := svc.pool.QueryRow(ctx,
		`SELECT private_key_enc FROM hosts WHERE id = $1`, h.ID).Scan(&raw); err != nil {
		t.Fatalf("read sealed column: %v", err)
	}
	if len(raw) == 0 || contains(raw, []byte("PRIVATE KEY")) {
		t.Fatal("key not sealed at rest")
	}

	got, err := svc.OpenKey(ctx, h)
	if err != nil {
		t.Fatalf("OpenKey: %v", err)
	}
	if string(got) != string(key) {
		t.Fatal("OpenKey did not round-trip the PEM")
	}
}

func TestCreateValidatesKeyPEM(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	if _, err := svc.Create(ctx, CreateInput{
		Name: "bad", Address: "deploy@10.0.0.6", Port: 22,
		KeyPEM: []byte("not a pem"),
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestDeleteRefusedWhileAppsAssigned(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	h, err := svc.Create(ctx, CreateInput{
		Name: "busy", Address: "deploy@10.0.0.7", Port: 22, KeyPEM: testKeyPEM(t),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var orgID [16]byte
	orgID[15] = 1
	if _, err := svc.pool.Exec(ctx,
		`INSERT INTO organizations (id, name, slug) VALUES ($1, 'org', 'org')`, orgID[:]); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	var appID [16]byte
	appID[15] = 2
	if _, err := svc.pool.Exec(ctx, `
		INSERT INTO applications (id, org_id, name, slug, exposed_port, source_type)
		VALUES ($2, $1, 'app', 'app', 8080, 'git')`, orgID[:], appID[:]); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := svc.pool.Exec(ctx,
		`UPDATE applications SET host_id = $1 WHERE id = $2`, h.ID, appID[:]); err != nil {
		t.Fatalf("assign host: %v", err)
	}
	if err := svc.Delete(ctx, h.ID.String()); !errors.Is(err, ErrHostInUse) {
		t.Fatalf("err = %v, want ErrHostInUse", err)
	}
}

func TestDeleteRemovesUnassignedHost(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	h, err := svc.Create(ctx, CreateInput{
		Name: "free", Address: "deploy@10.0.0.9", Port: 22, KeyPEM: testKeyPEM(t),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, h.ID.String()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.q.GetHost(ctx, h.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want pgx.ErrNoRows", err)
	}
}

func TestOpenKeyWrongKeyFails(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	key := testKeyPEM(t)
	h, err := svc.Create(ctx, CreateInput{
		Name: "w2", Address: "deploy@10.0.0.8", Port: 22, KeyPEM: key,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wrongBox, _ := crypto.New([]byte("fedcba9876543210fedcba9876543210"))
	wrong := &Service{pool: svc.pool, q: svc.q, box: wrongBox}
	if got, err := wrong.OpenKey(ctx, h); err == nil {
		t.Fatalf("OpenKey with wrong key returned %d bytes, want error", len(got))
	} else if !errors.Is(err, ErrKeyUnreadable) {
		t.Fatalf("err = %v, want ErrKeyUnreadable", err)
	}
}
