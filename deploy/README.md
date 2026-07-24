# Deploying Cargo

## Prerequisites (FR-8.2)

- A Linux host with Docker Engine + the compose plugin
- Ports 80 and 443 open
- For a production (domain) install, DNS records pointing at the host:
  - `cargo.yourdomain.com` → server IP (platform UI)
  - `*.apps.yourdomain.com` → server IP (wildcard for app subdomains)

No domain? Skip the DNS records — a local install on localhost / the
server's IP works out of the box (see below).

## Install

```bash
cd deploy
./install.sh   # prompts, generates secrets into .env, docker compose up -d
```

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

Custom domains always use HTTP-01 once their DNS points at the server.

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

## Development

```bash
docker compose -f docker-compose.dev.yml up --build
```

Serves the platform on http://localhost:8080 without Traefik/SSL.
