#!/usr/bin/env bash
#
# deploy-relay.sh — Self-deploy an agent-speaker compatible Nostr relay.
#
# Foundation: khatru (https://github.com/fiatjaf/khatru) — Go, by fiatjaf.
# Strategy:   This is a skeleton. It clones the khatru `basic` example as the
#             starting point. As Agent-Speaker's L2 routing plugins land in a
#             separate repo (planned: AuraAIHQ/relay-khatru), this script will
#             switch to building that fork instead.
#
# Usage:
#   ./scripts/deploy-relay.sh local         # run locally on :3334 (default)
#   ./scripts/deploy-relay.sh local 4000    # run locally on custom port
#   ./scripts/deploy-relay.sh tunnel        # local + cloudflared quick tunnel
#   ./scripts/deploy-relay.sh tunnel my.example.com  # named tunnel (needs config)
#
# Prerequisites:
#   - Go 1.22+
#   - git
#   - (optional, for `tunnel` mode) cloudflared
#
# Output:
#   build/relay-src/   the cloned source tree
#   build/relay        the compiled binary
#
set -euo pipefail

# --- config ---------------------------------------------------------------
KHATRU_REPO="https://github.com/fiatjaf/khatru.git"
# Once our fork exists, change to:
# RELAY_REPO="https://github.com/AuraAIHQ/relay-khatru.git"
RELAY_REPO="${RELAY_REPO:-$KHATRU_REPO}"
RELAY_REF="${RELAY_REF:-master}"

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$PROJECT_ROOT/build"
SRC_DIR="$BUILD_DIR/relay-src"
BIN_PATH="$BUILD_DIR/relay"

MODE="${1:-local}"
PORT="${2:-3334}"

# --- helpers --------------------------------------------------------------
log()  { printf "\033[36m[relay]\033[0m %s\n" "$*"; }
warn() { printf "\033[33m[relay]\033[0m %s\n" "$*" >&2; }
die()  { printf "\033[31m[relay]\033[0m %s\n" "$*" >&2; exit 1; }

require() { command -v "$1" >/dev/null 2>&1 || die "Missing dependency: $1"; }

# --- preflight ------------------------------------------------------------
require go
require git

mkdir -p "$BUILD_DIR"

# --- sync source ----------------------------------------------------------
if [ ! -d "$SRC_DIR/.git" ]; then
  log "Cloning $RELAY_REPO @ $RELAY_REF → $SRC_DIR"
  git clone --depth 1 --branch "$RELAY_REF" "$RELAY_REPO" "$SRC_DIR"
else
  log "Updating existing source at $SRC_DIR"
  git -C "$SRC_DIR" fetch origin "$RELAY_REF"
  git -C "$SRC_DIR" reset --hard "origin/$RELAY_REF"
fi

# --- build ---------------------------------------------------------------
# khatru ships a minimal example under examples/basic.
# When our fork lands, this path will be the root of the fork.
EXAMPLE_DIR="$SRC_DIR/examples/basic"
if [ ! -d "$EXAMPLE_DIR" ]; then
  die "Example dir not found at $EXAMPLE_DIR. Upstream layout may have changed."
fi

log "Building relay binary → $BIN_PATH"
cd "$EXAMPLE_DIR"
go build -o "$BIN_PATH" .

# --- run -----------------------------------------------------------------
case "$MODE" in
  local)
    log "Starting relay on ws://localhost:$PORT"
    log "Press Ctrl+C to stop."
    exec "$BIN_PATH" -port "$PORT"
    ;;

  tunnel)
    require cloudflared
    HOSTNAME="${2:-}"
    log "Starting relay locally on :$PORT"
    "$BIN_PATH" -port "$PORT" &
    RELAY_PID=$!
    trap 'kill $RELAY_PID 2>/dev/null || true' EXIT INT TERM

    if [ -z "$HOSTNAME" ]; then
      log "Starting cloudflared quick tunnel (random URL)"
      cloudflared tunnel --url "http://localhost:$PORT"
    else
      log "Starting cloudflared named tunnel → $HOSTNAME"
      log "Expects ~/.cloudflared/config.yml configured for $HOSTNAME"
      cloudflared tunnel run "$HOSTNAME"
    fi
    ;;

  *)
    die "Unknown mode: $MODE (use: local | tunnel)"
    ;;
esac
