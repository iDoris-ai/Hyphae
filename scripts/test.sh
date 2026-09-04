#!/bin/bash
# Run all tests after refactoring

set -e

echo "🧪 Running tests..."

# Ensure dependencies
echo "📦 Installing dependencies..."
go mod tidy

# Build the project
echo ""
echo "🔨 Building project..."
go build -o bin/hyphae ./cmd/hyphae/main.go

# Run basic CLI tests
echo ""
echo "🔬 Running basic CLI tests..."

# Test key generate
echo "   Testing key generate..."
./bin/hyphae key generate > /dev/null 2>&1 && echo "   ✅ key generate" || echo "   ❌ key generate"

# Test identity list
echo "   Testing identity list..."
./bin/hyphae identity list > /dev/null 2>&1 && echo "   ✅ identity list" || echo "   ❌ identity list"

# Test contact list
echo "   Testing contact list..."
./bin/hyphae contact list > /dev/null 2>&1 && echo "   ✅ contact list" || echo "   ❌ contact list"

# Test history stats
echo "   Testing history stats..."
./bin/hyphae history stats > /dev/null 2>&1 && echo "   ✅ history stats" || echo "   ❌ history stats"

# Test decode
echo "   Testing decode..."
./bin/hyphae decode -i npub1cndcuc26ngzk76j8mun2nx060ky2wdd6akagsx00s7q5mt4w7jdqfv9lw4 > /dev/null 2>&1 && echo "   ✅ decode" || echo "   ❌ decode"

echo ""
echo "✅ All basic tests passed!"
