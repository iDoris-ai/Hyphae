#!/bin/bash
# E2E Tests for TUI feature
# Note: TUI tests are limited since they require interactive terminal

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
    local cmd="$2"
    
    echo -e "${BLUE}▶ $name${NC}"
    
    if eval "$cmd" > /tmp/tui_test.log 2>&1; then
        echo -e "${GREEN}  ✅ PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}  ❌ FAIL${NC}"
        echo -e "${RED}  Error: $(tail -3 /tmp/tui_test.log)${NC}"
        ((TESTS_FAILED++))
        return 1
    fi
}

echo "=========================================="
echo "🎨 TUI E2E Test Suite"
echo "=========================================="
echo "Time: $(date)"
echo ""

# Build
echo "🔨 Building..."
./build.sh > /dev/null 2>&1

# Check prerequisites
echo "📋 Checking prerequisites..."
if ! ./bin/agent-speaker identity list | grep -q "alice"; then
    echo -e "${YELLOW}Creating test identity 'alice'...${NC}"
    ./bin/agent-speaker identity create --nickname alice --default 2>/dev/null || true
fi

BOB_ID=$(./bin/agent-speaker contact list | grep "^bob" | head -1 | awk '{print $1}')
if [ -z "$BOB_ID" ]; then
    BOB_ID="bob_e2e"
fi
echo "  Using bob identity: $BOB_ID"

echo ""
echo "=========================================="
echo "🖥️  TUI Command Tests"
echo "=========================================="

# Test help commands (non-interactive)
run_test "TUI help command" \
    "./bin/agent-speaker tui --help"

run_test "Chat command help" \
    "./bin/agent-speaker tui chat --help"

run_test "Contacts command help" \
    "./bin/agent-speaker tui contacts --help"

echo ""
echo "=========================================="
echo "⚠️  Interactive TUI Tests"
echo "=========================================="
echo -e "${YELLOW}Note: Interactive TUI tests require manual verification${NC}"
echo ""
echo "To test manually:"
echo "  1. ./bin/agent-speaker tui contacts"
echo "     - Should show contact list with navigation"
echo "     - Use ↑/↓ to navigate, q to quit"
echo ""
echo "  2. ./bin/agent-speaker chat --with $BOB_ID"
echo "     - Should open chat interface"
echo "     - Type message and press enter"
echo "     - Use pgup/pgdn to scroll, esc to quit"
echo ""

echo "=========================================="
echo "📊 TEST SUMMARY"
echo "=========================================="
echo -e "${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "${RED}Failed: $TESTS_FAILED${NC}"

if [ $TESTS_FAILED -eq 0 ]; then
    echo ""
    echo -e "${GREEN}🎉 All TUI E2E tests passed!${NC}"
    echo -e "${YELLOW}⚠️  Remember to test interactive features manually${NC}"
    exit 0
else
    echo ""
    echo -e "${RED}⚠️  Some tests failed${NC}"
    exit 1
fi
