package databases

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bograh/cargo/internal/apps"
	"github.com/bograh/cargo/internal/auth"
	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/bograh/cargo/internal/reconciler"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type env struct {
	svc      *Service
	appsSvc  *apps.Service
	provider reconciler.DatabaseProvider
	orgID    pgtype.UUID
	owner    pgtype.UUID
}

func setup(t *testing.T) *env {
	t.Helper()
	pool := startPool(t)
	box, err := crypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	provider := reconciler.NewDocker(t.TempDir())
	ctx := context.Background()
	authSvc := auth.NewService(pool)
	orgSvc := orgs.NewService(pool)
	owner, _, err := authSvc.Register(ctx, "owner@x.co", "password-123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	org, err := orgSvc.Create(ctx, "Acme", owner.ID)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	return &env{
		svc:      NewService(pool, box, provider, t.TempDir()),
		appsSvc:  apps.NewService(pool, box),
		provider: provider,
		orgID:    org.ID,
		owner:    owner.ID,
	}
}

func (e *env) newApp(t *testing.T, name string) pgtype.UUID {
	t.Helper()
	app, err := e.appsSvc.Create(context.Background(), e.orgID, e.owner, apps.CreateInput{
		Name: name, SourceType: "image", ImageRef: "nginx:alpine", ExposedPort: 80,
	})
	if err != nil {
		t.Fatalf("create app %s: %v", name, err)
	}
	return app.ID
}

// hostRewrite swaps the in-cluster container host (cargo-db-<id>:<port>) for
// 127.0.0.1:<hostPort>, since the container DNS name only resolves inside the
// cargo-data network. Tests provision with ExposePort so the port is
// published to the host.
func hostRewrite(t *testing.T, rawURL string, hostPort int32) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Host = fmt.Sprintf("127.0.0.1:%d", hostPort)
	return u.String()
}

func (e *env) provision(t *testing.T, in CreateInput) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	inst, err := e.svc.Create(ctx, e.orgID, e.owner, in)
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	t.Cleanup(func() { _ = e.provider.TeardownDB(context.Background(), uuidStr(inst.ID), os.Stderr) })
	if err := e.svc.Provision(ctx, inst.ID); err != nil {
		t.Fatalf("provision: %v", err)
	}
	got, err := e.svc.Get(ctx, inst.ID, e.owner)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Instance.Status != "running" {
		t.Fatalf("status = %s, want running", got.Instance.Status)
	}
	return inst.ID
}

func pgCanConnect(ctx context.Context, url string) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	var one int
	return conn.QueryRow(ctx, "SELECT 1").Scan(&one)
}

func TestPostgresLifecycle(t *testing.T) {
	requireDocker(t)
	e := setup(t)
	ctx := context.Background()
	instID := e.provision(t, CreateInput{Name: "orders-db", Engine: "postgres", Version: "16", ExposePort: true})

	inst, _ := e.svc.Get(ctx, instID, e.owner)
	port := inst.Instance.HostPort.Int32

	appA := e.newApp(t, "App A")
	appB := e.newApp(t, "App B")

	urlA, _, err := e.svc.Attach(ctx, instID, appA, e.owner)
	if err != nil {
		t.Fatalf("attach A: %v", err)
	}
	urlB, _, err := e.svc.Attach(ctx, instID, appB, e.owner)
	if err != nil {
		t.Fatalf("attach B: %v", err)
	}

	connA := hostRewrite(t, urlA, port)
	connB := hostRewrite(t, urlB, port)

	if err := pgCanConnect(ctx, connA); err != nil {
		t.Fatalf("A -> own db: %v", err)
	}
	if err := pgCanConnect(ctx, connB); err != nil {
		t.Fatalf("B -> own db: %v", err)
	}

	// Isolation: A's role connecting to B's database must fail.
	crossURL := strings.Replace(connA, "/app_app_a?", "/app_app_b?", 1)
	if err := pgCanConnect(ctx, crossURL); err == nil {
		t.Fatal("A connected to B's database, expected failure")
	}

	// Duplicate attach of the same app -> conflict.
	if _, _, err := e.svc.Attach(ctx, instID, appA, e.owner); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup attach = %v, want ErrConflict", err)
	}

	// User-set DATABASE_URL collision -> conflict.
	appC := e.newApp(t, "App C")
	if err := e.appsSvc.SetEnvVars(ctx, appC, e.owner, map[string]string{"DATABASE_URL": "postgres://x"}); err != nil {
		t.Fatalf("set env: %v", err)
	}
	if _, _, err := e.svc.Attach(ctx, instID, appC, e.owner); !errors.Is(err, ErrConflict) {
		t.Fatalf("env collision attach = %v, want ErrConflict", err)
	}

	// Delete while attached -> conflict.
	if err := e.svc.Delete(ctx, instID, e.owner); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete attached = %v, want ErrConflict", err)
	}

	// EnvFor rebuilds the URL from stored secrets.
	envA, err := e.svc.EnvFor(ctx, appA)
	if err != nil {
		t.Fatalf("envfor: %v", err)
	}
	if envA["DATABASE_URL"] != urlA {
		t.Fatalf("envfor url = %q, want %q", envA["DATABASE_URL"], urlA)
	}

	// Detach A: login must then fail; re-attach must work.
	if err := e.svc.Detach(ctx, instID, appA, e.owner); err != nil {
		t.Fatalf("detach A: %v", err)
	}
	if err := pgCanConnect(ctx, connA); err == nil {
		t.Fatal("A connected after detach, expected failure")
	}
	if _, _, err := e.svc.Attach(ctx, instID, appA, e.owner); err != nil {
		t.Fatalf("re-attach A: %v", err)
	}

	// Snapshot: creates a non-empty file listed by ListSnapshots.
	name, err := e.svc.Snapshot(ctx, instID, e.owner)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snaps, err := e.svc.ListSnapshots(ctx, instID, e.owner)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	found := false
	for _, s := range snaps {
		if s.Name == name {
			found = true
			if s.Size == 0 {
				t.Fatal("snapshot file is empty")
			}
		}
	}
	if !found {
		t.Fatalf("snapshot %q not listed", name)
	}
	if _, err := e.svc.SnapshotPath(ctx, instID, e.owner, "../etc/passwd"); !errors.Is(err, ErrValidation) {
		t.Fatalf("traversal = %v, want ErrValidation", err)
	}
	if _, err := e.svc.SnapshotPath(ctx, instID, e.owner, name); err != nil {
		t.Fatalf("valid snapshot path: %v", err)
	}
}

