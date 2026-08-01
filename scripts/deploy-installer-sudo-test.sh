#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMPDIR_TEST="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_TEST"' EXIT

mkdir -p "$TMPDIR_TEST/bin"
cp -R "$ROOT/deploy" "$TMPDIR_TEST/deploy"

cat > "$TMPDIR_TEST/bin/docker" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod +x "$TMPDIR_TEST/bin/docker"

cat > "$TMPDIR_TEST/bin/sudo" <<'EOF'
#!/usr/bin/env sh
printf '%s\n' "$*" >> "$CARGO_TEST_SUDO_LOG"
exec "$@"
EOF
chmod +x "$TMPDIR_TEST/bin/sudo"

PATH="$TMPDIR_TEST/bin:$PATH" \
CARGO_USE_SUDO=1 \
CARGO_PLATFORM_DOMAIN=localhost \
CARGO_TEST_SUDO_LOG="$TMPDIR_TEST/sudo.log" \
"$TMPDIR_TEST/deploy/install.sh" --no-up

grep -Fqx 'docker compose version' "$TMPDIR_TEST/sudo.log" || {
  printf 'FAIL: expected Docker Compose checks to run through sudo\n' >&2
  exit 1
}
printf 'PASS: deploy installer uses sudo for Docker when requested\n'
