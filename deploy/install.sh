#!/usr/bin/env bash
# Cargo installer (FR-8.1): dependency checks → prompts → secrets → up.
# Run from the deploy/ directory of a checkout:  ./install.sh
# Leave the platform domain empty (or give localhost/an IP) for a local
# install: plain HTTP + self-signed HTTPS, no DNS or Let's Encrypt needed.
# Non-interactive: set CARGO_PLATFORM_DOMAIN, CARGO_APPS_SUFFIX,
# CARGO_ACME_EMAIL (and optionally CARGO_DNS_PROVIDER + its credentials)
# in the environment; with none of them set you get a local install.
# Flags: --force (overwrite .env), --no-up (skip launch).
set -euo pipefail

cd "$(dirname "$0")"

FORCE=0
NO_UP=0
for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    --no-up) NO_UP=1 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

DOCKER=(docker)
if [[ ${CARGO_USE_SUDO:-0} == 1 ]]; then
  command -v sudo >/dev/null 2>&1 || fail "sudo is required to use Docker"
  DOCKER=(sudo docker)
fi
docker_cmd() { "${DOCKER[@]}" "$@"; }

# --- dependency checks -------------------------------------------------
command -v docker >/dev/null 2>&1 || fail "docker is not installed (https://docs.docker.com/engine/install/)"
docker_cmd compose version >/dev/null 2>&1 || fail "the docker compose plugin is not installed"
docker_cmd info >/dev/null 2>&1 || fail "cannot reach the docker daemon (is it running? do you need sudo?)"

if [[ -f .env && $FORCE -ne 1 ]]; then
  fail ".env already exists — this looks like an existing install. Re-run with --force to overwrite (this changes secrets!)"
fi

# --- reverse-proxy coexistence detection ---------------------------------
# A server already running nginx/Traefik/Apache owns ports 80/443; Cargo's
# bundled Traefik must not fight it for them. In external-proxy mode Cargo
# binds loopback-only ports and the existing proxy forwards traffic.
port_busy() { # port_busy PORT — 0 if something is listening on PORT
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltnH 2>/dev/null | awk '{print $4}' | grep -E "[:.]${port}\$" >/dev/null 2>&1
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | awk '{print $4}' | grep -E "[:.]${port}\$" >/dev/null 2>&1
  else
    return 1 # no tooling: assume free rather than block the install
  fi
}

EXTERNAL_PROXY="${CARGO_PROXY_MODE:-}"
if [[ -z $EXTERNAL_PROXY ]]; then
  if port_busy 80 || port_busy 443; then
    say "ports 80/443 are already in use on this host (existing nginx/Traefik/Apache?)"
    if [[ ! -t 0 ]]; then
      fail "ports 80/443 are in use. Re-run with CARGO_PROXY_MODE=external to run behind your existing reverse proxy."
    fi
    read -r -p "Run Cargo behind your existing reverse proxy instead of binding 80/443? [Y/n]: " reply
    [[ $reply =~ ^[Nn] ]] && fail "free ports 80/443 (or accept external-proxy mode) and rerun the installer."
    EXTERNAL_PROXY=external
  fi
fi

EXTERNAL_HTTP_PORT="${CARGO_EXTERNAL_HTTP_PORT:-8080}"
EXTERNAL_HTTPS_PORT="${CARGO_EXTERNAL_HTTPS_PORT:-8443}"

