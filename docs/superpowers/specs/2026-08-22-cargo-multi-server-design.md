# Cargo — Multi-server (Docker-over-SSH) design

Phase 12 (9.1) of [PhasedPlanv2.md](../../PhasedPlanv2.md). Spec date: 2026-08-22.

Cargo gains worker hosts: plain Docker machines reachable over SSH that run tenant
apps under their own Traefik, driven entirely by the control plane. No agent is
installed on workers; the only requirements are SSH access and Docker Engine with
the compose plugin.

Decisions locked during brainstorming:

| Decision | Choice |
|---|---|
| Scope | Sliced milestone: 12a hosts + remote deploy, 12b metrics/logs follow host, 12c bootstrap + docs. Managed databases stay single-host this cycle. |
| Traffic topology | Per-host Traefik + `cargo-proxy` + `cargo-system` on every worker; app DNS points at the app's host IP; no cross-host networking |
| SSH auth | Per-host private key stored sealed in Postgres (`crypto.Box`); host key pinned on first connect (TOFU), verified thereafter |
| Blue/green | Full parity day one; active-color state moves from per-host `state.json` into Postgres |
| Join flow | Cargo-driven over SSH (verify docker, render + push proxy stack) |
| Transport | Docker contexts — but scoped to a **per-host config dir**, not the global `~/.docker` |

## 1. Topology

- Control plane unchanged: Postgres, API, River workers, its own Traefik on
  `cargo-proxy`.
- Each worker runs the same proxy stack shape as the control-plane host: Traefik,
  `cargo-proxy` network, `cargo-system` network, ACME via the worker's own domain
  suffix + Let's Encrypt email. Rendered from the existing `deploy/` compose files,
  parameterized.
- An app's domain resolves to its host's IP. There is deliberately zero cross-host
  networking; moving an app between hosts is a redeploy with a different `host_id`
  plus a DNS change (out of scope for v1 UI).

## 2. Transport: contexts without shared state

One provider class, parameterized by target:

```go
provider := reconciler.NewDocker(dataDir) // local, exactly as today
hp := provider.ForHost(hostRecord)        // same struct bound to a Target
```

All exec funnels through the existing `run` / `runCompose` / `output` helpers
(`internal/reconciler/provider.go`). When targeting a host, each command gets:

- `DOCKER_CONFIG=<dataDir>/hosts/<hostID>/docker` — per-host docker config dir
  containing the context definition (`ssh://user@ip:p`). Nothing global is touched;
  concurrent deploys to different hosts cannot race.
- `HOME=<dataDir>/hosts/<hostID>/home` — managed home dir with:
  - `.ssh/id_ed25519` (0600), decrypted from `private_key_enc` at materialization
  - `.ssh/config` (StrictHostKeyChecking yes, pinned known_hosts)
  - `.ssh/known_hosts` — host key fingerprint pinned at first Verify; mismatch
    aborts every subsequent operation (MITM guard)

Materialization happens when a host is added and re-derived at control-plane boot
(sealed DB row is the source of truth). Local invocations keep inheriting the
process env unchanged; golden tests assert the local command stream is identical
to today's.

## 3. Data model

Migration `00020_multi_server.sql`:

```sql
CREATE TABLE hosts (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  address text NOT NULL,            -- user@host
  port int NOT NULL DEFAULT 22,
  private_key_enc bytea,            -- crypto.Box-sealed PEM
  key_version int NOT NULL DEFAULT 1,
  host_key_fingerprint text,        -- TOFU-pinned SHA-256 fingerprint
  status text NOT NULL DEFAULT 'pending',   -- pending|online|degraded|unreachable
  engine_version text,
  cpu_count int,
  mem_total_mb bigint,
  apps_domain_suffix text NOT NULL, -- e.g. worker1.example.com
  letsencrypt_email text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE applications ADD COLUMN host_id uuid REFERENCES hosts(id);
ALTER TABLE deployments  ADD COLUMN host_id uuid;
ALTER TABLE applications ADD COLUMN active_color text;
```

- `applications.host_id IS NULL` means the control plane itself — every existing
  app keeps working untouched.
