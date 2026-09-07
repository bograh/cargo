# Deploying Cargo

## Prerequisites (FR-8.2)

- A Linux host with Docker Engine + the compose plugin
- Ports 80 and 443 open (or an existing reverse proxy you'll put in front — see below)
- For a production (domain) install, DNS records pointing at the host:
  - `cargo.yourdomain.com` → server IP (platform UI)
  - `*.apps.yourdomain.com` → server IP (wildcard for app subdomains)

No domain? Skip the DNS records — a local install on localhost / the
server's IP works out of the box (see below).

## Install

```bash
curl -fsSL https://usecargo.vercel.app/install.sh | sh
```

The bootstrap supports Ubuntu/Debian hosts, installs Docker Engine and Compose
when needed, clones Cargo to `/opt/cargo`, and then runs this directory's
installer. To install from an existing checkout instead, run `./install.sh`
from this directory.

- **Local install (no domain):** leave the platform domain empty (or enter
  `localhost` / an IP). The stack serves the platform on plain HTTP (and
  HTTPS with a self-signed certificate, browser warning expected) at
  `http://localhost` / `http://<server-ip>`. No DNS, email, or certificates
  needed. Apps are served at `https://<name>.apps.localhost` with the
  self-signed certificate.
- **Production install (domain):** enter your platform domain, apps-domain
  suffix, and Let's Encrypt email when prompted.

Manual alternative: create `.env` yourself (keys documented in
docker-compose.yml), `chmod 600 .env`, create the shared proxy network
(`docker network create cargo-proxy`), then `docker compose up -d` (add the
appropriate TLS overlay for production, see below).

First registered account becomes the instance admin.

> **Invite-only by default.** After the first account, everyone else needs
> an invite link from an organization admin. Change it under **Admin →
> Instance settings → Who can sign up** (`Anyone` / `Invited people only` /
> `Nobody`). Instances upgrading from an earlier version become invite-only
> on restart — set it back to `Anyone` if you relied on open sign-up.

### SSL modes (FR-5.3)

- **Local (default, base file only):** no ACME. Traefik serves its
  self-signed default certificate on 443; the platform is also reachable
  on plain HTTP port 80.
- **HTTP-01:** run with the TLS overlay —
  `docker compose -f docker-compose.yml -f docker-compose.tls.yml up -d` —
  with `CARGO_ACME_EMAIL` set. Each auto subdomain and custom domain gets
  its own certificate on first request. Subject to Let's Encrypt rate
  limits with many apps.
- **Wildcard DNS-01 (recommended for production):** run with the DNS-01
  overlay —
  `docker compose -f docker-compose.yml -f docker-compose.dns01.yml up -d` —
  and set `CARGO_DNS_PROVIDER` (a [Traefik DNS provider name](https://doc.traefik.io/traefik/https/acme/#providers),
  e.g. `cloudflare`) plus the provider's credential env vars in `.env`
  (e.g. `CF_DNS_API_TOKEN`). One certificate covers `*.apps.yourdomain.com`.

### Running behind an existing reverse proxy (nginx / Traefik / Apache)

If the host already runs a reverse proxy on 80/443, Cargo's bundled Traefik
must not fight it for those ports. The installer detects busy ports and offers
**external-proxy mode**: Cargo's Traefik then binds loopback-only ports
(`127.0.0.1:8080` HTTP, `127.0.0.1:8443` HTTPS — tunable with
`CARGO_EXTERNAL_HTTP_PORT` / `CARGO_EXTERNAL_HTTPS_PORT`), and your existing
proxy forwards traffic for `cargo.example.com` and `*.apps.example.com` to it.
You can also opt in non-interactively with `CARGO_PROXY_MODE=external`.

Ready-made forwarding configs live in [`examples/`](examples/):
[`nginx-cargo.conf`](examples/nginx-cargo.conf) and
[`traefik-cargo-dynamic.yml`](examples/traefik-cargo-dynamic.yml).
TLS passthrough on 443 is recommended so Cargo keeps managing its own Let's
Encrypt certificates; terminating TLS at your own proxy works too.

Manual equivalent: set in `.env`

```bash
CARGO_HTTP_PUBLISH=127.0.0.1:8080
CARGO_HTTPS_PUBLISH=127.0.0.1:8443
```

Custom domains always use HTTP-01 once their DNS points at the server.

### Resource ceilings

Per-app memory/CPU/PID limits are advisory defaults. An instance admin can
cap how far a tenant raises their own limits via environment variables:

| Variable | Purpose |
|---|---|
| `CARGO_MAX_MEM_LIMIT` | Absolute ceiling for per-app `mem_limit` |
| `CARGO_MAX_CPU_LIMIT` | Absolute ceiling for per-app `cpus` |
| `CARGO_MAX_PIDS_LIMIT` | Absolute ceiling for per-app `pids_limit` |

### Git-clone host screening

Repository URLs are validated against SSRF. Only `https://`, `ssh://`, and
`git@host:path` transports are accepted; hosts that resolve to loopback,
RFC1918, link-local, or cloud-metadata addresses are refused. If your git
server is self-hosted on a private network, set:

```bash
CARGO_ALLOW_PRIVATE_GIT_HOSTS=true
```

This widens *where* clones may go, never *which transports*.

### Network topology & database isolation

The stack uses three Docker networks so a deployed user app can never reach
the control-plane database:

- **`cargo-proxy`** (external, shared) — Traefik, the controlplane, and every
  tenant app container. This is the routing/traffic network.
- **`cargo-system`** (compose-managed, `internal`) — a private link carrying
  only controlplane↔platform-DB traffic. The `db` service joins **only** this
  network, so it has no address on `cargo-proxy`; `internal: true` also denies
  it any outbound route. Tenant apps are never attached here.
- **`cargo-data`** (external) — managed-database instances; an app joins it
  only when it has a database attachment.

Verify the isolation on a running stack (the DB must be reachable from the
controlplane but not from a tenant app):

```bash
# controlplane -> DB : succeeds
docker compose exec controlplane sh -c 'nc -z -w3 db 5432 && echo reachable'
# a container on cargo-proxy (like any app) -> DB : fails to resolve/connect
docker run --rm --network cargo-proxy alpine sh -c 'nc -z -w3 db 5432 || echo isolated'
```

### Upgrades (NFR-8)

```bash
docker compose pull && docker compose up -d   # add your TLS overlay's -f flag for production installs
```

Migrations run automatically at startup; running user apps are not touched.

## Backups & disaster recovery

Cargo backs up its **own** control-plane state (all users, orgs, apps, and the
encrypted env vars / GitHub / OIDC / SMTP secrets live in the platform Postgres —
distinct from managed-database snapshots).

- **Automatic:** a daily job writes to `<dataDir>/platform-backups/`:
  - `<timestamp>.dump` — `pg_dump -Fc` of the control database (run via
    `docker exec` into the DB container, since the app image ships no `pg_dump`),
  - `<timestamp>.certs/` — a copy of Traefik's ACME certificates (`acme.json`,
    and `acme-dns.json` in wildcard mode),
  - `<timestamp>.keyfp` — a SHA-256 fingerprint of the master key (never the key
    itself), so you can confirm which key a backup set belongs to.
  The last `CARGO_PLATFORM_BACKUP_KEEP` sets (default 14) are retained.
- **On demand:** Admin → Backups → **Run backup now**.

### ⚠️ Master key custody

The database dump is encrypted-at-rest data plus AES-GCM-sealed secrets. **You
cannot recover env vars, registry credentials, or integration secrets without the
`CARGO_MASTER_KEY`.** Cargo never writes the key into a backup. Store it in a
password manager / secrets vault the moment `install.sh` prints it. A backup
without the matching key is only partially useful.

### Restore runbook

On a fresh host (or after data loss):

```bash
# 1. Bring up the stack so the DB container exists (it will be empty).
cd deploy && docker compose up -d db

# 2. Restore the database dump into it (via docker exec, matching the backup path).
docker exec -i "$(docker compose ps -q db)" \
  pg_restore -U cargo -d cargo --clean --if-exists < <timestamp>.dump

# 3. Restore the TLS certificates into Traefik's volume, then set permissions.
docker run --rm -v cargo_cargo-acme:/acme -v "$PWD/<timestamp>.certs":/backup \
  alpine sh -c 'cp /backup/acme*.json /acme/ && chmod 600 /acme/acme*.json'

# 4. Put the SAME master key (and DB password) back into .env, then start everything.
#    Confirm it matches the backup: sha256sum of the key hex == <timestamp>.keyfp.
docker compose up -d   # add your TLS overlay's -f flag for production
```

`scripts/backup-restore-test.sh` exercises the dump → restore round-trip in CI.

## Worker hosts (multi-server, early access)

Under **Admin → Worker hosts**, register any SSH-reachable machine running
Docker Engine with the compose plugin. Paste the host's address
(`user@host`), SSH port, and private key. Cargo seals the key with the
master key, pins the host's fingerprint on first contact (mismatch aborts
all operations), and gives each host an isolated docker context under
`<dataDir>/hosts/<id>/`.

