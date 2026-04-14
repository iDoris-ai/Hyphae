#!/bin/bash
# Build script for agent-speaker

set -e

echo "🔨 Building agent-speaker..."

# Ensure output directory exists
mkdir -p bin

# Build the application
go build -o bin/agent-speaker ./cmd/agent-speaker/main.go

echo "✅ Build complete: bin/agent-speaker"

# Optional: install to GOPATH/bin
if [ "$1" == "install" ]; then
    echo "📦 Installing to GOPATH/bin..."
    go install ./cmd/agent-speaker/main.go
    echo "✅ Installed to $(go env GOPATH)/bin/agent-speaker"
fi
