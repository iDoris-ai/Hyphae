#!/usr/bin/env bash
# Build and run this repository's persistent Hyphae relay.
# Usage: deploy-relay.sh check | local [port] | tunnel [hostname] [port]
set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$PROJECT_ROOT/build"
BIN_PATH="$BUILD_DIR/relay"
MODE="${1:-local}"
ARG2="${2:-}"
ARG3="${3:-}"

log()  { printf '\033[36m[relay]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[relay]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m[relay]\033[0m %s\n' "$*" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || die "Missing dependency: $1"; }

# Old deployments used these values to clone arbitrary relay sources. Fail
# explicitly so a stale environment cannot appear to select a relay version.
if [[ -n "${RELAY_REPO+x}${RELAY_REF+x}" ]]; then
  die "RELAY_REPO/RELAY_REF are no longer supported; this script builds the relay in this repository. Unset both variables."
fi

required_go="$(awk '$1 == "go" { print $2; exit }' "$PROJECT_ROOT/go.mod")"
[[ -n "$required_go" ]] || die "Could not read Go version from $PROJECT_ROOT/go.mod"

version_ge() {
  [[ "$1" == "$2" ]] && return 0
  [[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" == "$2" ]]
}

port_in_use() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
  else
    (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null \
      || (exec 3<>"/dev/tcp/::1/$port") 2>/dev/null
  fi
}

check_go() {
  if ! command -v go >/dev/null 2>&1; then
    echo "❌ go: not found in PATH"
    return 1
  fi
  local ver
  ver="$(go version | awk '{print $3}' | sed 's/^go//')"
  if version_ge "$ver" "$required_go"; then
    echo "✅ go: $ver (go.mod requires $required_go+)"
  else
    echo "❌ go: $ver found, but go.mod requires $required_go+"
    return 1
  fi
}

check_port() {
  local port="$1"
  if ! [[ "$port" =~ ^[0-9]{1,5}$ ]] || (( 10#$port < 1 || 10#$port > 65535 )); then
    echo "❌ port $port: must be a number from 1 to 65535"
    return 1
  fi
  if port_in_use "$port"; then
    echo "❌ port $port: already in use"
    return 1
  fi
  echo "✅ port $port: free"
}

is_loopback() {
  local ip="$1" octet a b c
  [[ "$ip" == "::1" || "$ip" == "[::1]" ]] && return 0
  [[ "$ip" =~ ^127(\.[0-9]{1,3}){3}$ ]] || return 1
  IFS=. read -r a b c <<< "${ip#127.}"
  for octet in "$a" "$b" "$c"; do
    (( 10#$octet <= 255 )) || return 1
  done
}

check_listen() {
  local ip="$1" public_ok="$2"
  if is_loopback "$ip"; then
    echo "✅ listen address: $ip (loopback)"
    return 0
  fi
  if [[ "$public_ok" == "1" ]]; then
    echo "✅ listen address: $ip (explicit public binding)"
    return 0
  fi
  echo "❌ non-loopback RELAY_LISTEN requires RELAY_PUBLIC=1"
  return 1
}

case "$MODE" in
  check)
    PORT="${RELAY_PORT:-3334}"
    failures=0
    log "Preflight checks (port=$PORT, Go $required_go+)"
    check_go || failures=$((failures + 1))
    check_port "$PORT" || failures=$((failures + 1))
    [[ -d "$PROJECT_ROOT/cmd/hyphae-relay" ]] || { echo "❌ relay command is missing"; failures=$((failures + 1)); }
    check_listen "${RELAY_LISTEN:-127.0.0.1}" "${RELAY_PUBLIC:-}" || failures=$((failures + 1))
    if [[ "$failures" -eq 0 ]]; then log "All checks passed."; else warn "$failures check(s) failed."; fi
    (( failures == 0 ))
    ;;

  local|tunnel)
    require go
    if [[ "$MODE" == "local" ]]; then
      PORT="${ARG2:-${RELAY_PORT:-3334}}"
    else
      # The first tunnel argument is a hostname. A port, when needed, is third.
      PORT="${ARG3:-${RELAY_PORT:-3334}}"
    fi
    check_port "$PORT" || die "Invalid or occupied port"
    check_listen "${RELAY_LISTEN:-127.0.0.1}" "${RELAY_PUBLIC:-}" || die "Invalid listen address configuration"
    mkdir -p "$BUILD_DIR"
    log "Building the in-repository relay → $BIN_PATH"
    (cd "$PROJECT_ROOT" && go build -o "$BIN_PATH" ./cmd/hyphae-relay)

    RELAY_ARGS=(--listen "${RELAY_LISTEN:-127.0.0.1}" --port "$PORT" --data-dir "${RELAY_DATA_DIR:-$BUILD_DIR/relay-data}")
    [[ "${RELAY_PUBLIC:-}" == "1" ]] && RELAY_ARGS+=(--public)
    if [[ "$MODE" == "local" ]]; then
      log "Starting relay on ws://${RELAY_LISTEN:-127.0.0.1}:$PORT"
      log "Press Ctrl+C to stop."
      exec "$BIN_PATH" "${RELAY_ARGS[@]}"
    fi

    require cloudflared
    require curl
    tunnel_hostname="$ARG2"
    tunnel_host="${RELAY_LISTEN:-127.0.0.1}"
    [[ "$tunnel_host" == *:* && "$tunnel_host" != \[*\] ]] && tunnel_host="[$tunnel_host]"
    log "Starting relay locally on $tunnel_host:$PORT; tunnel URLs can be reached by anyone who has them."
    "$BIN_PATH" "${RELAY_ARGS[@]}" &
    RELAY_PID=$!
    trap 'kill "$RELAY_PID" 2>/dev/null || true' EXIT INT TERM
    if ! curl --fail --silent --max-time 1 --retry 50 --retry-connrefused --retry-delay 0 --retry-max-time 3 \
      -H 'Accept: application/nostr+json' "http://$tunnel_host:$PORT" >/dev/null 2>&1; then
      kill -0 "$RELAY_PID" 2>/dev/null || die "Relay exited before becoming ready."
      die "Relay did not become ready within the 4-second readiness deadline."
    fi
    if [[ -z "$tunnel_hostname" ]]; then
      log "Starting cloudflared quick tunnel (random public URL)"
      cloudflared tunnel --url "http://$tunnel_host:$PORT"
    else
      log "Starting cloudflared named tunnel → $tunnel_hostname"
      log "Expects ~/.cloudflared/config.yml configured for $tunnel_hostname"
      cloudflared tunnel run "$tunnel_hostname"
    fi
    ;;

  *)
    die "Unknown mode: $MODE (use: check | local [port] | tunnel [hostname] [port])"
    ;;
esac
