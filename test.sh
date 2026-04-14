#!/bin/bash
# Comprehensive test script for agent-speaker

set -e

echo "🧪 Running agent-speaker tests..."

# Test function
run_test() {
    local name="$1"
    local cmd="$2"
    
    if eval "$cmd" > /dev/null 2>&1; then
        echo "  ✅ $name"
    else
        echo "  ❌ $name"
        return 1
    fi
}

# Ensure dependencies
echo "📦 Installing dependencies..."
go mod tidy > /dev/null 2>&1

# Build the project
echo ""
echo "🔨 Building project..."
./build.sh > /dev/null 2>&1

echo ""
echo "========================================="
echo "UNIT TESTS"
echo "========================================="

# Run compress package tests
if go test ./pkg/compress > /dev/null 2>&1; then
    echo "  ✅ pkg/compress"
else
    echo "  ❌ pkg/compress"
fi

echo ""
echo "========================================="
echo "CLI TESTS"
echo "========================================="

# Nostr base commands
run_test "key generate" "./bin/agent-speaker key generate"
run_test "identity list" "./bin/agent-speaker identity list"
run_test "contact list" "./bin/agent-speaker contact list"
run_test "history stats" "./bin/agent-speaker history stats"
run_test "decode bech32" "./bin/agent-speaker decode -i npub1cndcuc26ngzk76j8mun2nx060ky2wdd6akagsx00s7q5mt4w7jdqfv9lw4"

echo ""
echo "========================================="
echo "E2E TESTS (Requires identities)"
echo "========================================="

# Check if we have test identities for E2E tests
if ./bin/agent-speaker identity list | grep -q "No identities"; then
    echo "  ⚠️  Skipping E2E tests (no identities found)"
    echo "      Run: ./bin/agent-speaker identity create --nickname test"
else
    echo "  ✅ Identities found - E2E tests ready"
    echo "      Run: ./test_e2e.sh for full E2E tests"
fi

echo ""
echo "✅ Unit & CLI tests complete!"
echo "   Run ./test_e2e.sh for comprehensive E2E tests"