- Hosts are instance-admin resources (like instance settings). Org users see the
  host list read-only when choosing a target for their app.
- Key rotation (`internal/keyrotate`) registers `hosts.private_key_enc`.

### Active color moves to Postgres

Blue/green color state was `<dataDir>/apps/<id>/state.json`, described in code as
"what is actually running on this host". It becomes `applications.active_color`,
written by the reconciler inside the advisory-locked deploy transaction. This is
strictly stronger than the file: a restored backup can never disagree with any
host. The legacy-project retirement step reads it the same way it read the file.

## 4. Deploy flow

Unchanged pipeline: clone → build → build `Spec{HostID}` → `Provider.Apply`.

Clone and build semantics are preserved by running them through the remote daemon
itself (compose `build:` executes against the context's engine), matching local
behavior; v1 introduces no image-transfer layer.

Health gate rework: today it probes container bridge IPs from the control plane —
unreachable cross-host. It instead polls the target daemon (`docker inspect`
health/compose state through the same SSH context) until healthy or timeout, the
same signal Traefik's LB healthcheck consumes. Timeouts and failure semantics are
preserved; the "no healthcheck path" caveat behaves identically.

Teardown / stop / start / prune follow the app's host the same way. Image-prune
color-awareness operates per host daemon, so no change beyond routing.

## 5. Observability (12b)

- `AppStats` / `AppLogs` already route through provider methods; they gain
  `ForHost` routing for free.
- `HostCollector` is already shaped one-instance-per-host (prev-CPU delta state):
  instantiate one per online host, keyed by `host_id`. Remote `/proc/stat`,
  `/proc/meminfo` and statfs samples read over SSH exec; Docker container count
  via the same context. Each source degrades independently as today.
- Traffic metrics scrape each host's Traefik (`http://<host>:8082/metrics`);
  per-host series keyed by `host_id`. Existing 48h retention applies.
- API: existing endpoints gain host scoping; the all-apps admin view shows each
  app's host badge.

## 6. Host lifecycle & join flow (12a/12c)

`internal/hostmgr` owns:

- `Verify(ctx, h)` — connect over SSH; require Engine + compose plugin; record
  version, CPU count, memory; pin host-key fingerprint on first connect; set
  status `online`.
- If docker is missing, return the exact copy-paste install command for the
  operator instead of auto-installing (keeps the worker side boring and auditable).
- `Bootstrap(ctx, h)` — render the per-host proxy compose (domain suffix, acme
  email), push files over SSH (scp-style exec), `docker compose up -d`;
  idempotent/converging.
- Health loop — periodic lightweight probe (`docker version` over SSH) updating
  `online | degraded | unreachable`; unreachable hosts surface in the admin UI and
  fail fast in the deploy pipeline with the SSH error in deploy logs.

UI: Admin "Workers" page (add/edit/remove host, live status, capacity, bootstrap
status); app create/settings gain a host selector defaulting to the control plane.

## 7. Error handling

| Failure | Behavior |
|---|---|
| Host unreachable at deploy start | Deployment `failed`, SSH error in log; host marked `unreachable` |
| Connection drops mid-deploy | Same as above; next deploy converges (compose idempotent; blue/green prune is color-aware) |
| Host key mismatch | Hard abort of every operation to that host; status `degraded` + admin alert |
| Bootstrap failure | Retriable; proxy stack converge-on-replay |
| Sealed key unreadable (key lost) | Host marked `degraded`, admin prompted to re-enter key |

## 8. Testing

- Unit: env/dir materialization, context rendering, fingerprint pinning/mismatch,
  `active_color` transaction behavior.
- Integration: real sshd testcontainer exposing the local dockerd as the "remote"
  engine — full deploy → serve → blue/green hand-off → teardown over a genuine
  SSH transport.
- Real-Docker suite reuse: existing tests pointed at the SSH context.
- Golden tests: local-target commands byte-identical to today's output.
- Phase exit demo: add worker → deploy app to it → domain serves via the worker's
  Traefik with valid certs → redeploy with zero downtime → sever the worker →
  deployment fails cleanly, host shows unreachable, other hosts unaffected →
  restore worker.
