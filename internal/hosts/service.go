// Package hosts manages worker hosts: sealed SSH credentials, deletion
// guards, and the seam the deploy pipeline uses to route an app to its host.
//
// SSH private keys are sealed with crypto.Box before they ever touch disk or
// wire; the plaintext exists only in memory, materialized per invocation by
// internal/hostmgr.
package hosts

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"
)

var (
	ErrNotFound      = errors.New("host not found")
	ErrValidation    = errors.New("validation failed")
	ErrHostInUse     = errors.New("host still has applications assigned")
	ErrKeyUnreadable = errors.New("stored SSH key could not be decrypted with the current master key")
)

type Service struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
	box  *crypto.Box
}

func NewService(pool *pgxpool.Pool, box *crypto.Box) *Service {
	return &Service{pool: pool, q: sqlc.New(pool), box: box}
}

type CreateInput struct {
	Name             string
	Address          string // user@host
	Port             int
	KeyPEM           []byte
	DomainSuffix     string
	LetsEncryptEmail string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (sqlc.Host, error) {
	if in.Name == "" || in.Address == "" {
		return sqlc.Host{}, fmt.Errorf("%w: name and address are required", ErrValidation)
	}
	if in.Port == 0 {
		in.Port = 22
	}
	if _, err := parseKeyPEM(in.KeyPEM); err != nil {
		return sqlc.Host{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	sealed, err := s.box.Seal(in.KeyPEM)
	if err != nil {
		return sqlc.Host{}, fmt.Errorf("seal key: %w", err)
	}
	h, err := s.q.CreateHost(ctx, sqlc.CreateHostParams{
		Name:             in.Name,
		Address:          in.Address,
		Port:             int32(in.Port),
		PrivateKeyEnc:    sealed,
		AppsDomainSuffix: in.DomainSuffix,
		LetsencryptEmail: in.LetsEncryptEmail,
	})
	if err != nil {
		return sqlc.Host{}, fmt.Errorf("create host: %w", err)
	}
	return h, nil
}

// OpenKey decrypts a host's stored private key. The plaintext never leaves
// memory here; callers materialize it to a 0600 file under the host's
// isolated HOME (see internal/hostmgr).
func (s *Service) OpenKey(ctx context.Context, h sqlc.Host) ([]byte, error) {
	if len(h.PrivateKeyEnc) == 0 {
		return nil, fmt.Errorf("%w: no key stored", ErrKeyUnreadable)
	}
	plain, err := s.box.Open(h.PrivateKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnreadable, err)
	}
	return plain, nil
}

func (s *Service) List(ctx context.Context) ([]sqlc.Host, error) {
	return s.q.ListHosts(ctx)
}

func (s *Service) Get(ctx context.Context, id pgtype.UUID) (sqlc.Host, error) {
	h, err := s.q.GetHost(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Host{}, ErrNotFound
	}
	return h, err
}

// Delete removes a host unless applications still target it. Assignments are
// the operator's call to move first — silent re-homing would strand apps on
// DNS that points nowhere.
func (s *Service) Delete(ctx context.Context, id string) error {
	var count int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM applications WHERE host_id = $1::uuid`, id).Scan(&count); err != nil {
		return fmt.Errorf("count apps: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("%w (%d)", ErrHostInUse, count)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM hosts WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("delete host: %w", err)
	}
	return nil
}

// ReplaceKey re-seals and stores a new private key for the host (the
// recovery path when a stored key is unreadable or was rotated on the
// worker).
func (s *Service) ReplaceKey(ctx context.Context, id, keyPEM string) error {
	if _, err := parseKeyPEM([]byte(keyPEM)); err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	sealed, err := s.box.Seal([]byte(keyPEM))
	if err != nil {
		return fmt.Errorf("seal key: %w", err)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE hosts SET private_key_enc = $2 WHERE id = $1::uuid`, id, sealed)
	return err
}

// GetByID loads one host by textual UUID.
func (s *Service) GetByID(ctx context.Context, id string) (sqlc.Host, error) {
	var h sqlc.Host
	err := s.pool.QueryRow(ctx, `SELECT * FROM hosts WHERE id = $1::uuid`, id).Scan(
		&h.ID, &h.Name, &h.Address, &h.Port, &h.PrivateKeyEnc, &h.KeyVersion,
		&h.HostKeyFingerprint, &h.Status, &h.EngineVersion, &h.CpuCount,
		&h.MemTotalMb, &h.AppsDomainSuffix, &h.LetsencryptEmail, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Host{}, ErrNotFound
	}
	return h, err
}

// PinFingerprint persists the TOFU-pinned host key fingerprint.
func (s *Service) PinFingerprint(ctx context.Context, id, fingerprint string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE hosts SET host_key_fingerprint = $2 WHERE id = $1::uuid`, id, fingerprint)
	return err
}

// SetStatus records liveness without capacity detail.
func (s *Service) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE hosts SET status = $2 WHERE id = $1::uuid`, id, status)
	return err
}

// SetStatusVersioned records liveness plus the observed Engine version.
func (s *Service) SetStatusVersioned(ctx context.Context, id, status, engineVersion string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE hosts SET status = $2, engine_version = $3 WHERE id = $1::uuid`,
		id, status, pgText(engineVersion))
	return err
}

// UpdateCapacity records a full verification result.
func (s *Service) UpdateCapacity(ctx context.Context, id, status, engineVersion string, cpu int32, memTotalMB int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE hosts SET status = $2, engine_version = $3, cpu_count = $4,
		                  mem_total_mb = $5
		WHERE id = $1::uuid`, id, status, pgText(engineVersion), cpu, memTotalMB)
	return err
}

func pgText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// parseKeyPEM accepts any private key shape ssh can use (OpenSSH, PEM RSA /
// ECDSA / PKCS8). ssh.ParsePrivateKey handles every supported container.
func parseKeyPEM(raw []byte) (ssh.Signer, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	return ssh.ParsePrivateKey(raw)
}
