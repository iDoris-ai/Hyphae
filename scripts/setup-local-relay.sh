#!/bin/bash
# Setup local Nostr relay for development
# Usage: ./scripts/setup-local-relay.sh [start|stop|restart|status]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
COMPOSE_DIR="$PROJECT_ROOT/docker/relay"

ACTION="${1:-start}"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_docker() {
    if ! command -v docker &> /dev/null; then
        log_error "Docker not found. Please install Docker first."
        echo "  macOS: brew install --cask docker"
        echo "  Linux: https://docs.docker.com/engine/install/"
        exit 1
    fi
    
    if ! docker compose version &> /dev/null && ! docker-compose version &> /dev/null; then
        log_error "Docker Compose not found."
        exit 1
    fi
    
    if ! docker info &> /dev/null; then
        log_error "Docker daemon not running. Please start Docker first."
        exit 1
    fi
}

start_relay() {
    check_docker
    
    log_info "Starting local Nostr relay..."
    
    cd "$COMPOSE_DIR"
    
    # Create data directory
    mkdir -p data
    
    # Pull latest image
    docker compose pull strfry 2>/dev/null || true
    
    # Start relay
    docker compose up -d strfry
    
    # Wait for relay to be ready
    log_info "Waiting for relay to be ready..."
    for i in {1..30}; do
        if curl -s http://localhost:7777/health &> /dev/null || \
           nc -z localhost 7777 2>/dev/null; then
            log_info "✅ Local relay is ready!"
            echo ""
            echo "Relay URL: ws://localhost:7777"
            echo "Web URL:   http://localhost:7777"
            echo ""
            echo "Test connection:"
            echo "  ./bin/agent-speaker relay info ws://localhost:7777"
            return 0
        fi
        sleep 1
    done
    
    log_warn "Relay started but health check failed. Check logs:"
    echo "  docker compose -f $COMPOSE_DIR/docker-compose.yml logs strfry"
}

stop_relay() {
    log_info "Stopping local Nostr relay..."
    cd "$COMPOSE_DIR"
    docker compose down
    log_info "✅ Relay stopped"
}

restart_relay() {
    stop_relay
    start_relay
}

relay_status() {
    cd "$COMPOSE_DIR"
    
    if docker compose ps | grep -q "agent-speaker-relay"; then
        log_info "Local relay is running"
        docker compose ps
        echo ""
        echo "Relay URL: ws://localhost:7777"
        
        # Test connection
        if nc -z localhost 7777 2>/dev/null; then
            echo "Port 7777: ✅ Listening"
        else
            echo "Port 7777: ❌ Not responding"
        fi
    else
        log_warn "Local relay is not running"
        echo "Start with: ./scripts/setup-local-relay.sh start"
    fi
}

case "$ACTION" in
    start)
        start_relay
        ;;
    stop)
        stop_relay
        ;;
    restart)
        restart_relay
        ;;
    status)
        relay_status
        ;;
    logs)
        cd "$COMPOSE_DIR"
        docker compose logs -f strfry
        ;;
    *)
        echo "Usage: $0 [start|stop|restart|status|logs]"
        echo ""
        echo "Commands:"
        echo "  start   - Start local relay (default)"
        echo "  stop    - Stop local relay"
        echo "  restart - Restart local relay"
        echo "  status  - Check relay status"
        echo "  logs    - Show relay logs"
        exit 1
        ;;
esac
