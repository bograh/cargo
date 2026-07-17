package apps

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	svc           *Service
	orgID         pgtype.UUID
	owner, viewer pgtype.UUID
	outsider      pgtype.UUID
}

func setup(t *testing.T) (*fixture, *pgxpool.Pool) {
	t.Helper()
	pool := startPool(t)
	box, err := crypto.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(pool)
	orgSvc := orgs.NewService(pool)
	ctx := context.Background()
	reg := func(email string) pgtype.UUID {
		u, _, err := authSvc.Register(ctx, email, "password-123")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return u.ID
	}
	owner, viewer, outsider := reg("o@x.co"), reg("v@x.co"), reg("out@x.co")
	org, err := orgSvc.Create(ctx, "Acme", owner)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	if err := orgSvc.AddMember(ctx, org.ID, viewer, "viewer"); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	return &fixture{svc: NewService(pool, box), orgID: org.ID, owner: owner, viewer: viewer, outsider: outsider}, pool
}

func imageInput() CreateInput {
	return CreateInput{Name: "My API", SourceType: "image", ImageRef: "nginx:alpine",
		ExposedPort: 80, HealthcheckPath: "/", AutoDeploy: true}
}

func TestCreateAndGetApp(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.Slug != "my-api" || app.SourceType != "image" {
		t.Fatalf("app = %+v", app)
	}
	if _, err := f.svc.Get(ctx, app.ID, f.viewer); err != nil {
		t.Fatalf("viewer get: %v", err)
	}
	if _, err := f.svc.Get(ctx, app.ID, f.outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider get = %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	bad := imageInput()
	bad.ImageRef = ""
	if _, err := f.svc.Create(ctx, f.orgID, f.owner, bad); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing image_ref err = %v", err)
	}
	git := CreateInput{Name: "web", SourceType: "git", ExposedPort: 3000}
	if _, err := f.svc.Create(ctx, f.orgID, f.owner, git); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing repo err = %v", err)
	}
	if _, err := f.svc.Create(ctx, f.orgID, f.viewer, imageInput()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create err = %v", err)
	}
}

func TestDeleteRequiresAdmin(t *testing.T) {
	f, pool := setup(t)
	ctx := context.Background()
	member, _, err := auth.NewService(pool).Register(ctx, "m@x.co", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := orgs.NewService(pool).AddMember(ctx, f.orgID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Delete(ctx, app.ID, member.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member delete err = %v", err)
	}
	if err := f.svc.Delete(ctx, app.ID, f.owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

func TestEnvVarsEncryptedAndWriteOnly(t *testing.T) {
	f, pool := setup(t)
	ctx := context.Background()
	app, err := f.svc.Create(ctx, f.orgID, f.owner, imageInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetEnvVars(ctx, app.ID, f.owner, map[string]string{"DB_URL": "postgres://secret"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	keys, err := f.svc.ListEnvKeys(ctx, app.ID, f.viewer)
	if err != nil || len(keys) != 1 || keys[0] != "DB_URL" {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT value_enc FROM env_vars WHERE key='DB_URL'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("postgres://secret")) {
		t.Fatal("env value stored in plaintext")
	}
	env, err := f.svc.DecryptedEnv(ctx, app.ID)
	if err != nil || env["DB_URL"] != "postgres://secret" {
		t.Fatalf("decrypted = %v, %v", env, err)
	}
	if err := f.svc.SetEnvVars(ctx, app.ID, f.viewer, map[string]string{"X": "y"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer set err = %v", err)
	}
	if err := f.svc.DeleteEnvVar(ctx, app.ID, f.owner, "DB_URL"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestRegistryCredsEncrypted(t *testing.T) {
	f, pool := setup(t)
	ctx := context.Background()
	in := imageInput()
	in.RegistryCreds = &RegistryCreds{Server: "ghcr.io", Username: "bot", Password: "hunter2"}
	app, err := f.svc.Create(ctx, f.orgID, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT registry_creds_enc FROM applications WHERE id=$1`, app.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Fatal("registry password stored in plaintext")
	}
	creds, err := f.svc.DecryptedRegistryCreds(ctx, app)
	if err != nil || creds.Password != "hunter2" {
		t.Fatalf("creds = %v, %v", creds, err)
	}
}
