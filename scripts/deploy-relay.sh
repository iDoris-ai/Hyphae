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
#   ./scripts/deploy-relay.sh check         # check prerequisites only, don't deploy
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
MIN_GO_VERSION="1.22"

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

# version_ge A B: true (exit 0) if version A >= version B
version_ge() {
  [ "$1" = "$2" ] && return 0
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" = "$2" ]
}

RELAY_REPO_CHECK_TIMEOUT_SECS="${RELAY_REPO_CHECK_TIMEOUT_SECS:-10}"

# run_with_timeout SECS CMD...: runs CMD, killing it and returning 124 (same
# convention as GNU coreutils' timeout(1)) if it hasn't finished within SECS.
# macOS doesn't ship `timeout` by default (only Linux distros with coreutils
# typically do), so this prefers the real timeout/gtimeout binary when
# present and falls back to a plain bash polling loop otherwise -- no
# external dependency required on either platform.
run_with_timeout() {
  local secs="$1"
  shift
  if command -v timeout >/dev/null 2>&1; then
    timeout "$secs" "$@"
    return $?
  fi
  if command -v gtimeout >/dev/null 2>&1; then
    gtimeout "$secs" "$@"
    return $?
  fi
  "$@" &
  local cmd_pid=$!
  local waited=0
  while kill -0 "$cmd_pid" 2>/dev/null; do
    if [ "$waited" -ge "$secs" ]; then
      kill "$cmd_pid" 2>/dev/null
      wait "$cmd_pid" 2>/dev/null
      return 124
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$cmd_pid"
}

port_in_use() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
  else
    # The fd opened here is local to this subshell, so there's nothing to
    # close afterwards -- the subshell's own exit closes it. Success means
    # something accepted the connection, i.e. the port is in use. Probe
    # both loopback families since an IPv6-only listener wouldn't answer
    # on 127.0.0.1.
    (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null \
      || (exec 3<>"/dev/tcp/::1/$port") 2>/dev/null
  fi
}

# --- check mode -------------------------------------------------------------
# Diagnostic-only: verifies go/git/port/RELAY_REPO reachability without
# cloning or building anything. Every check runs (a failure doesn't stop
# the rest), and the report is printed in full before deciding the exit
# code -- so CI (or a human) always sees every failing item in one pass
# instead of fixing them one at a time across repeated invocations.
#
# The Go version check only compares against MIN_GO_VERSION (this script's
# own documented prerequisite above), not khatru/relay-khatru's go.mod --
# fetching a remote go.mod would mean this check's success depends on the
# same repo reachability being probed separately below, and khatru's example
# dir path/module layout is already re-verified at build time regardless.

check_go() {
  if ! command -v go >/dev/null 2>&1; then
    echo "❌ go: not found in PATH"
    return 1
  fi
  local ver
  ver="$(go version | awk '{print $3}' | sed 's/^go//')"
  if version_ge "$ver" "$MIN_GO_VERSION"; then
    echo "✅ go: $ver (>= $MIN_GO_VERSION required)"
  else
    echo "❌ go: $ver found, but >= $MIN_GO_VERSION is required"
    return 1
  fi
}

check_git() {
  if ! command -v git >/dev/null 2>&1; then
    echo "❌ git: not found in PATH"
    return 1
  fi
  local ver
  if ver="$(git --version)"; then
    echo "✅ git: $(echo "$ver" | awk '{print $3}')"
  else
    echo "❌ git: found in PATH, but 'git --version' failed"
    return 1
  fi
}

check_port() {
  local port="$1"
  # The length check must run before the -lt/-gt arithmetic comparisons: an
  # all-digit string longer than 5 characters is already >65535 (the max
  # valid port has 5 digits), and bash's arithmetic comparison overflows on
  # very long digit strings ("integer expression expected" on stderr, exit
  # status 2) -- since 2 is still just "non-zero" to the `||` chain, that
  # error was previously indistinguishable from "not the invalid case" and
  # fell through to port_in_use, which then misreported the bogus value as
  # free (the same failure class the earlier non-numeric/out-of-range fix
  # targeted, just triggered by integer overflow instead).
  if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "${#port}" -gt 5 ] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    echo "❌ port $port: not a valid port number (must be 1-65535)"
    return 1
  fi
  if port_in_use "$port"; then
    echo "❌ port $port: already in use -- stop whatever's listening on it, or pass a different port"
    return 1
  else
    echo "✅ port $port: free"
  fi
}

check_relay_repo() {
  local status
  run_with_timeout "$RELAY_REPO_CHECK_TIMEOUT_SECS" git ls-remote --exit-code "$RELAY_REPO" >/dev/null 2>&1
  status=$?
  if [ "$status" -eq 0 ]; then
    echo "✅ RELAY_REPO reachable: $RELAY_REPO"
  elif [ "$status" -eq 124 ]; then
    echo "❌ RELAY_REPO unreachable: $RELAY_REPO (timed out after ${RELAY_REPO_CHECK_TIMEOUT_SECS}s -- host may be blackholing the connection rather than refusing it)"
    return 1
  else
    echo "❌ RELAY_REPO unreachable: $RELAY_REPO (check the URL, network, or auth)"
    return 1
  fi
}

run_checks() {
  local failures=0
  log "Running preflight checks (RELAY_REPO=$RELAY_REPO, port=$PORT)..."
  echo
  check_go || failures=$((failures + 1))
  check_git || failures=$((failures + 1))
  check_port "$PORT" || failures=$((failures + 1))
  check_relay_repo || failures=$((failures + 1))
  echo
  if [ "$failures" -eq 0 ]; then
    log "All checks passed."
  else
    warn "$failures check(s) failed."
  fi
  return "$failures"
}

if [ "$MODE" = "check" ]; then
  if run_checks; then
    exit 0
  else
    exit 1
  fi
fi

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
    die "Unknown mode: $MODE (use: check | local | tunnel)"
    ;;
esac
