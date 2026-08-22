# Multi-server 12a — Hosts & Remote Deploys Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apps can be deployed to registered worker hosts over Docker-over-SSH (per-host docker contexts), with full blue/green parity, while every existing single-host app keeps working untouched.

**Architecture:** One `reconciler.Docker` provider class parameterized by a `Target` (per-host isolated `DOCKER_CONFIG` + managed `HOME` so nothing global is touched). Blue/green active-color moves from per-host `state.json` into Postgres. New `internal/hosts` service seals SSH keys with `crypto.Box`; new `/api/v1/admin/hosts` endpoints manage them.

**Tech Stack:** Go, pgx/v5 + sqlc + goose migrations, chi, docker CLI + compose plugin over `DOCKER_CONFIG`-scoped contexts, crypto.Box (AES-256-GCM), Astro SPA.

**Spec:** `docs/superpowers/specs/2026-08-22-cargo-multi-server-design.md` §1–§4, §6 (Verify only — proxy-stack Bootstrap is slice 12c).

## Global Constraints

- Local behavior must be byte-for-byte unchanged: local invocations inherit process env exactly as today (`golden` test asserts it).
- `applications.host_id IS NULL` means control plane; NULL must remain valid everywhere forever.
- SSH private keys are only ever persisted sealed (`crypto.Box`, `key_version` column) and registered in `internal/keyrotate`.
- Host-key verification is TOFU-pinned; a fingerprint mismatch aborts every operation to that host.
- No agent on workers; requirements are SSH access + Docker Engine + compose plugin. If docker is missing, surface a copy-paste install command — never auto-install.
- Migrations are goose-style up/down in one file, next number `00020`. Regenerate sqlc with `sqlc generate` after query changes.
- Testcontainers wait: `wait.ForLog("database system is ready to accept connections").WithOccurrence(2)`.
- Verification commands: `go test ./... -p 2`, `golangci-lint run`, frontend `pnpm check && pnpm build`.

---

### Task 1: Migration 00020 + sqlc queries for hosts

**Files:**
- Create: `internal/db/migrations/00020_multi_server.sql`
- Create: `internal/db/queries/hosts.sql`
- Modify: `internal/apps/queries` additions go in existing `internal/db/queries/apps.sql` (host_id + active_color columns)
- Generated: `internal/db/sqlc/*` (via `sqlc generate`)

**Interfaces:**
- Produces: `hosts` table; sqlc methods `CreateHost`, `ListHosts`, `GetHost`, `GetHostForApp`, `UpdateHostStatus`, `UpdateHostCapacity`, `DeleteHost`, `SetHostKey`; `applications.host_id`, `applications.active_color`, `deployments.host_id` columns; sqlc model `Host`.

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- Worker hosts reachable over SSH (Phase 12a). Applications with
-- host_id IS NULL deploy to the control plane itself, which stays the
-- default. active_color replaces the per-host state.json: the reconciler
-- owns it inside the advisory-locked deploy transaction, so a restored
-- backup can never disagree with what any host is running.
CREATE TABLE hosts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    address text NOT NULL,
    port int NOT NULL DEFAULT 22,
    private_key_enc bytea,
    key_version int NOT NULL DEFAULT 1,
    host_key_fingerprint text,
    status text NOT NULL DEFAULT 'pending',
    engine_version text,
    cpu_count int,
    mem_total_mb bigint,
    apps_domain_suffix text NOT NULL DEFAULT '',
    letsencrypt_email text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE applications ADD COLUMN host_id uuid REFERENCES hosts(id);
ALTER TABLE applications ADD COLUMN active_color text;
ALTER TABLE deployments ADD COLUMN host_id uuid REFERENCES hosts(id);

-- +goose Down
ALTER TABLE deployments DROP COLUMN host_id;
ALTER TABLE applications DROP COLUMN active_color;
ALTER TABLE applications DROP COLUMN host_id;
DROP TABLE hosts;
```

- [ ] **Step 2: Write `internal/db/queries/hosts.sql`**

Follow the style of the existing query files (one named query per block):

```sql
-- name: CreateHost :one
INSERT INTO hosts (name, address, port, private_key_enc, key_version,
                   apps_domain_suffix, letsencrypt_email)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListHosts :many
SELECT * FROM hosts ORDER BY created_at;

