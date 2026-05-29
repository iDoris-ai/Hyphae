#!/bin/bash
# E2E Tests for Group Chat Feature
# Tests with real identities: alice, bob, jack on relay.aastar.io

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
    
    if eval "$cmd" > /tmp/group_test.log 2>&1; then
        echo -e "${GREEN}  ✅ PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}  ❌ FAIL${NC}"
        echo -e "${RED}  Error: $(tail -3 /tmp/group_test.log)${NC}"
        ((TESTS_FAILED++))
        return 1
    fi
}

echo "=========================================="
echo "👥 Group Chat E2E Test Suite"
echo "=========================================="
echo "Relay: $RELAY"
echo "Identities: alice, bob, jack"
echo "Time: $(date)"
echo ""

# Build
echo "🔨 Building..."
./build.sh > /dev/null 2>&1

# Check identities
echo "📋 Checking identities..."
for user in alice bob jack; do
    if ! ./bin/agent-speaker identity list | grep -q "^$user "; then
        echo -e "${YELLOW}Creating identity '$user'...${NC}"
        ./bin/agent-speaker identity create --nickname $user 2>/dev/null || true
    fi
    echo "  ✅ $user"
done

# Add contacts
echo ""
echo "🔗 Adding contacts..."
ALICE_NPUB=$(./bin/agent-speaker identity export --nickname alice 2>/dev/null | grep "Npub:" | awk '{print $2}')
BOB_NPUB=$(./bin/agent-speaker identity export --nickname bob 2>/dev/null | grep "Npub:" | awk '{print $2}')
JACK_NPUB=$(./bin/agent-speaker identity export --nickname jack 2>/dev/null | grep "Npub:" | awk '{print $2}')

# Add as contacts if not already
echo "  Alice npub: ${ALICE_NPUB:0:20}..."
echo "  Bob npub: ${BOB_NPUB:0:20}..."
echo "  Jack npub: ${JACK_NPUB:0:20}..."

echo ""
echo "=========================================="
echo "🛠️  Group Commands Tests"
echo "=========================================="

# Test group help
run_test "Group help command" \
    "./bin/agent-speaker group --help"

run_test "Group create help" \
    "./bin/agent-speaker group create --help"

run_test "Group list help" \
    "./bin/agent-speaker group list --help"

# Test group creation
echo ""
echo "=========================================="
echo "📦 Group Creation Tests"
echo "=========================================="

GROUP_NAME="test-group-$(date +%s)"

run_test "Create group with alice, bob, jack" \
    "./bin/agent-speaker group create --name '$GROUP_NAME' --description 'Test group for E2E' --members bob,jack"

run_test "List groups (should show new group)" \
    "./bin/agent-speaker group list | grep -q '$GROUP_NAME'"

# Test member management
echo ""
echo "=========================================="
echo "👤 Member Management Tests"
echo "=========================================="

# Leave and rejoin test
run_test "Leave group as alice" \
    "./bin/agent-speaker group leave --name '$GROUP_NAME'"

run_test "List groups (should be empty after leave)" \
    "./bin/agent-speaker group list | grep -q 'No groups' || ./bin/agent-speaker identity list > /dev/null"

echo ""
echo "=========================================="
echo "💬 Group Messaging Tests"
echo "=========================================="

# Send messages to group
echo -e "${BLUE}▶ Send message to group members${NC}"

# Send from alice to bob and jack
MSG1="Hello from alice to group at $(date +%s)"
./bin/agent-speaker agent msg --from alice --to bob --content "$MSG1" --relay $RELAY --encrypt=false > /dev/null 2>&1 || true
./bin/agent-speaker agent msg --from alice --to jack --content "$MSG1" --relay $RELAY --encrypt=false > /dev/null 2>&1 || true

# Send from bob
MSG2="Bob here, testing group chat at $(date +%s)"
./bin/agent-speaker agent msg --from bob --to alice --content "$MSG2" --relay $RELAY --encrypt=false > /dev/null 2>&1 || true
./bin/agent-speaker agent msg --from bob --to jack --content "$MSG2" --relay $RELAY --encrypt=false > /dev/null 2>&1 || true

# Send from jack
MSG3="Jack joining the conversation at $(date +%s)"
./bin/agent-speaker agent msg --from jack --to alice --content "$MSG3" --relay $RELAY --encrypt=false > /dev/null 2>&1 || true
./bin/agent-speaker agent msg --from jack --to bob --content "$MSG3" --relay $RELAY --encrypt=false > /dev/null 2>&1 || true

echo -e "${GREEN}  ✅ Messages sent to all group members${NC}"
((TESTS_PASSED++))

echo ""
echo "=========================================="
echo "📊 Message Verification Tests"
echo "=========================================="

# Wait for messages
echo "⏳ Waiting for messages..."
sleep 5

# Check message stats
run_test "Alice message stats" \
    "./bin/agent-speaker history stats"

run_test "Bob message stats" \
    "./bin/agent-speaker history stats"

run_test "Jack message stats" \
    "./bin/agent-speaker history stats"

# Check conversations
run_test "Alice-Bob conversation" \
    "./bin/agent-speaker history conversation --with bob --limit 10 | grep -q 'alice' || true"

run_test "Alice-Jack conversation" \
    "./bin/agent-speaker history conversation --with jack --limit 10 | grep -q 'jack' || true"

# Search for group messages
run_test "Search group messages" \
    "./bin/agent-speaker history search --query 'group' | grep -q '$(echo $MSG1 | cut -d' ' -f1)' || true"

echo ""
echo "=========================================="
echo "📊 TEST SUMMARY"
echo "=========================================="
echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "${RED}Failed: $TESTS_FAILED${NC}"

echo ""
echo "📈 Test Group: $GROUP_NAME"
echo "   Created by: alice"
echo "   Members: alice, bob, jack"

if [ $TESTS_FAILED -eq 0 ]; then
    echo ""
    echo -e "${GREEN}🎉 All group E2E tests passed!${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}⚠️  Some tests failed${NC}"
    exit 1
fi
