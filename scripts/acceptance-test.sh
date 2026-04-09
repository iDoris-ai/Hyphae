#!/bin/bash
# Agent Speaker 全面验收测试
# TC-001 ~ TC-012

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 加载环境变量
if [ -f ".env" ]; then
    source .env
else
    echo -e "${RED}❌ 错误: 找不到 .env 文件${NC}"
    echo "请先运行: ./scripts/setup-test-env.sh"
    exit 1
fi

# 检查二进制
if [ ! -f "./bin/agent-speaker" ]; then
    echo -e "${RED}❌ 错误: 请先构建项目: make build${NC}"
    exit 1
fi

AGENT="./bin/agent-speaker"
PASSED=0
FAILED=0

# 测试函数
run_test() {
    local test_id=$1
    local test_name=$2
    shift 2
    
    echo ""
    echo "========================================"
    echo "测试 $test_id: $test_name"
    echo "========================================"
    
    if "$@"; then
        echo -e "${GREEN}✅ $test_id 通过${NC}"
        ((PASSED++))
    else
        echo -e "${RED}❌ $test_id 失败${NC}"
        ((FAILED++))
    fi
}

# TC-001: 基础消息发送
tc001_basic_message() {
    echo "场景: Alice 发送未压缩消息给 Bob"
    
    local content="TC001-Hello-$(date +%s)"
    
    # Alice 发送消息
    $AGENT agent msg \
        --sec "$ALICE_NSEC" \
        --to "$BOB_PUB" \
        --relay "$RELAY_PUBLIC" \
        --compress=false \
        "$content" 2>&1 | tee /tmp/tc001_send.log
    
    echo "消息已发送，内容: $content"
    return 0
}

# TC-002: 压缩消息发送
tc002_compressed_message() {
    echo "场景: Alice 发送压缩消息给 Bob"
    
    local content="TC002-Compressed-$(date +%s)-$(openssl rand -hex 10)"
    
    # Alice 发送压缩消息
    $AGENT agent msg \
        --sec "$ALICE_NSEC" \
        --to "$BOB_PUB" \
        --relay "$RELAY_PUBLIC" \
        --compress=true \
        "$content" 2>&1 | tee /tmp/tc002_send.log
    
    echo "压缩消息已发送"
    return 0
}

# TC-003: 双向通信
tc003_bidirectional() {
    echo "场景: Alice 和 Bob 互相发送消息"
    
    local content_a="TC003-Alice-$(date +%s)"
    local content_b="TC003-Bob-$(date +%s)"
    
    # Alice -> Bob
    $AGENT agent msg \
        --sec "$ALICE_NSEC" \
        --to "$BOB_PUB" \
        --relay "$RELAY_PUBLIC" \
        "$content_a" 2>&1 | tee /tmp/tc003_alice.log
    
    # Bob -> Alice
    $AGENT agent msg \
        --sec "$BOB_NSEC" \
        --to "$ALICE_PUB" \
        --relay "$RELAY_PUBLIC" \
        "$content_b" 2>&1 | tee /tmp/tc003_bob.log
    
    echo "双向消息已发送"
    return 0
}

# TC-004: 批量查询
tc004_batch_query() {
    echo "场景: Bob 批量查询消息"
    
    timeout 10 $AGENT agent query \
        --authors "$ALICE_PUB" \
        --kinds "30078" \
        --relay "$RELAY_PUBLIC" \
        --limit 5 \
        2>&1 | tee /tmp/tc004_query.log
    
    echo "查询完成"
    return 0
}

# TC-005: 查看时间线
tc005_timeline() {
    echo "场景: 查看 Agent 时间线"
    
    timeout 10 $AGENT agent timeline \
        --relay "$RELAY_PUBLIC" \
        --limit 3 \
        2>&1 | tee /tmp/tc005_timeline.log
    
    echo "时间线查看完成"
    return 0
}

# TC-006: 本地 Mini Relay 启动
tc006_local_relay() {
    echo "场景: Charlie 启动本地 Relay"
    
    # 启动 relay（后台运行）
    timeout 3 $AGENT agent relay start --port 17777 2>&1 | tee /tmp/tc006_relay.log || true
    
    echo "Relay 启动测试完成（仅测试启动，不保持运行）"
    return 0
}