func TestRedisACLIsolation(t *testing.T) {
	requireDocker(t)
	e := setup(t)
	ctx := context.Background()
	instID := e.provision(t, CreateInput{Name: "cache", Engine: "redis", Version: "7", RedisMode: "acl", ExposePort: true})
	inst, _ := e.svc.Get(ctx, instID, e.owner)
	port := inst.Instance.HostPort.Int32

	appA := e.newApp(t, "App A")
	appB := e.newApp(t, "App B")
	urlA, attA, err := e.svc.Attach(ctx, instID, appA, e.owner)
	if err != nil {
		t.Fatalf("attach A: %v", err)
	}
	urlB, attB, err := e.svc.Attach(ctx, instID, appB, e.owner)
	if err != nil {
		t.Fatalf("attach B: %v", err)
	}
	if attA.DbIndex.Int32 == attB.DbIndex.Int32 {
		t.Fatalf("A and B share index %d", attA.DbIndex.Int32)
	}

	ua := mustParseRedis(t, urlA)
	ub := mustParseRedis(t, urlB)

	// A writes to its own index and reads it back.
	rc := dialRedis(t, port)
	defer rc.Close()
	rc.cmd(t, "AUTH", ua.user, ua.pass)
	rc.cmd(t, "SELECT", ua.idx)
	rc.cmd(t, "SET", "k", "va")
	if got := rc.cmd(t, "GET", "k"); got != "va" {
		t.Fatalf("A GET = %q, want va", got)
	}
	// A cannot SELECT into B's index.
	if _, err := rc.cmdErr("SELECT", ub.idx); err == nil {
		t.Fatal("A selected B's index, expected NOPERM")
	}

	// A cannot AUTH as B.
	rc2 := dialRedis(t, port)
	defer rc2.Close()
	if _, err := rc2.cmdErr("AUTH", ua.user, ub.pass); err == nil {
		t.Fatal("A authenticated with B's password, expected failure")
	}

	// acl mode grants no pub/sub: channels are global (not db-index scoped),
	// so SUBSCRIBE must be denied (NOPERM). rc is still authed as A.
	if _, err := rc.cmdErr("SUBSCRIBE", "ch"); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("A SUBSCRIBE = %v, want NOPERM (acl mode has no pub/sub)", err)
	}
}

