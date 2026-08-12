#!/bin/bash
# Build script for hyphae

set -e

echo "🔨 Building hyphae..."

# Ensure output directory exists
mkdir -p bin

# Build the application
go build -o bin/hyphae ./cmd/hyphae/main.go

echo "✅ Build complete: bin/hyphae"

# Optional: install to GOPATH/bin
if [ "$1" == "install" ]; then
    echo "📦 Installing to GOPATH/bin..."
    go install ./cmd/hyphae/main.go
    echo "✅ Installed to $(go env GOPATH)/bin/hyphae"
fi
