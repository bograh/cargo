#!/usr/bin/env bash
# Cargo installer (FR-8.1): dependency checks → prompts → secrets → up.
# Run from the deploy/ directory of a checkout:  ./install.sh
# Non-interactive: set CARGO_PLATFORM_DOMAIN, CARGO_APPS_SUFFIX,
# CARGO_ACME_EMAIL (and optionally CARGO_DNS_PROVIDER + its credentials)
# in the environment. Flags: --force (overwrite .env), --no-up (skip launch).
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

# --- dependency checks -------------------------------------------------
command -v docker >/dev/null 2>&1 || fail "docker is not installed (https://docs.docker.com/engine/install/)"
docker compose version >/dev/null 2>&1 || fail "the docker compose plugin is not installed"
docker info >/dev/null 2>&1 || fail "cannot reach the docker daemon (is it running? do you need sudo?)"

if [[ -f .env && $FORCE -ne 1 ]]; then
  fail ".env already exists — this looks like an existing install. Re-run with --force to overwrite (this changes secrets!)"
fi

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

ask CARGO_PLATFORM_DOMAIN "Platform domain (where the Cargo UI lives, e.g. cargo.example.com)"
ask CARGO_APPS_SUFFIX     "Apps domain suffix (apps get <name>.<suffix>, e.g. apps.example.com)"
ask CARGO_ACME_EMAIL      "Email for Let's Encrypt certificates"
ask CARGO_DNS_PROVIDER    "DNS provider for wildcard certs (e.g. cloudflare; empty = per-domain HTTP-01)" ""

[[ -n "$CARGO_PLATFORM_DOMAIN" && -n "$CARGO_APPS_SUFFIX" && -n "$CARGO_ACME_EMAIL" ]] ||
  fail "platform domain, apps suffix, and ACME email are required"

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

cat <<'EOF'

  ┌─────────────────────────────────────────────────────────────────┐
  │  BACK UP CARGO_MASTER_KEY FROM .env SOMEWHERE SAFE.             │
  │  Encrypted secrets (env vars, credentials) are UNRECOVERABLE   │
  │  without it.                                                    │
  └─────────────────────────────────────────────────────────────────┘

EOF

say "DNS prerequisites (verify before continuing):"
echo "    ${CARGO_PLATFORM_DOMAIN}  → this server's IP"
echo "    *.${CARGO_APPS_SUFFIX}    → this server's IP"

if [[ -n "$CARGO_DNS_PROVIDER" ]]; then
  say "wildcard DNS-01 mode: remember to add ${CARGO_DNS_PROVIDER}'s credential env vars to .env (see docker-compose.dns01.yml)"
  COMPOSE_ARGS=(-f docker-compose.yml -f docker-compose.dns01.yml)
else
  COMPOSE_ARGS=(-f docker-compose.yml)
fi

if [[ $NO_UP -eq 1 ]]; then
  say "skipping launch (--no-up). Start later with: docker compose ${COMPOSE_ARGS[*]} up -d"
  exit 0
fi

say "starting Cargo…"
docker compose "${COMPOSE_ARGS[@]}" up -d
say "done. Open https://${CARGO_PLATFORM_DOMAIN} and register — the first account becomes the instance admin."