-- name: GetHost :one
SELECT * FROM hosts WHERE id = $1;

-- name: UpdateHostStatus :exec
UPDATE hosts SET status = $2, engine_version = $3,
                 cpu_count = $4, mem_total_mb = $5
WHERE id = $1;

-- name: PinHostFingerprint :exec
UPDATE hosts SET host_key_fingerprint = $2 WHERE id = $1;

-- name: SetHostKey :exec
UPDATE hosts SET private_key_enc = $2, key_version = $3 WHERE id = $1;

-- name: DeleteHost :exec
DELETE FROM hosts WHERE id = $1;
```

Add to `internal/db/queries/apps.sql`:

```sql
-- name: SetAppHost :exec
UPDATE applications SET host_id = $2 WHERE id = $1;

-- name: GetActiveColor :one
SELECT active_color FROM applications WHERE id = $1;

-- name: SetActiveColor :exec
UPDATE applications SET active_color = $2 WHERE id = $1;

-- name: CountAppsOnHost :one
SELECT count(*) FROM applications WHERE host_id = $1;
```

And to `internal/db/queries/deployments.sql` (the finish/update used by the pipeline records the host):

```sql
-- name: StartDeploymentWithHost :exec
UPDATE deployments SET host_id = $2, status = 'running', started_at = now()
WHERE id = $1;
```

(Copy the exact shape of the existing `StartDeployment` query and extend it; delete the old one and fix call sites.)

- [ ] **Step 3: Regenerate and verify**

Run: `sqlc generate && go build ./...`
Expected: clean compile; `Host` model present in `internal/db/sqlc/models.go`.

- [ ] **Step 4: Migration round-trip test**

Add to the existing migration test package (find it via `grep -rl "migrate" internal/db --include=*_test.go`): assert up then down then up against a testcontainer Postgres.

Run: `go test ./internal/db/... -run Migration -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db
git commit -m "feat(db): hosts table, per-app host and active-color columns"
```

---

### Task 2: `internal/hosts` service — sealed keys + verify

**Files:**
- Create: `internal/hosts/service.go`
- Create: `internal/hosts/service_test.go`
- Modify: `cmd/server/main.go` (construct and wire later tasks' consumers)

**Interfaces:**
- Consumes: `crypto.Box` (`Seal/Open`), sqlc `*Queries` from Task 1.
- Produces:
  - `type Service struct{...}`; `NewService(pool *pgxpool.Pool, box *crypto.Box) *Service`
  - `(s *Service) Create(ctx, in CreateInput) (sqlc.Host, error)` where `CreateInput{Name, Address string; Port int; KeyPEM []byte; DomainSuffix, LetsEncryptEmail string}`
  - `(s *Service) OpenKey(ctx context.Context, h sqlc.Host) (keyPEM []byte, err error)`
  - `(s *Service) Delete(ctx context.Context, id string) error` — refused while `CountAppsOnHost(id) > 0`

- [ ] **Step 1: Write failing tests**

```go
func TestCreateSealsKeyAndOpenRoundTrips(t *testing.T) {
	svc := newTestService(t) // testcontainer postgres + crypto.New(bytes.Repeat([]byte{1},32))
	h, err := svc.Create(ctx, CreateInput{
		Name: "w1", Address: "deploy@10.0.0.5", Port: 22,
		KeyPEM: testEd25519PEM(t), DomainSuffix: "w1.example.com",
		LetsEncryptEmail: "ops@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, "pending", h.Status)
	// Sealed at rest: raw column must not contain the PEM.
	var raw []byte
	require.NoError(t, svc.pool.QueryRow(ctx,
		`SELECT private_key_enc FROM hosts WHERE id = $1`, h.ID).Scan(&raw))
	require.NotContains(t, string(raw), "PRIVATE KEY")

	got, err := svc.OpenKey(ctx, h)
	require.NoError(t, err)
	require.Equal(t, testEd25519PEM(t), got)
}

func TestDeleteRefusedWhileAppsAssigned(t *testing.T) { /* seed host + application row pointing at it; expect ErrHostInUse */ }
func TestOpenKeyWrongKeyFails(t *testing.T)           /* reopen with a different Box; expect error */ }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hosts/ -v`
Expected: FAIL (package doesn't exist)

- [ ] **Step 3: Implement the service**

```go
// Package hosts manages worker-host lifecycle: sealed credentials,
// connectivity verification, and capacity reporting.
package hosts

var ErrHostInUse = errors.New("host still has applications assigned")

type CreateInput struct {
	Name, Address            string
	Port                     int
	KeyPEM                   []byte
	DomainSuffix, LetsEncryptEmail string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (sqlc.Host, error) {
	sealed := s.box.Seal(in.KeyPEM)
	return s.q.CreateHost(ctx, sqlc.CreateHostParams{
		Name: in.Name, Address: in.Address, Port: int32(in.Port),
		PrivateKeyEnc: sealed, KeyVersion: s.keyVersion(),
		AppsDomainSuffix: in.DomainSuffix, LetsencryptEmail: in.LetsEncryptEmail,
	})
}
```

Validate PEM parses before storing: `pem.Decode` succeeds and `ssh.ParsePrivateKey` accepts it (import `golang.org/x/crypto/ssh` — already an indirect dep; promote to direct in `go.mod`). `keyVersion()` mirrors how `internal/apps/service.go` stamps `key_version`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/hosts/ -v`
Expected: PASS

- [ ] **Step 5: Register in key rotation**

Modify `internal/keyrotate/rotate.go`: add a branch alongside `applications.registry_creds_enc` that re-seals `hosts.private_key_enc` (same decrypt-compare-replace pattern). Add a real-DB rotation test mirroring the existing per-shape tests.

Run: `go test ./internal/keyrotate/ -v && golangci-lint run ./internal/hosts/... ./internal/keyrotate/...`
Expected: PASS, 0 issues

- [ ] **Step 6: Commit**

```bash
git add internal/hosts internal/keyrotate go.mod go.sum
git commit -m "feat(hosts): worker-host service with sealed SSH keys"
```

---

### Task 3: Host environment materialization (context + HOME)

**Files:**
- Create: `internal/hostmgr/env.go`
- Create: `internal/hostmgr/env_test.go`

**Interfaces:**
- Consumes: `hosts.Service.OpenKey`.
- Produces:
  - `type Target struct { HostID, Endpoint string; Env []string }` (lives here, consumed by reconciler in Task 4)
  - `(m *Materializer) Target(ctx context.Context, h sqlc.Host) (reconciler.Target, error)`
  - `NewMaterializer(dataDir string, keys KeyOpener)` where `type KeyOpener interface{ OpenKey(ctx, h) ([]byte, error) }`

- [ ] **Step 1: Write failing tests**

```go
func TestTargetMaterializesIsolatedDirs(t *testing.T) {
	m := NewMaterializer(t.TempDir(), stubKeys{})
	tg, err := m.Target(ctx, hostFixture()) // hostFixture: address deploy@10.0.0.5, port 2222
	require.NoError(t, err)

	require.Equal(t, "ssh://deploy@10.0.0.5:2222", tg.Endpoint)
	env := mapFromEnv(tg.Env)
	require.Equal(t, filepath.Join(m.dataDir, "hosts", hostID, "docker"), env["DOCKER_CONFIG"])
	require.Equal(t, filepath.Join(m.dataDir, "hosts", hostID, "home"), env["HOME"])
	require.Equal(t, "cargo-worker", env["DOCKER_CONTEXT"])

	home := env["HOME"]
	key, err := os.ReadFile(filepath.Join(home, ".ssh", "id_ed25519"))
	require.NoError(t, err); require.Equal(t, testPEM, string(key))
	st, _ := os.Stat(filepath.Join(home, ".ssh", "id_ed25519"))
	require.Equal(t, os.FileMode(0o600), st.Perm().Perm())
	cfg, _ := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	require.Contains(t, string(cfg), "StrictHostKeyChecking yes")
	kh, _ := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts"))
	require.NotEmpty(t, kh) // pinned entry present after pinning
	require.NoDirEntry(t, ...) // no leak outside hosts/<id>/
}

func TestFingerprintMismatchAborts(t *testing.T) {
	// fixture has pinned fingerprint X; ssh-keyscan-equivalent returns Y -> error
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hostmgr/ -v`
Expected: FAIL

- [ ] **Step 3: Implement**

```go
// Target describes one worker host's fully-materialized execution
// environment: a per-host DOCKER_CONFIG holding a docker context and a
// managed HOME whose .ssh pins the host key. Nothing global (~/.docker,
// ~/.ssh) is ever touched, so concurrent deploys to different hosts
// cannot race.
type Target struct {
	HostID   string
	Endpoint string // ssh://user@host:port
	Env      []string
}
```

Implementation notes (each is a step):
1. Dirs `<dataDir>/hosts/<id>/{docker,home/.ssh}` with `0o700`.
2. Write key PEM 0600, `.ssh/config` containing `StrictHostKeyChecking yes\nIdentitiesOnly yes\nIdentityFile ~/.ssh/id_ed25519`.
3. Fingerprint: run `ssh-keyscan -p <port> <host>` with `HOME` set to the managed home and parse the `SHA256:<...>` fingerprint. If the DB row's `host_key_fingerprint` is empty → TOFU: write known_hosts entry and `PinHostFingerprint`. If set and different → return `ErrHostKeyMismatch`.
4. Context creation: shell out once (idempotent) —
   `env DOCKER_CONFIG=<dir> HOME=<home> docker context create cargo-worker --docker host=ssh://user@host:p`
   ignoring "already exists"; then `Env = ["DOCKER_CONFIG="+dir, "HOME="+home, "DOCKER_CONTEXT=cargo-worker"]`.
5. Cache materialized targets in-memory keyed by host ID + updated-at to avoid re-running ssh-keyscan on every op; invalidate on host update.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/hostmgr/ -v && golangci-lint run ./internal/hostmgr/...`
Expected: PASS, 0 issues

- [ ] **Step 5: Commit**

```bash
git add internal/hostmgr
git commit -m "feat(hostmgr): per-host docker-context and ssh-home materialization"
```

---

### Task 4: Provider targets — `Docker.ForHost` + `Spec.HostID`

**Files:**
- Modify: `internal/reconciler/spec.go` (Spec.HostID)
- Modify: `internal/reconciler/provider.go` (target-aware exec)
- Create: `internal/reconciler/target_test.go`

**Interfaces:**
- Consumes: `reconciler.Target` from Task 3 (re-declared here if import cycle appears — reconciler must not import hostmgr; if so, move `Target` into `internal/reconciler` and have hostmgr produce it).
- Produces: `(d *Docker) ForHost(t Target) *Docker`; `Spec.HostID string`.

- [ ] **Step 1: Golden test proving local path unchanged**

```go
func TestLocalCommandsInheritProcessEnv(t *testing.T) {
	d := NewDocker(t.TempDir())
	cmd := d.buildCmd(context.Background(), "docker", "ps") // extract helper under test
	require.Nil(t, cmd.Env)                                  // nil = inherit, exactly as today
}

func TestForHostCommandsCarryTargetEnv(t *testing.T) {
	d := NewDocker(t.TempDir()).ForHost(Target{HostID: "h1",
		Endpoint: "ssh://u@h:22",
		Env:      []string{"DOCKER_CONFIG=/x/docker", "HOME=/x/home", "DOCKER_CONTEXT=cargo-worker"}})
	cmd := d.buildCmd(context.Background(), "docker", "ps")
	got := mapFromEnv(cmd.Env)
	require.Equal(t, "/x/docker", got["DOCKER_CONFIG"])
	require.Equal(t, "cargo-worker", got["DOCKER_CONTEXT"])
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/reconciler/ -run TestLoc|TestForHost -v`
Expected: FAIL

- [ ] **Step 3: Implement**

Add to `Docker`: `target *Target`. Change the four exec funnels (`run`, `runCompose`, `outputCompose`, `output`) from package functions to methods (or pass `d` through) so each sets `cmd.Env`:

```go
func (d *Docker) setEnv(cmd *exec.Cmd) {
	if d.target == nil {
		return // inherit process env — unchanged local behaviour
	}
	cmd.Env = append(os.Environ(), d.target.Env...)
}
```

`ForHost` returns a shallow copy sharing `dataDir`/`HealthTimeout` but bound to the target. All existing call sites compile unchanged because they run on the zero-target receiver. Add `Spec.HostID` (documentation-only for the provider; routing happens in Task 6).

- [ ] **Step 4: Full reconciler suite green**

Run: `go test ./internal/reconciler/... -p 2`
Expected: PASS (proves no regression in local behavior)

- [ ] **Step 5: Commit**

```bash
git add internal/reconciler
git commit -m "feat(reconciler): target-bound provider via per-host contexts"
```

---

### Task 5: Active color moves to Postgres

**Files:**
- Modify: `internal/reconciler/provider.go` (`activeColor`/`setActiveColor`)
- Create: `internal/reconciler/colors.go`
- Modify: `internal/jobs/deploy.go` (wire DB-backed store)
- Test: `internal/reconciler/colors_test.go`, adjust existing blue/green tests

**Interfaces:**
- Produces:
  ```go
  type ColorStore interface {
      ActiveColor(ctx context.Context, appID string) (string, error)
      SetActiveColor(ctx context.Context, appID, color string) error
  }
  ```
  `Docker.colors ColorStore` — nil falls back to the legacy `state.json` file (keeps standalone reconciler tests working); main.go wires a DB adapter backed by `GetActiveColor`/`SetActiveColor`.

- [ ] **Step 1: Write failing tests**

```go
func TestDBBackedColorStore(t *testing.T) {
	// real-db test: SetActiveColor("blue") then ActiveColor -> "blue";
	// unknown app -> "" with no error (matches legacy file semantics).
}
func TestApplyBlueGreenUsesInjectedStore(t *testing.T) {
	// stub ColorStore recording writes; run applyBlueGreen twice against a
	// fake-provider harness (reuse the existing blue/green test scaffolding);
	// assert colors sequence "" -> "blue" -> "green" came from the stub,
	// and state.json was never written.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/reconciler/ -run Color -v`
Expected: FAIL

- [ ] **Step 3: Implement**

`activeColor(appID)` becomes `activeColor(ctx, appID)` consulting `d.colors` first, falling back to reading `state.json` when `d.colors == nil`. Same split for `setActiveColor` (when `d.colors != nil`, also remove any stale `state.json`). Thread `ctx` through `applyBlueGreen`/`applyRecreate`/legacy-retirement reads. Wire the adapter in `cmd/server/main.go`:

```go
provider := reconciler.NewDocker(cfg.DataDir)
provider.Colors = dbColorStore{q: queries} // implements reconciler.ColorStore
```

- [ ] **Step 4: Blue/green suite green (real-docker tests included)**

Run: `go test ./internal/reconciler/... ./internal/jobs/... -p 2`
Expected: PASS — including the zero-outage hand-off test

- [ ] **Step 5: Commit**

```bash
git add internal/reconciler internal/jobs cmd/server
git commit -m "refactor(reconciler): active color owned by Postgres, not state.json"
```

---

### Task 6: Remote-safe health gate

**Files:**
- Modify: `internal/reconciler/provider.go` (`waitHealthy`)
- Test: extend the existing health-gate tests

**Interfaces:**
- Consumes: target-bound `outputCompose` from Task 4.
- Produces: identical external semantics; remote targets never dial bridge IPs.

- [ ] **Step 1: Write failing test**

```go
func TestRemoteGateNeverProbesHTTP(t *testing.T) {
	// target-bound Docker with an httptest server that would 500 if dialed;
	// app with HealthcheckPath set; container reported running by compose ps.
	// Expect: gate passes on stability window alone, HTTP probe never dialed.
}
func TestRemoteGateFailsOnCrashLoop(t *testing.T) { /* compose ps reports restarting -> gate fails fast */ }
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/reconciler/ -run RemoteGate -v`
Expected: FAIL

- [ ] **Step 3: Implement**

In `waitHealthy`, when `d.target != nil`: skip the `http.Client` probe entirely; poll `compose ps -q` + `docker inspect` (through the same context) for running/restarting state and apply the same `stableFor` window and `HealthTimeout` deadline. Keep the local path exactly as-is (including the bridge-IP probe and its unreachable degradation).

- [ ] **Step 4: Suite green**

Run: `go test ./internal/reconciler/... -p 2`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reconciler
git commit -m "feat(reconciler): daemon-side health gate for remote targets"
```

---

### Task 7: hostmgr Verify + API endpoints

**Files:**
- Create: `internal/hostmgr/verify.go`, `internal/hostmgr/verify_test.go`
- Create: `internal/api/admin_hosts.go`, `internal/api/admin_hosts_test.go`
- Modify: `internal/api/router.go` (routes), `internal/api/server.go` (fields)

**Interfaces:**
- Consumes: `hosts.Service` (Task 2), `Materializer` (Task 3).
- Produces REST surface (instance-admin only, mirroring `/admin/*` conventions):
  - `POST /api/v1/admin/hosts` `{name,address,port,key_pem,apps_domain_suffix,letsencrypt_email}` → creates + verifies synchronously, returns host with `status`
  - `GET /api/v1/admin/hosts` → list (never includes `private_key_enc` or PEM)
  - `DELETE /api/v1/admin/hosts/{hostID}` → 409 `ErrHostInUse` when apps assigned
  - `PUT /api/v1/admin/hosts/{hostID}/key` `{key_pem}` → re-seal + re-verify
  - `GET /api/v1/orgs/{orgID}/hosts` → org-scoped read-only list `{id,name,status}` (for the app host selector)

- [ ] **Step 1: Write failing API tests** (follow `admin_test.go` harness patterns: authed instance-admin cookie vs org user forbidden)

```go
func TestCreateHostVerifiesAgainstFakeSSHD(t *testing.T)          { /* 201, status online, capacity filled */ }
func TestCreateHostWithoutDockerSurfacesInstallCommand(t *testing.T) { /* status degraded, response carries hint command */ }
func TestHostListNeverReturnsPrivateKey(t *testing.T)             { /* JSON body contains neither "key_pem" nor PEM text */ }
func TestOrgUserCannotManageHosts(t *testing.T)                   { /* 403 on POST/DELETE */ }
func TestDeleteHostWithAppsConflicts(t *testing.T)                { /* 409 */ }
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run Host -v`
Expected: FAIL

- [ ] **Step 3: Implement Verify**

```go
// Verify connects to the host through its materialized Target and records
// liveness + capacity. A missing docker/compose plugin degrades to
// 'degraded' plus an operator-facing install hint; it is never auto-installed.
func (m *Manager) Verify(ctx context.Context, h sqlc.Host) (VerifyResult, error)
```

Runs, through the Target env: `docker version --format json` (Engine version), `docker compose version` (plugin presence), `nproc`, `free -m` (mem_total). Updates `UpdateHostStatus`; pins fingerprint via the materializer on first contact.

- [ ] **Step 4: Implement handlers + routes**

Handlers follow the existing admin handler shape (decode → validate → service call → JSON). Router additions under the instance-admin group (copy the grouping used by `/admin/backups`):

```go
r.Route("/admin/hosts", func(r chi.Router) {
	r.Post("/", s.handleCreateHost)
	r.Get("/", s.handleListHosts)
	r.Route("/{hostID}", func(r chi.Router) {
		r.Delete("/", s.handleDeleteHost)
		r.Put("/key", s.handleReplaceHostKey)
	})
})
r.Get("/orgs/{orgID}/hosts", s.handleListOrgHosts) // inside requireAuth group
```

Wire `Manager` onto `Server` in `cmd/server/main.go`.

- [ ] **Step 5: Tests green, lint clean**

Run: `go test ./internal/api/ ./internal/hostmgr/ -p 2 && golangci-lint run`
Expected: PASS, 0 issues

- [ ] **Step 6: Commit**

```bash
git add internal/api internal/hostmgr cmd/server
git commit -m "feat(api): worker-host management endpoints"
```

---

### Task 8: Pipeline routes deploys to the app's host

**Files:**
- Modify: `internal/jobs/deploy.go` (resolve host, record `deployments.host_id`, fail-fast)
- Modify: `cmd/server/main.go` (inject hosts deps into pipeline)
- Test: `internal/jobs/deploy_test.go` additions

**Interfaces:**
- Consumes: everything above. Pipeline gains:
  ```go
  type HostResolver interface {
      Resolve(ctx context.Context, appID string) (reconciler.Target, error) // nil,nil = local
  }
  ```
  `Pipeline.ForTarget(t reconciler.Target)` returns the provider view for one deploy.

- [ ] **Step 1: Write failing tests**

```go
func TestDeployToLocalUnchanged(t *testing.T) { /* hostless app: provider receives zero-target Docker */ }
func TestDeployToRemoteUsesTargetProvider(t *testing.T) {
	// app with host_id; stub resolver returns sentinel Target; assert Apply ran
	// through ForHost(sentinel) and deployment row carries host_id.
}
func TestDeployFailFastWhenHostUnreachable(t *testing.T) {
	// resolver returns ErrUnreachable; deployment -> failed with SSH error in log; host marked unreachable
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/jobs/ -run Deploy -v`
Expected: FAIL

- [ ] **Step 3: Implement**

Early in `Pipeline.Run` (after fetching the deployment, inside the advisory-lock window): resolve the app's host; on `ErrUnreachable` mark failed immediately. Record `deployments.host_id` via `StartDeploymentWithHost`. Build the provider view: `p := d.Provider; if t != nil { p = p.(*reconciler.Docker).ForHost(*t) }` — if the interface assertion smells, promote `ForHost` onto a small `interface{ ForHost(reconciler.Target) reconciler.DeployProvider }` check next to `Pipeline`.

- [ ] **Step 4: App host assignment API**

Modify `internal/api/apps.go` `handleCreateApp`/`handleUpdateApp`: accept `host_id` (validated: host exists AND `status IN ('online','degraded')`; org members may select any listed host). Org-scope the selector via Task 7's org hosts endpoint.

- [ ] **Step 5: Integration test over real SSH**

Reuse the existing real-Docker test pattern but expose the local dockerd socket through an sshd testcontainer: mount `/var/run/docker.sock` into the container, run sshd, register it as a host, deploy the demo app end-to-end, assert the container runs and serves through the remote project labels, teardown removes everything.

Run: `go test ./internal/jobs/ -run SSH -v -timeout 20m`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/jobs internal/api cmd/server
git commit -m "feat(deploy): route pipelines to worker hosts over SSH"
```

---

### Task 9: Frontend — Workers admin page + app host selector

**Files:**
- Create: `src/pages/admin/workers.astro` (+ components per existing admin page patterns)
- Modify: app create/settings pages (host selector fed by `GET /orgs/{orgID}/hosts`)
- Test: existing vitest component suite

**Interfaces:**
- Consumes: Task 7 REST surface.

- [ ] **Step 1: Workers page** — table (name, address, status badge, engine version, CPU/mem, apps count), add-host form (name/address/port/private key textarea/domain suffix/LE email), delete with confirm, replace-key dialog. Mirror styling and data-fetch patterns from the existing Admin pages.
- [ ] **Step 2: App form host selector** — dropdown defaulting to "Control plane"; shows host status inline; disabled options for unreachable hosts.
- [ ] **Step 3: Vitest coverage** — selector renders org hosts and defaults correctly; workers page renders statuses; forms hit the documented endpoints (mock fetch, per existing tests).
- [ ] **Step 4: Verify**

Run: `pnpm vitest run && pnpm check && pnpm build` (in `cargo-web/`)
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src
git commit -m "feat(ui): worker hosts admin page and app host selector"
```

---

### Task 10: Close 12a — logs/stats routing, docs, version

**Files:**
- Modify: `internal/api/applogs.go` + metrics handlers (route `AppLogs`/`AppStats` through the app's host target)
- Modify: `README.md` (multi-server section: requirements, adding a worker, DNS guidance)
- Modify: `PhasedPlanv2.md` (mark 12a progress)
- Modify: version bump wherever 1.x lives (grep `1.3.0`)

- [ ] **Step 1: Logs/stats follow the host** — handlers currently reach `*reconciler.Docker` directly; resolve the app's host target (same `HostResolver`) and call `ForHost(...).AppLogs/AppStats`. Local apps unchanged. Test: handler test asserting the routed target for a hosted app.
- [ ] **Step 2: Docs** — README section + `docs/` note that per-host Traefik bootstrap arrives in 12c (remote apps serve traffic only after the operator bootstraps a proxy stack or points DNS accordingly until then).
- [ ] **Step 3: Version bump + changelog line.**
- [ ] **Step 4: Full verification**

Run: `go test ./... -p 2 && golangci-lint run && cd ../cargo-web && pnpm vitest run && pnpm check && pnpm build && docker build -t cargo-check .` (from `cargo/`)
Expected: all green

- [ ] **Step 5: Phase exit demo (manual, documented)**

Register a VM as a worker → deploy an app targeting it → app runs on the worker (verified via `docker ps` there) → redeploy exercises blue/green over SSH with zero downtime → kill sshd on the worker → deploy fails cleanly, host shows `unreachable`, other apps unaffected.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: close multi-server 12a — logs/stats follow host, docs, bump"
```