When creating an app, pick a worker host in the form. Blue/green deploys
work identically on workers. If a worker goes unreachable mid-deploy the
deployment fails cleanly and other hosts are unaffected.

For the full design see
[multi-server spec](../docs/superpowers/specs/2026-08-22-cargo-multi-server-design.md).

## Threat model

The control plane mounts the Docker socket **read-write, as root** — it has
to: a PaaS whose job is to deploy containers needs the daemon. Write access
to that socket is root on the host, so any remote code execution in the
control plane is host compromise. See the main
[README threat model](../README.md#threat-model) for the full discussion and
operator guidance.

What Cargo defends is the boundary *below* the control plane: tenant inputs
(compose files, build paths, container labels, repository URLs, resource
caps) are validated rather than trusted. Deployed apps run on `cargo-proxy`
with `no-new-privileges` and resource caps, and cannot reach the platform
database on its separate internal network.

## Key rotation

If `CARGO_MASTER_KEY` is suspected compromised:

```bash
# 1. Generate a new key (save it before continuing!)
cargod gen-key

# 2. Stop the control plane, re-seal everything under the new key, update .env
cargod rotate-key   # prompts for the old key and re-seals in one transaction

# 3. Start the stack with the new key in .env
docker compose up -d
```

`rotate-key` covers every persisted secret shape: env vars, registry
credentials, database passwords, SMTP/GitHub/OIDC/webhook settings, and
OIDC client secrets. Wrong old key aborts before any write; re-running a
completed rotation is a no-op.

## Development

```bash
docker compose -f docker-compose.dev.yml up --build
```

Serves the platform on http://localhost:8080 without Traefik/SSL.
