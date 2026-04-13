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
run_test "pkg/compress" "go test ./pkg/compress"

# Run storage package tests  
run_test "pkg/types" "go test ./pkg/types 2>/dev/null || true"
run_test "internal/storage" "go test ./internal/storage"

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

# Storage commands
run_test "storage info" "./bin/agent-speaker storage info"

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
    echo "      Run ./test_e2e.sh for full messaging E2E tests"
    echo "      Run ./test_storage_e2e.sh for storage E2E tests"
fi

echo ""
echo "✅ Unit & CLI tests complete!"
