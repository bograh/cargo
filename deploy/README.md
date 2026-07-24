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

## Development

```bash
docker compose -f docker-compose.dev.yml up --build
```

Serves the platform on http://localhost:8080 without Traefik/SSL.
