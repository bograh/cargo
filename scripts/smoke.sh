#!/usr/bin/env bash
# Cargo E2E smoke (NFR-1 demo): build the image → start the dev stack on a
# random project/port → register admin → create org → deploy an nginx image
# app → poll until live → assert the app container runs → exercise app
# teardown. Exits non-zero on any failure and cleans up after itself.
set -euo pipefail

cd "$(dirname "$0")/.."

PROJECT="cargo-smoke-$(date +%s)-$$"
PORT="$(shuf -i 18100-18999 -n 1)"
BASE="http://localhost:${PORT}/api/v1"
COMPOSE=(docker compose -p "$PROJECT" -f deploy/docker-compose.dev.yml)
JAR="$(mktemp)"
SLUG="smoke-web"

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31msmoke FAILED:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
  say "teardown"
  docker ps -aq --filter "name=cargo-app-${SLUG}" | xargs -r docker rm -f >/dev/null 2>&1 || true
  "${COMPOSE[@]}" down -v --rmi local --remove-orphans >/dev/null 2>&1 || true
  rm -f "$JAR"
}
trap cleanup EXIT

for tool in docker curl jq; do command -v "$tool" >/dev/null || fail "$tool is required"; done

say "pre-pulling nginx:alpine (keeps the deploy within its budget)"
docker pull -q nginx:alpine >/dev/null

say "building and starting the platform (project $PROJECT, port $PORT)"
CARGO_DEV_PORT="$PORT" "${COMPOSE[@]}" up -d --build

say "waiting for /healthz"
healthy=0
for _ in $(seq 90); do
  if curl -fsS "http://localhost:${PORT}/healthz" >/dev/null 2>&1; then healthy=1; break; fi
  sleep 2
done
[[ $healthy -eq 1 ]] || fail "platform never became healthy"

req() { # req METHOD PATH [JSON] → response body; fails on non-2xx
  local method="$1" path="$2" body="${3:-}"
  local args=(-fsS -X "$method" -c "$JAR" -b "$JAR" -H 'content-type: application/json')
  [[ -n "$body" ]] && args+=(-d "$body")
  curl "${args[@]}" "$BASE$path"
}

say "register admin (first user)"
admin=$(req POST /auth/register '{"email":"smoke@example.com","password":"smoke-test-pw-123"}')
[[ $(jq -r .is_instance_admin <<<"$admin") == "true" ]] || fail "first user is not instance admin: $admin"

say "create org"
ORG_ID=$(req POST /orgs '{"name":"Smoke"}' | jq -r .id)
[[ -n "$ORG_ID" && "$ORG_ID" != "null" ]] || fail "org creation returned no id"

say "create nginx image app"
app=$(req POST "/orgs/$ORG_ID/apps" '{"name":"Smoke Web","source_type":"image","image_ref":"nginx:alpine","exposed_port":80,"healthcheck_path":"/"}')
APP_ID=$(jq -r .id <<<"$app")
[[ $(jq -r .slug <<<"$app") == "$SLUG" ]] || fail "unexpected app: $app"

say "deploy"
DEP_ID=$(req POST "/apps/$APP_ID/deploy" '{}' | jq -r .id)
[[ -n "$DEP_ID" && "$DEP_ID" != "null" ]] || fail "deploy returned no deployment id"

say "polling deployment until live (max 120s)"
status=""
for _ in $(seq 40); do
  dep=$(req GET "/deployments/$DEP_ID")
  status=$(jq -r .status <<<"$dep")
  case "$status" in
    live) break ;;
    failed|cancelled) fail "deployment $status: $(jq -r .error <<<"$dep")" ;;
  esac
  sleep 3
done
[[ "$status" == "live" ]] || fail "deployment not live after 120s (last status: $status)"

say "asserting app container is running"
cid=$(docker ps -q --filter "name=cargo-app-${SLUG}-app")
[[ -n "$cid" ]] || fail "no running container for cargo-app-${SLUG}"
[[ $(docker inspect -f '{{.State.Status}}' "$cid") == "running" ]] || fail "app container not running"

say "deleting app (exercises the teardown path)"
req DELETE "/apps/$APP_ID" >/dev/null
if docker ps -aq --filter "name=cargo-app-${SLUG}" | grep -q .; then
  fail "app containers still present after app delete"
fi

say "smoke OK — register → org → app → deploy → live → teardown all passed"
