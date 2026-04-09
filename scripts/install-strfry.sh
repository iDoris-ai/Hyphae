#!/bin/bash
# Install strfry from source (macOS/Linux)
# Usage: ./scripts/install-strfry.sh

set -e

STRFRY_VERSION="1.1.0-beta5"
INSTALL_DIR="${HOME}/.local/bin"
REPO_DIR="/tmp/strfry-build-$$"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Detect OS
OS=$(uname -s)
ARCH=$(uname -m)

log_info "Detected: $OS $ARCH"

# Check prerequisites
check_deps() {
    local missing=()
    
    if ! command -v git &>/dev/null; then
        missing+=("git")
    fi
    
    if ! command -v make &>/dev/null; then
        missing+=("make")
    fi
    
    if ! command -v g++ &>/dev/null && ! command -v clang++ &>/dev/null; then
        missing+=("gcc/clang")
    fi
    
    if [ ${#missing[@]} -ne 0 ]; then
        log_error "Missing dependencies: ${missing[*]}"
        echo "Install with:"
        if [ "$OS" = "Darwin" ]; then
            echo "  brew install git make llvm"
        else
            echo "  sudo apt-get install git make g++  # Debian/Ubuntu"
            echo "  sudo yum install git make gcc-c++   # RHEL/CentOS"
        fi
        exit 1
    fi
}

# Install on macOS
install_macos() {
    log_info "Installing dependencies for macOS..."
    
    # Check for brew
    if ! command -v brew &>/dev/null; then
        log_error "Homebrew not found. Install from https://brew.sh"
        exit 1
    fi
    
    # Install dependencies
    brew install cmake libtool automake autoconf flatbuffers 2>/dev/null || true
    
    # Clone and build
    log_info "Cloning strfry $STRFRY_VERSION..."
    git clone --branch "$STRFRY_VERSION" https://github.com/hoytech/strfry.git "$REPO_DIR"
    cd "$REPO_DIR"
    
    log_info "Initializing submodules..."
    git submodule update --init
    
    log_info "Building strfry (this may take 5-10 minutes)..."
    make setup-golpe
    make -j$(sysctl -n hw.ncpu)
    
    # Install
    mkdir -p "$INSTALL_DIR"
    cp strfry "$INSTALL_DIR/"
    
    log_info "strfry installed to $INSTALL_DIR/strfry"
}

# Install on Linux
install_linux() {
    log_info "Installing dependencies for Linux..."
    
    # Detect package manager
    if command -v apt-get &>/dev/null; then
        sudo apt-get update
        sudo apt-get install -y cmake libtool automake autoconf libssl-dev zlib1g-dev
    elif command -v yum &>/dev/null; then
        sudo yum install -y cmake libtool automake autoconf openssl-devel zlib-devel
    else
        log_warn "Unknown package manager, assuming dependencies are installed"
    fi
    
    # Clone and build
    log_info "Cloning strfry $STRFRY_VERSION..."
    git clone --branch "$STRFRY_VERSION" https://github.com/hoytech/strfry.git "$REPO_DIR"
    cd "$REPO_DIR"
    
    log_info "Initializing submodules..."
    git submodule update --init
    
    log_info "Building strfry (this may take 5-10 minutes)..."
    make setup-golpe
    make -j$(nproc)
    
    # Install
    mkdir -p "$INSTALL_DIR"
    cp strfry "$INSTALL_DIR/"
    
    log_info "strfry installed to $INSTALL_DIR/strfry"
}

# Main
main() {
    # Check if already installed
    if command -v strfry &>/dev/null; then
        log_info "strfry already installed: $(which strfry)"
        strfry --version
        exit 0
    fi
    
    check_deps
    
    case "$OS" in
        Darwin)
            install_macos
            ;;
        Linux)
            install_linux
            ;;
        *)
            log_error "Unsupported OS: $OS"
            exit 1
            ;;
    esac
    
    # Cleanup
    rm -rf "$REPO_DIR"
    
    # Add to PATH if needed
    if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
        log_warn "Please add $INSTALL_DIR to your PATH:"
        echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.bashrc"
        echo "  # Or for zsh:"
        echo "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.zshrc"
    fi
    
    # Verify
    log_info "Verifying installation..."
    "$INSTALL_DIR/strfry" --version
    
    log_info "✅ Installation complete!"
    echo ""
    echo "Quick start:"
    echo "  strfry relay                    # Start relay (port 7777)"
    echo "  strfry --help                   # Show help"
}

main "$@"
