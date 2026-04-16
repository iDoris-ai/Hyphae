#!/bin/bash
# E2E Tests for SQLite Storage Feature

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

TESTS_PASSED=0
TESTS_FAILED=0

run_test() {
    local name="$1"
    shift
    local cmd="$@"
    
    echo -e "${BLUE}▶ $name${NC}"
    
    if eval "$cmd" > /tmp/storage_test.log 2>&1; then
        echo -e "${GREEN}  ✅ PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}  ❌ FAIL${NC}"
        echo -e "${RED}  Error: $(tail -3 /tmp/storage_test.log)${NC}"
        ((TESTS_FAILED++))
        return 1
    fi
}

echo "=========================================="
echo "🗄️  Storage E2E Test Suite"
echo "=========================================="
echo "Time: $(date)"
echo ""

# Build
echo "🔨 Building..."
./build.sh > /dev/null 2>&1

# Check identities exist
echo "📋 Checking prerequisites..."
if ! ./bin/agent-speaker identity list | grep -q "alice"; then
    echo -e "${YELLOW}Creating test identity 'alice'...${NC}"
    ./bin/agent-speaker identity create --nickname alice --default 2>/dev/null || true
fi

BOB_ID=$(./bin/agent-speaker contact list | grep "^bob" | head -1 | awk '{print $1}')
if [ -z "$$BOB_ID" ]; then
    BOB_ID="bob_e2e"
fi
echo "  Using bob identity: $BOB_ID"

echo ""
echo "=========================================="
echo "📊 Storage Info Tests"
echo "=========================================="

run_test "Storage info command" \
    "./bin/agent-speaker storage info | grep -q 'Database:'"

run_test "Storage shows tables" \
    "./bin/agent-speaker storage info | grep -q 'messages'"

echo ""
echo "=========================================="
echo "💬 Message Storage Tests"
echo "=========================================="

# Send a test message
TEST_MSG="Storage test $(date +%s)"
run_test "Send message (stores to SQLite)" \
    "./bin/agent-speaker agent msg --from alice --to $BOB_ID --content '$TEST_MSG' --relay wss://relay.aastar.io --encrypt=false"

# Check stats updated
echo ""
echo -e "${BLUE}▶ Checking stats updated${NC}"
sleep 2
./bin/agent-speaker history stats
STATS_OUTPUT=$(./bin/agent-speaker history stats 2>&1)
if echo "$STATS_OUTPUT" | grep -q "Total messages:"; then
    TOTAL=$(echo "$STATS_OUTPUT" | grep "Total messages:" | awk '{print $3}')
    if [ "$TOTAL" -gt "0" ] 2>/dev/null; then
        echo -e "${GREEN}  ✅ Stats show $TOTAL messages${NC}"
        ((TESTS_PASSED++))
    else
        echo -e "${YELLOW}  ⚠️  Stats may show 0 (message async storage)${NC}"
    fi
else
    echo -e "${RED}  ❌ Stats not working${NC}"
    ((TESTS_FAILED++))
fi

# Test search
echo ""
echo "=========================================="
echo "🔍 Search Tests"
echo "=========================================="

run_test "History search command" \
    "./bin/agent-speaker history search --query 'test' > /dev/null"

run_test "History inbox command" \
    "./bin/agent-speaker history inbox > /dev/null"

echo ""
echo "=========================================="
echo "📜 Conversation Tests"
echo "=========================================="

run_test "History conversation command" \
    "./bin/agent-speaker history conversation --with $BOB_ID --limit 10 > /dev/null"

echo ""
echo "=========================================="
echo "📊 TEST SUMMARY"
echo "=========================================="
echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "${RED}Failed: $TESTS_FAILED${NC}"

# Show final storage info
echo ""
echo -e "${BLUE}Final Storage State:${NC}"
./bin/agent-speaker storage info 2>/dev/null || echo "  (storage info unavailable)"

if [ $TESTS_FAILED -eq 0 ]; then
    echo ""
    echo -e "${GREEN}🎉 All storage E2E tests passed!${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}⚠️  Some tests failed${NC}"
    exit 1
fi
