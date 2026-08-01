#!/usr/bin/env sh
# Cargo bootstrap installer. Safe to run with: curl -fsSL <URL> | sh
set -eu

INSTALL_DIR="${CARGO_INSTALL_DIR:-/opt/cargo}"
REPO_URL="${CARGO_REPO_URL:-https://github.com/bograh/cargo.git}"
REPO_BRANCH="${CARGO_REPO_BRANCH:-dev}"

say() { printf '%s\n' "$*"; }
fail() { say "Error: $*" >&2; exit 1; }

if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
elif command -v sudo >/dev/null 2>&1; then
  SUDO="sudo"
else
  fail "sudo is required when installing as a non-root user."
fi

require_linux() {
  [ "$(uname -s)" = "Linux" ] || fail "Cargo's installer currently supports Linux hosts."
}

require_apt() {
  command -v apt-get >/dev/null 2>&1 || fail "Cargo's installer currently supports Ubuntu/Debian hosts."
}

install_package() {
  require_apt
  say "Installing $*..."
  $SUDO apt-get update
  $SUDO apt-get install -y "$@"
}

ensure_git() {
  command -v git >/dev/null 2>&1 || install_package ca-certificates git
}

ensure_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    install_package ca-certificates docker.io
    if command -v systemctl >/dev/null 2>&1; then
      $SUDO systemctl enable --now docker >/dev/null 2>&1 || true
    fi
  fi

  if docker compose version >/dev/null 2>&1 || $SUDO docker compose version >/dev/null 2>&1; then
    return
  fi

  require_apt
  for package_name in docker-compose-plugin docker-compose-v2; do
    if apt-cache show "$package_name" >/dev/null 2>&1; then
      say "Installing Docker Compose plugin..."
      $SUDO apt-get update
      $SUDO apt-get install -y "$package_name"
      $SUDO docker compose version >/dev/null 2>&1 && return
    fi
  done
  fail "Docker Compose v2 was not found. Enable Docker's apt repository, then rerun this installer."
}

clone_or_update_repo() {
  if [ -d "$INSTALL_DIR/.git" ]; then
    say "Updating Cargo source in $INSTALL_DIR..."
    git -C "$INSTALL_DIR" fetch origin "$REPO_BRANCH"
    git -C "$INSTALL_DIR" checkout "$REPO_BRANCH"
    git -C "$INSTALL_DIR" pull --ff-only origin "$REPO_BRANCH"
    return
  fi

  if [ -e "$INSTALL_DIR" ]; then
    fail "$INSTALL_DIR already exists but is not a Cargo Git checkout. Move it aside and rerun the installer."
  fi

  install_parent="$(dirname "$INSTALL_DIR")"
  if ! mkdir -p "$install_parent" 2>/dev/null; then
    $SUDO mkdir -p "$install_parent"
    $SUDO chown "$(id -u):$(id -g)" "$install_parent"
  fi
  say "Cloning Cargo into $INSTALL_DIR..."
  git clone --branch "$REPO_BRANCH" --single-branch "$REPO_URL" "$INSTALL_DIR"
}

main() {
  require_linux
  ensure_git
  ensure_docker
  clone_or_update_repo

  if docker info >/dev/null 2>&1; then
    exec "$INSTALL_DIR/deploy/install.sh" "$@"
  fi
  if $SUDO docker info >/dev/null 2>&1; then
    exec env CARGO_USE_SUDO=1 "$INSTALL_DIR/deploy/install.sh" "$@"
  fi
  fail "cannot reach the Docker daemon. Is it running?"
}

main "$@"
