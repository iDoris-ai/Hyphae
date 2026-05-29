#!/bin/bash
# E2E Tests for Agent Profile Feature
# Tests with real identities: alice, bob on relay.aastar.io

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

RELAY="wss://relay.aastar.io"
TESTS_PASSED=0
TESTS_FAILED=0

run_test() {
    local name="$1"
    shift
    local cmd="$@"
    
    echo -e "${BLUE}▶ $name${NC}"
    
    if eval "$cmd" > /tmp/profile_test.log 2>&1; then
        echo -e "${GREEN}  ✅ PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}  ❌ FAIL${NC}"
        echo -e "${RED}  Error: $(tail -3 /tmp/profile_test.log)${NC}"
        ((TESTS_FAILED++))
        return 1
    fi
}

echo "=========================================="
echo "👤 Agent Profile E2E Test Suite"
echo "=========================================="
echo "Relay: $RELAY"
echo "Identities: alice, bob"
echo "Time: $(date)"
echo ""

# Build
echo "🔨 Building..."
./build.sh > /dev/null 2>&1

# Check identities
echo "📋 Checking identities..."
for user in alice bob; do
    if ! ./bin/agent-speaker identity list | grep -q "^$user "; then
        echo -e "${YELLOW}Creating identity '$user'...${NC}"
        ./bin/agent-speaker identity create --nickname $user 2>/dev/null || true
    fi
    echo "  ✅ $user"
done

ALICE_NPUB=$(./bin/agent-speaker identity export --nickname alice 2>/dev/null | grep "Npub:" | awk '{print $2}')
BOB_NPUB=$(./bin/agent-speaker identity export --nickname bob 2>/dev/null | grep "Npub:" | awk '{print $2}')

echo "  Alice npub: ${ALICE_NPUB:0:20}..."
echo "  Bob npub: ${BOB_NPUB:0:20}..."

echo ""
echo "=========================================="
echo "📤 Profile Publish Tests"
echo "=========================================="

# Test 1: Publish alice's profile
run_test "Publish alice profile" \
    "./bin/agent-speaker profile publish --as alice --name 'Alice the SEO Expert' --description 'I help websites rank better' --availability available --capability 'seo:Search engine optimization' --capability 'content:Content strategy' --rate 'audit:page:50:Full SEO audit' --rate 'article:word:0.15:Blog post writing' --currency USD --relay $RELAY"

# Test 2: Publish bob's profile with different capabilities
run_test "Publish bob profile" \
    "./bin/agent-speaker profile publish --as bob --name 'Bob the Developer' --description 'Full-stack developer and smart contract auditor' --availability busy --capability 'solidity:Smart contract development' --capability 'audit:Security audit' --rate 'contract:project:500:Complete smart contract' --currency USD --relay $RELAY"

# Test 3: Help commands
run_test "Profile command help" \
    "./bin/agent-speaker profile --help"

run_test "Profile publish help" \
    "./bin/agent-speaker profile publish --help"

run_test "Profile discover help" \
    "./bin/agent-speaker profile discover --help"

echo ""
echo "=========================================="
echo "🔍 Profile Discovery Tests"
echo "=========================================="

# Wait for relay propagation
echo "⏳ Waiting for relay propagation (5s)..."
sleep 5

# Test 4: Discover alice's profile by npub
run_test "Discover alice profile by npub" \
    "./bin/agent-speaker profile discover --npub '$ALICE_NPUB' --relay $RELAY --timeout 10"

# Test 5: Discover bob's profile by npub
run_test "Discover bob profile by npub" \
    "./bin/agent-speaker profile discover --npub '$BOB_NPUB' --relay $RELAY --timeout 10"

# Test 6: Discover all profiles (no npub filter)
run_test "Discover all profiles on relay" \
    "./bin/agent-speaker profile discover --relay $RELAY --limit 20 --timeout 10"

echo ""
echo "=========================================="
echo "📋 Local Profile Management Tests"
echo "=========================================="

# Test 7: Show alice's local profile
run_test "Show alice local profile" \
    "./bin/agent-speaker profile show --npub '$ALICE_NPUB' | grep -q 'Alice the SEO Expert'"

# Test 8: Show bob's local profile
run_test "Show bob local profile" \
    "./bin/agent-speaker profile show --npub '$BOB_NPUB' | grep -q 'Bob the Developer'"

# Test 9: List all local profiles
run_test "List local profiles" \
    "./bin/agent-speaker profile list | grep -q 'Alice the SEO Expert' && ./bin/agent-speaker profile list | grep -q 'Bob the Developer'"

# Test 10: Search profiles by capability
run_test "Search profiles for 'seo'" \
    "./bin/agent-speaker profile search --query 'seo' | grep -q 'Alice the SEO Expert'"

run_test "Search profiles for 'developer'" \
    "./bin/agent-speaker profile search --query 'developer' | grep -q 'Bob the Developer'"

run_test "Search profiles for 'audit' (should find both)" \
    "./bin/agent-speaker profile search --query 'audit' | grep -q 'Alice the SEO Expert' && ./bin/agent-speaker profile search --query 'audit' | grep -q 'Bob the Developer'"

echo ""
echo "=========================================="
echo "📊 TEST SUMMARY"
echo "=========================================="
echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "${RED}Failed: $TESTS_FAILED${NC}"

if [ $TESTS_FAILED -eq 0 ]; then
    echo ""
    echo -e "${GREEN}🎉 All agent profile E2E tests passed!${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}⚠️  Some tests failed${NC}"
    exit 1
fi