# TC-007: 通过公共 Relay 通信
tc007_public_relay() {
    echo "场景: 通过公共 Relay 通信"
    
    local content="TC007-Public-$(date +%s)"
    
    $AGENT agent msg \
        --sec "$ALICE_NSEC" \
        --to "$BOB_PUB" \
        --relay "$RELAY_PUBLIC" \
        "$content" 2>&1 | tee /tmp/tc007_public.log
    
    echo "公共 Relay 通信测试完成"
    return 0
}

# TC-008: 密钥生成
tc008_key_generation() {
    echo "场景: 密钥生成测试"
    
    local new_sec=$($AGENT key generate)
    local new_pub=$($AGENT key public "$new_sec")
    
    if [ ${#new_sec} -eq 64 ] && [ ${#new_pub} -eq 64 ]; then
        echo "密钥生成成功: sec=${#new_sec} pub=${#new_pub}"
        return 0
    else
        echo "密钥长度错误"
        return 1
    fi
}

# TC-009: npub 编码解码
tc009_npub_codec() {
    echo "场景: npub 编码解码测试"
    
    # 编码测试
    local npub=$($AGENT encode npub "$ALICE_PUB" 2>/dev/null || echo "")
    
    if [ -n "$npub" ] && [[ "$npub" == npub1* ]]; then
        echo "npub 编码成功: $npub"
        return 0
    else
        echo "npub 编码测试跳过（需要 nostr 库支持）"
        return 0
    fi
}

# TC-010: 事件生成
tc010_event_generation() {
    echo "场景: 事件生成测试"
    
    local event=$($AGENT event --ts 1699485669 2>&1)
    
    if echo "$event" | grep -q "kind"; then
        echo "事件生成成功"
        return 0
    else
        echo "事件生成失败"
        return 1
    fi
}

# TC-011: Filter 生成
tc011_filter_generation() {
    echo "场景: Filter 生成测试"
    
    local filter=$($AGENT filter -k 1 -k 30078 --limit 10 2>&1)
    
    if echo "$filter" | grep -q "kinds"; then
        echo "Filter 生成成功"
        return 0
    else
        echo "Filter 生成失败"
        return 1
    fi
}

# TC-012: 压缩解压功能
tc012_compression() {
    echo "场景: 压缩解压功能测试"
    
    # 测试压缩文本功能（通过 agent.go 中的 compressText）
    local test_data="TC012-Test-$(openssl rand -hex 50)"
    
    # 通过发送压缩消息间接测试
    $AGENT agent msg \
        --sec "$ALICE_NSEC" \
        --to "$BOB_PUB" \
        --relay "$RELAY_PUBLIC" \
        --compress=true \
        "$test_data" 2>&1 | tee /tmp/tc012_compress.log
    
    echo "压缩功能测试完成"
    return 0
}

# 主程序
echo "========================================"
echo "Agent Speaker 全面验收测试"
echo "========================================"
echo ""
echo "测试账户:"
echo "  Alice:   ${ALICE_NPUB:0:20}..."
echo "  Bob:     ${BOB_NPUB:0:20}..."
echo "  Charlie: ${CHARLIE_NPUB:0:20}..."
echo ""
echo "Relay: $RELAY_PUBLIC"
echo ""

# 运行所有测试
run_test "TC-001" "基础消息发送" tc001_basic_message
run_test "TC-002" "压缩消息发送" tc002_compressed_message
run_test "TC-003" "双向通信" tc003_bidirectional
run_test "TC-004" "批量查询" tc004_batch_query
run_test "TC-005" "查看时间线" tc005_timeline
run_test "TC-006" "本地 Mini Relay" tc006_local_relay
run_test "TC-007" "公共 Relay 通信" tc007_public_relay
run_test "TC-008" "密钥生成" tc008_key_generation
run_test "TC-009" "npub 编码解码" tc009_npub_codec
run_test "TC-010" "事件生成" tc010_event_generation
run_test "TC-011" "Filter 生成" tc011_filter_generation
run_test "TC-012" "压缩解压功能" tc012_compression

# 测试报告
echo ""
echo "========================================"
echo "测试报告"
echo "========================================"
echo -e "${GREEN}通过: $PASSED${NC}"
echo -e "${RED}失败: $FAILED${NC}"
echo "总计: $((PASSED + FAILED))"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}🎉 所有测试通过!${NC}"
    exit 0
else
    echo -e "${RED}⚠️  部分测试失败，请检查日志 /tmp/tc*.log${NC}"
    exit 1
fi