// TestAppDeleteDropsRedisACLUser is the cross-tenant regression for the
// orphaned-credential breach: deleting an app must remove its redis ACL user
// on the engine, not just cascade-delete the attachment row, so a retained old
// REDIS_URL cannot authenticate against a freed-and-reassigned db index.
func TestAppDeleteDropsRedisACLUser(t *testing.T) {
	requireDocker(t)
	e := setup(t)
	e.appsSvc.SetAttachmentCleaner(e.svc)
	ctx := context.Background()
	instID := e.provision(t, CreateInput{Name: "cache", Engine: "redis", Version: "7", RedisMode: "acl", ExposePort: true})
	inst, _ := e.svc.Get(ctx, instID, e.owner)
	port := inst.Instance.HostPort.Int32

	appA := e.newApp(t, "App A")
	urlA, attA, err := e.svc.Attach(ctx, instID, appA, e.owner)
	if err != nil {
		t.Fatalf("attach A: %v", err)
	}
	if attA.DbIndex.Int32 != 0 {
		t.Fatalf("first attach index = %d, want 0", attA.DbIndex.Int32)
	}
	ua := mustParseRedis(t, urlA)

	// Old credential authenticates before the app is deleted.
	rc := dialRedis(t, port)
	if _, err := rc.cmdErr("AUTH", ua.user, ua.pass); err != nil {
		t.Fatalf("A auth before delete: %v", err)
	}
	rc.Close()

	// Deleting the app must run engine cleanup (ACL DELUSER).
	if err := e.appsSvc.Delete(ctx, appA, e.owner); err != nil {
		t.Fatalf("delete app: %v", err)
	}

	// The old credential no longer authenticates.
	rc2 := dialRedis(t, port)
	defer rc2.Close()
	if _, err := rc2.cmdErr("AUTH", ua.user, ua.pass); err == nil {
		t.Fatal("A authenticated after app delete, expected ACL user removed")
	}

	// A new app reuses the freed index 0 with a fresh user.
	appB := e.newApp(t, "App B")
	_, attB, err := e.svc.Attach(ctx, instID, appB, e.owner)
	if err != nil {
		t.Fatalf("attach B: %v", err)
	}
	if attB.DbIndex.Int32 != 0 {
		t.Fatalf("reused index = %d, want 0 (freed by app delete)", attB.DbIndex.Int32)
	}
}

// TestAppDeleteDropsPostgresRole asserts app deletion drops the postgres login
// role of its attachment (the database itself is kept, matching Detach).
func TestAppDeleteDropsPostgresRole(t *testing.T) {
	requireDocker(t)
	e := setup(t)
	e.appsSvc.SetAttachmentCleaner(e.svc)
	ctx := context.Background()
	instID := e.provision(t, CreateInput{Name: "orders-db", Engine: "postgres", Version: "16", ExposePort: true})
	inst, _ := e.svc.Get(ctx, instID, e.owner)
	port := inst.Instance.HostPort.Int32

	appA := e.newApp(t, "App A")
	urlA, _, err := e.svc.Attach(ctx, instID, appA, e.owner)
	if err != nil {
		t.Fatalf("attach A: %v", err)
	}
	connA := hostRewrite(t, urlA, port)
	if err := pgCanConnect(ctx, connA); err != nil {
		t.Fatalf("A -> own db before delete: %v", err)
	}
	if err := e.appsSvc.Delete(ctx, appA, e.owner); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	if err := pgCanConnect(ctx, connA); err == nil {
		t.Fatal("A connected after app delete, expected role dropped")
	}
}

// ---- minimal RESP client (no new deps) -----------------------------------

type redisConn struct {
	c   net.Conn
	buf []byte
}

func dialRedis(t *testing.T, port int32) *redisConn {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial redis: %v", err)
	}
	return &redisConn{c: c}
}

func (r *redisConn) Close() { _ = r.c.Close() }

func (r *redisConn) cmd(t *testing.T, args ...string) string {
	t.Helper()
	out, err := r.cmdErr(args...)
	if err != nil {
		t.Fatalf("redis %v: %v", args, err)
	}
	return out
}

func (r *redisConn) cmdErr(args ...string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	_ = r.c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := r.c.Write([]byte(b.String())); err != nil {
		return "", err
	}
	return r.readReply()
}

func (r *redisConn) readByte() (byte, error) {
	for len(r.buf) == 0 {
		tmp := make([]byte, 512)
		n, err := r.c.Read(tmp)
		if n > 0 {
			r.buf = append(r.buf, tmp[:n]...)
			break
		}
		if err != nil {
			return 0, err
		}
	}
	b := r.buf[0]
	r.buf = r.buf[1:]
	return b, nil
}

func (r *redisConn) readLine() (string, error) {
	var out []byte
	for {
		b, err := r.readByte()
		if err != nil {
			return "", err
		}
		if b == '\r' {
			if _, err := r.readByte(); err != nil { // consume \n
				return "", err
			}
			return string(out), nil
		}
		out = append(out, b)
	}
}

func (r *redisConn) readReply() (string, error) {
	prefix, err := r.readByte()
	if err != nil {
		return "", err
	}
	line, err := r.readLine()
	if err != nil {
		return "", err
	}
	switch prefix {
	case '+', ':':
		return line, nil
	case '-':
		return "", errors.New(line)
	case '$':
		if line == "-1" {
			return "", nil
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err != nil {
			return "", err
		}
		data := make([]byte, 0, n)
		for len(data) < n {
			b, err := r.readByte()
			if err != nil {
				return "", err
			}
			data = append(data, b)
		}
		_, _ = r.readByte() // \r
		_, _ = r.readByte() // \n
		return string(data), nil
	default:
		return "", fmt.Errorf("unexpected reply prefix %q", prefix)
	}
}

type redisURL struct {
	user, pass, idx string
}

func mustParseRedis(t *testing.T, raw string) redisURL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	pass, _ := u.User.Password()
	return redisURL{user: u.User.Username(), pass: pass, idx: strings.TrimPrefix(u.Path, "/")}
}
