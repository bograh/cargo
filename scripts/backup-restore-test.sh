#!/usr/bin/env bash
# Proves the control-plane backup path round-trips: pg_dump -Fc via `docker exec`
# (the same mechanism internal/jobs/platformbackup.go uses, since the runtime
# image ships no pg_dump) → pg_restore into a fresh database → the seeded row
# survives. Run locally or in CI; requires only Docker.
set -euo pipefail

CTR="cargo-backup-test-$$"
IMG="postgres:16-alpine"
WORK="$(mktemp -d)"
cleanup() {
  docker rm -f "$CTR" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> starting $IMG"
docker run -d --name "$CTR" -e POSTGRES_USER=cargo -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=cargo "$IMG" >/dev/null

echo "==> waiting for readiness"
# A real query, not just pg_isready: during init postgres briefly accepts
# connections on a temporary server before restarting, which pg_isready passes.
ready=""
for _ in $(seq 1 60); do
  if docker exec "$CTR" psql -U cargo -d cargo -tAc 'SELECT 1' >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
test -n "$ready" || { echo "FAIL: database never became ready"; exit 1; }

echo "==> seeding a known row"
docker exec -i "$CTR" psql -U cargo -d cargo -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE canary (id int PRIMARY KEY, note text);
INSERT INTO canary VALUES (1, 'survives-restore');
SQL

echo "==> pg_dump -Fc via docker exec (mirrors the backup job)"
docker exec -i "$CTR" pg_dump -Fc -U cargo -d cargo > "$WORK/cargo.dump"
test -s "$WORK/cargo.dump" || { echo "FAIL: empty dump"; exit 1; }

echo "==> restoring into a fresh database"
docker exec -i "$CTR" psql -U cargo -d cargo -c 'CREATE DATABASE restored;' >/dev/null
docker exec -i "$CTR" pg_restore -U cargo -d restored < "$WORK/cargo.dump"

echo "==> verifying the row survived"
GOT="$(docker exec -i "$CTR" psql -U cargo -d restored -tAc "SELECT note FROM canary WHERE id=1;")"
if [ "$GOT" != "survives-restore" ]; then
  echo "FAIL: expected 'survives-restore', got '$GOT'"
  exit 1
fi

echo "PASS: control-plane backup round-trips (pg_dump -> pg_restore)"
