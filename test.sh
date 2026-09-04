#!/bin/bash
# Comprehensive test script for hyphae

set -e

echo "🧪 Running hyphae tests..."

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
run_test "key generate" "./bin/hyphae key generate"
run_test "identity list" "./bin/hyphae identity list"
run_test "contact list" "./bin/hyphae contact list"
run_test "history stats" "./bin/hyphae history stats"
run_test "decode bech32" "./bin/hyphae decode -i npub1cndcuc26ngzk76j8mun2nx060ky2wdd6akagsx00s7q5mt4w7jdqfv9lw4"

# Storage commands
run_test "storage info" "./bin/hyphae storage info"

echo ""
echo "========================================="
echo "GROUP CHAT UNIT TESTS"
echo "========================================="

run_test "internal/group" "go test ./internal/group"

echo ""
echo "========================================="
echo "AGENT PROFILE UNIT TESTS"
echo "========================================="

run_test "pkg/types profile" "go test ./pkg/types"
run_test "internal/profile" "go test ./internal/profile"

echo ""
echo "========================================="
echo "TUI UNIT TESTS"
echo "========================================="

run_test "internal/tui" "go test ./internal/tui"

echo ""
echo "========================================="
echo "E2E TESTS (Requires identities)"
echo "========================================="

# Check if we have test identities for E2E tests
if ./bin/hyphae identity list | grep -q "No identities"; then
    echo "  ⚠️  Skipping E2E tests (no identities found)"
    echo "      Run: ./bin/hyphae identity create --nickname test"
else
    echo "  ✅ Identities found - E2E tests ready"
    echo "      Run ./test_e2e.sh for full messaging E2E tests"
    echo "      Run ./test_storage_e2e.sh for storage E2E tests"
    echo "      Run ./test_group_e2e.sh for group chat E2E tests"
    echo "      Run ./test_profile_e2e.sh for agent profile E2E tests"
    echo "      Run ./test_tui_e2e.sh for TUI E2E tests"
fi

echo ""
echo "✅ Unit & CLI tests complete!"
