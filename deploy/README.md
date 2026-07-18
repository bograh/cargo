# Deploying Cargo

## Prerequisites (FR-8.2)

- A Linux host with Docker Engine + the compose plugin
- DNS records pointing at the host:
  - `cargo.yourdomain.com` → server IP (platform UI)
  - `*.apps.yourdomain.com` → server IP (wildcard for app subdomains)
- Ports 80 and 443 open

## Production

```bash
cd deploy
cp .env.example .env   # created by install.sh in a later phase; keys documented in docker-compose.yml
chmod 600 .env
docker compose up -d
```

First registered account becomes the instance admin.

### SSL modes (FR-5.3)

- **HTTP-01 (default):** no DNS API needed. Each auto subdomain and custom
  domain gets its own certificate on first request. Subject to Let's
  Encrypt rate limits with many apps.
- **Wildcard DNS-01 (recommended):** run with the overlay file —
  `docker compose -f docker-compose.yml -f docker-compose.dns01.yml up -d` —
  and set `CARGO_DNS_PROVIDER` (a [Traefik DNS provider name](https://doc.traefik.io/traefik/https/acme/#providers),
  e.g. `cloudflare`) plus the provider's credential env vars in `.env`
  (e.g. `CF_DNS_API_TOKEN`). One certificate covers `*.apps.yourdomain.com`.

Custom domains always use HTTP-01 once their DNS points at the server.

### Upgrades (NFR-8)

```bash
docker compose pull && docker compose up -d
```

Migrations run automatically at startup; running user apps are not touched.

## Development

```bash
docker compose -f docker-compose.dev.yml up --build
```

Serves the platform on http://localhost:8080 without Traefik/SSL.
