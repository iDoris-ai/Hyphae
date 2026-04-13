#!/bin/bash
# Comprehensive E2E Test for agent-speaker
# Tests actual message sending/receiving via relay.aastar.io

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Config
RELAY="wss://relay.aastar.io"
TEST_MESSAGE="E2E test message $(date +%s)"
TEST_MESSAGE_ENCRYPTED="E2E encrypted test $(date +%s)"

# Track results
TESTS_PASSED=0
TESTS_FAILED=0

# Test runner
run_test() {
    local name="$1"
    shift
    local cmd="$@"
    local timeout_sec="${3:-30}"
    
    echo -e "${BLUE}▶ $name${NC}"
    
    if eval "$cmd" > /tmp/e2e_test_output.log 2>&1; then
        echo -e "${GREEN}  ✅ PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}  ❌ FAIL${NC}"
        echo -e "${RED}  Error output:${NC}"
        tail -5 /tmp/e2e_test_output.log | sed 's/^/    /'
        ((TESTS_FAILED++))
        return 1
    fi
}

echo "=========================================="
echo "🧪 Agent-Speaker E2E Test Suite"
echo "=========================================="
echo "Relay: $RELAY"
echo "Time: $(date)"
echo ""

# Check prerequisites
echo "📋 Checking prerequisites..."

# Check binary exists
if [ ! -f "./bin/agent-speaker" ]; then
    echo -e "${YELLOW}Building binary...${NC}"
    ./build.sh
fi

# Check identities
echo -n "  Checking identities... "
if ./bin/agent-speaker identity list | grep -q "alice"; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗${NC}"
    echo -e "${YELLOW}Creating test identity 'alice'...${NC}"
    ./bin/agent-speaker identity create --nickname alice --default 2>/dev/null || true
fi

if ./bin/agent-speaker identity list | grep -q "bob_e2e"; then
    echo -e "  ${GREEN}✓ bob_e2e found${NC}"
    BOB_IDENTITY="bob_e2e"
else
    # Use any available bob contact
    BOB_IDENTITY=$(./bin/agent-speaker contact list | grep "^bob" | head -1 | awk '{print $1}')
    if [ -z "$BOB_IDENTITY" ]; then
        echo -e "${YELLOW}Warning: No bob contact found. Some tests may fail.${NC}"
    else
        echo -e "  ${GREEN}✓ Using bob identity: $BOB_IDENTITY${NC}"
    fi
fi

# Check relay connectivity
echo -n "  Checking relay connectivity... "
if ./bin/agent-speaker relay info "$RELAY" > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC}"
else
    echo -e "${RED}✗ Cannot connect to $RELAY${NC}"
    exit 1
fi

echo ""
echo "=========================================="
echo "📨 E2E Messaging Tests"
echo "=========================================="

# Test 1: Send unencrypted message
run_test "Send plaintext message (alice → bob)" \
    "./bin/agent-speaker agent msg --from alice --to ${BOB_IDENTITY:-bob_e2e} --content '$TEST_MESSAGE' --relay $RELAY --encrypt=false"

# Test 2: Send encrypted message
run_test "Send encrypted message (alice → bob)" \
    "./bin/agent-speaker agent msg --from alice --to ${BOB_IDENTITY:-bob_e2e} --content '$TEST_MESSAGE_ENCRYPTED' --relay $RELAY --encrypt=true"

# Wait for relay propagation
echo ""
echo -e "${YELLOW}⏳ Waiting for relay propagation (3s)...${NC}"
sleep 3

echo ""
echo "=========================================="
echo "📥 Inbox Tests"
echo "=========================================="

# Test 3: Query events from relay (as alice - sent messages)
ALICE_NPUB=$(./bin/agent-speaker identity export --nickname alice 2>/dev/null | grep 'Npub:' | awk '{print $2}')
run_test "Query sent messages from relay" \
    "./bin/agent-speaker req --authors '$ALICE_NPUB' --kinds 30078 --relay $RELAY --limit 5 --json > /dev/null"

# Test 4: Check local message store
echo ""
echo -e "${BLUE}▶ Checking local message storage${NC}"
BEFORE_COUNT=$(./bin/agent-speaker history stats 2>/dev/null | grep "Total messages" | awk '{print $3}')
echo "  Current message count: ${BEFORE_COUNT:-0}"

echo ""
echo "=========================================="
echo "📊 History & Stats Tests"
echo "=========================================="

run_test "Message statistics" \
    "./bin/agent-speaker history stats | grep -q 'Total messages'"

run_test "Conversation history" \
    "./bin/agent-speaker history conversation --with ${BOB_IDENTITY:-bob_e2e} --limit 10 > /dev/null"

run_test "Message search" \
    "./bin/agent-speaker history search --query 'E2E' > /dev/null"

echo ""
echo "=========================================="
echo "🔐 Key Management Tests"
echo "=========================================="

run_test "Key generation" \
    "./bin/agent-speaker key generate > /dev/null"

run_test "Public key derivation" \
    "./bin/agent-speaker key public --sec nsec1g6ewhuj2ycx0sxwx7znhst0fhgd94nvljxe2hzvdq5lswxqqtk8qlnj0y3 > /dev/null"

echo ""
echo "=========================================="
echo "📝 Event Tests"
echo "=========================================="

run_test "Event creation (dry run)" \
    "./bin/agent-speaker event --kind 1 --content 'Test event' --sec nsec1g6ewhuj2ycx0sxwx7znhst0fhgd94nvljxe2hzvdq5lswxqqtk8qlnj0y3 --json > /dev/null"

echo ""
echo "=========================================="
echo "🔢 Encode/Decode Tests"
echo "=========================================="

run_test "Decode npub" \
    "./bin/agent-speaker decode -i npub1cndcuc26ngzk76j8mun2nx060ky2wdd6akagsx00s7q5mt4w7jdqfv9lw4 | grep -q 'Prefix: npub'"

run_test "Encode hex to npub" \
    "./bin/agent-speaker encode --prefix npub --hex c4db8e615a9a056f6a47df26a999fa7d88a735baedba8819ef87814daeaef49a 2>/dev/null | grep -q 'npub1'"

echo ""
echo "=========================================="
echo "📦 Outbox Tests"
echo "=========================================="

# Check if outbox file exists
if [ -f "$HOME/.agent-speaker/outbox.json" ]; then
    OUTBOX_COUNT=$(cat $HOME/.agent-speaker/outbox.json 2>/dev/null | grep -c '"status":' || echo "0")
    echo "  Outbox entries: $OUTBOX_COUNT"
else
    echo "  Outbox file not created yet (OK for fresh install)"
fi

echo ""
echo "=========================================="
echo "📊 TEST SUMMARY"
echo "=========================================="
echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "${RED}Failed: $TESTS_FAILED${NC}"

# Show final stats
echo ""
echo -e "${BLUE}Final state:${NC}"
./bin/agent-speaker history stats 2>/dev/null || echo "  (stats unavailable)"

if [ $TESTS_FAILED -eq 0 ]; then
    echo ""
    echo -e "${GREEN}🎉 All E2E tests passed!${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}⚠️  Some tests failed${NC}"
    exit 1
fi