# --- prompts ------------------------------------------------------------
ask() { # ask VAR "prompt" [default]
  local var="$1" prompt="$2" default="${3:-}" current
  current="${!var:-}"
  if [[ -n "$current" ]]; then return 0; fi
  if [[ ! -t 0 ]]; then
    # A default counts as provided even when empty (optional prompts).
    if [[ $# -ge 3 ]]; then printf -v "$var" '%s' "$default"; return 0; fi
    fail "$var is required (no TTY for prompting)"
  fi
  read -r -p "$prompt${default:+ [$default]}: " current
  printf -v "$var" '%s' "${current:-$default}"
}

# Best-effort primary IPv4 of this host (for local installs).
detect_ip() {
  local ip=""
  if command -v ip >/dev/null 2>&1; then
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -n1)"
  fi
  if [[ -z "$ip" ]] && command -v hostname >/dev/null 2>&1; then
    ip="$(hostname -I 2>/dev/null | tr ' ' '\n' | sed -n '/^[0-9]*\./p' | head -n1)"
  fi
  printf '%s' "$ip"
}

# localhost, *.localhost, and IP literals can't get real certificates.
is_local_host() {
  [[ "$1" == "localhost" || "$1" == *.localhost ||
    "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ || "$1" == *:* ]]
}

ask CARGO_PLATFORM_DOMAIN "Platform domain (where the Cargo UI lives, e.g. cargo.example.com; empty = localhost/this server's IP)" ""

MODE=production
if [[ -z "$CARGO_PLATFORM_DOMAIN" ]] || is_local_host "$CARGO_PLATFORM_DOMAIN"; then
  MODE=local
  if [[ -z "$CARGO_PLATFORM_DOMAIN" ]]; then
    CARGO_PLATFORM_DOMAIN="$(detect_ip)"
    [[ -n "$CARGO_PLATFORM_DOMAIN" ]] || CARGO_PLATFORM_DOMAIN="localhost"
  fi
  CARGO_APPS_SUFFIX="${CARGO_APPS_SUFFIX:-apps.localhost}"
  CARGO_ACME_EMAIL=""
  CARGO_DNS_PROVIDER=""
  say "local install on ${CARGO_PLATFORM_DOMAIN}: no DNS or Let's Encrypt needed (plain HTTP + self-signed HTTPS)"
else
  ask CARGO_APPS_SUFFIX  "Apps domain suffix (apps get <name>.<suffix>, e.g. apps.example.com)"
  ask CARGO_ACME_EMAIL   "Email for Let's Encrypt certificates"
  ask CARGO_DNS_PROVIDER "DNS provider for wildcard certs (e.g. cloudflare; empty = per-domain HTTP-01)" ""
  [[ -n "$CARGO_APPS_SUFFIX" && -n "$CARGO_ACME_EMAIL" ]] ||
    fail "apps suffix and ACME email are required for a domain install"
fi

# --- secrets ------------------------------------------------------------
gen_hex() { # gen_hex BYTES
  if command -v openssl >/dev/null 2>&1; then openssl rand -hex "$1"; else
    od -vN "$1" -An -tx1 /dev/urandom | tr -d ' \n'; fi
}
MASTER_KEY="$(gen_hex 32)"
DB_PASSWORD="$(gen_hex 16)"

umask 077
cat > .env <<EOF
CARGO_PLATFORM_DOMAIN=${CARGO_PLATFORM_DOMAIN}
CARGO_APPS_SUFFIX=${CARGO_APPS_SUFFIX}
CARGO_ACME_EMAIL=${CARGO_ACME_EMAIL}
CARGO_MASTER_KEY=${MASTER_KEY}
CARGO_DB_PASSWORD=${DB_PASSWORD}
CARGO_DNS_PROVIDER=${CARGO_DNS_PROVIDER}
EOF
chmod 600 .env
say "wrote .env (mode 0600)"

if [[ $EXTERNAL_PROXY == external ]]; then
  cat >> .env <<EOF
# Existing reverse proxy on 80/443: Cargo's Traefik binds loopback only.
CARGO_HTTP_PUBLISH=127.0.0.1:${EXTERNAL_HTTP_PORT}
CARGO_HTTPS_PUBLISH=127.0.0.1:${EXTERNAL_HTTPS_PORT}
EOF
  say "external-proxy mode: Cargo's Traefik will bind 127.0.0.1:${EXTERNAL_HTTP_PORT} (HTTP) and 127.0.0.1:${EXTERNAL_HTTPS_PORT} (HTTPS)"
fi

cat <<'EOF'

  ┌─────────────────────────────────────────────────────────────────┐
  │  BACK UP CARGO_MASTER_KEY FROM .env SOMEWHERE SAFE.             │
  │  Encrypted secrets (env vars, credentials) are UNRECOVERABLE    │
  │  without it.                                                    │
  └─────────────────────────────────────────────────────────────────┘

EOF

if [[ $MODE == production ]]; then
  say "DNS prerequisites (verify before continuing):"
  echo "    ${CARGO_PLATFORM_DOMAIN}  → this server's IP"
  echo "    *.${CARGO_APPS_SUFFIX}    → this server's IP"
fi

if [[ $MODE == local ]]; then
  COMPOSE_ARGS=(-f docker-compose.yml)
elif [[ -n "$CARGO_DNS_PROVIDER" ]]; then
  say "wildcard DNS-01 mode: remember to add ${CARGO_DNS_PROVIDER}'s credential env vars to .env (see docker-compose.dns01.yml)"
  COMPOSE_ARGS=(-f docker-compose.yml -f docker-compose.dns01.yml)
else
  COMPOSE_ARGS=(-f docker-compose.yml -f docker-compose.tls.yml)
fi

if [[ $NO_UP -eq 1 ]]; then
  say "skipping launch (--no-up). Start later with: docker compose ${COMPOSE_ARGS[*]} up -d"
  exit 0
fi

say "starting Cargo…"
# cargo-proxy is a shared external network (app containers attach to it
# too); create it if this is the first install on this host.
docker_cmd network inspect cargo-proxy >/dev/null 2>&1 || docker_cmd network create cargo-proxy >/dev/null
docker_cmd compose "${COMPOSE_ARGS[@]}" up -d
if [[ $MODE == local ]]; then
  say "done. Open http://${CARGO_PLATFORM_DOMAIN} (or http://localhost) and register — the first account becomes the instance admin."
  say "apps will be served at https://<name>.${CARGO_APPS_SUFFIX} with a self-signed certificate (accept the browser warning)"
else
  say "done. Open https://${CARGO_PLATFORM_DOMAIN} and register — the first account becomes the instance admin."
fi

if [[ $EXTERNAL_PROXY == external ]]; then
  cat <<EOF

  Your existing reverse proxy must now forward traffic to Cargo:
    HTTP  → 127.0.0.1:${EXTERNAL_HTTP_PORT}
    HTTPS → 127.0.0.1:${EXTERNAL_HTTPS_PORT}

  Routes needed: ${CARGO_PLATFORM_DOMAIN} and *.${CARGO_APPS_SUFFIX}
  Ready-made configs: $(pwd)/examples/nginx-cargo.conf and
                      $(pwd)/examples/traefik-cargo-dynamic.yml
  Tip: TLS passthrough on 443 lets Cargo keep managing its own Let's
  Encrypt certificates; or terminate TLS at your proxy and forward to
  ${EXTERNAL_HTTP_PORT}.
EOF
fi
