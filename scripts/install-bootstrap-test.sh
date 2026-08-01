#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BOOTSTRAP="$ROOT/install.sh"
TMPDIR_TEST="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_TEST"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
mkdir -p "$TMPDIR_TEST/bin"
cat > "$TMPDIR_TEST/bin/git" <<'EOF'
#!/usr/bin/env sh
set -eu
if [ "$1" = "clone" ]; then
  for target in "$@"; do :; done
  mkdir -p "$target/deploy"
  cat > "$target/deploy/install.sh" <<'INNER'
#!/usr/bin/env sh
printf '%s\n' "$PWD" > "$CARGO_TEST_MARKER"
INNER
  chmod +x "$target/deploy/install.sh"
  exit 0
fi
exit 1
EOF
chmod +x "$TMPDIR_TEST/bin/git"

cat > "$TMPDIR_TEST/bin/docker" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod +x "$TMPDIR_TEST/bin/docker"

PATH="$TMPDIR_TEST/bin:$PATH" \
CARGO_INSTALL_DIR="$TMPDIR_TEST/install" \
CARGO_REPO_URL="https://example.test/cargo.git" \
CARGO_REPO_BRANCH="v0.1.0" \
CARGO_TEST_MARKER="$TMPDIR_TEST/ran-from" \
"$BOOTSTRAP"

test -f "$TMPDIR_TEST/ran-from" || fail "expected the deploy installer to run"
printf 'PASS: bootstrap clones Cargo and runs its deploy installer\n'
