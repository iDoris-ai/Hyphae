#!/bin/bash
# 完整的两角色测试脚本
set -e

YELLOW='\033[1;33m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

# 使用唯一的昵称避免冲突
TIMESTAMP=$(date +%s)
ALICE_NICK="alice_${TIMESTAMP}"
BOB_NICK="bob_${TIMESTAMP}"
TEST_DIR="$HOME/.agent-speaker-test-${TIMESTAMP}"
export AGENT_SPEAKER_CONFIG_DIR="$TEST_DIR"

cleanup() {
    echo -e "\n${YELLOW}清理测试环境...${NC}"
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}🎭 Agent Speaker 两角色完整测试${NC}"
echo -e "${BLUE}========================================${NC}"
echo -e "Alice: $ALICE_NICK"
echo -e "Bob: $BOB_NICK"
echo -e "测试目录: $TEST_DIR\n"

# 步骤 1-2: 创建身份
echo -e "${YELLOW}[步骤 1-2] 创建身份${NC}"
agent-speaker identity create --nickname "$ALICE_NICK" --default
agent-speaker identity create --nickname "$BOB_NICK"
echo -e "${GREEN}✅ 身份创建完成${NC}\n"

# 步骤 3: 查看身份
echo -e "${YELLOW}[步骤 3] 查看身份列表${NC}"
agent-speaker identity list
echo -e "${GREEN}✅ 身份列表完成${NC}\n"

# 步骤 4: 导出公钥
echo -e "${YELLOW}[步骤 4] 交换公钥${NC}"
ALICE_PUB=$(agent-speaker identity export --nickname "$ALICE_NICK" 2>/dev/null | grep "Npub:" | awk '{print $2}')
BOB_PUB=$(agent-speaker identity export --nickname "$BOB_NICK" 2>/dev/null | grep "Npub:" | awk '{print $2}')
echo "Alice 公钥: ${ALICE_PUB:0:30}..."
echo "Bob 公钥: ${BOB_PUB:0:30}..."
echo -e "${GREEN}✅ 公钥导出完成${NC}\n"

# 步骤 5: 添加联系人
echo -e "${YELLOW}[步骤 5] 添加联系人${NC}"
agent-speaker contact add --nickname "$BOB_NICK" --npub "$BOB_PUB"
agent-speaker contact add --nickname "$ALICE_NICK" --npub "$ALICE_PUB"
agent-speaker contact list
echo -e "${GREEN}✅ 联系人添加完成${NC}\n"

# 步骤 6: Alice 发送消息
echo -e "${YELLOW}[步骤 6] Alice 发送加密消息${NC}"
agent-speaker agent msg \
    --from "$ALICE_NICK" \
    --to "$BOB_NICK" \
    --content "嗨 Bob！我是 Alice。帮我设计 Logo，预算 500。" \
    --encrypt=true
echo -e "${GREEN}✅ 消息发送完成${NC}\n"
sleep 3

# 步骤 7: Bob 查看收件箱
echo -e "${YELLOW}[步骤 7] Bob 查看收件箱${NC}"
agent-speaker agent inbox --as "$BOB_NICK" --decrypt=true
echo -e "${GREEN}✅ 收件箱查看完成${NC}\n"

# 步骤 8: Bob 回复
echo -e "${YELLOW}[步骤 8] Bob 回复消息${NC}"
agent-speaker agent msg \
    --from "$BOB_NICK" \
    --to "$ALICE_NICK" \
    --content "收到！明早给你初稿。" \
    --encrypt=true
echo -e "${GREEN}✅ 回复发送完成${NC}\n"
sleep 3

# 步骤 9: Alice 查看回复
echo -e "${YELLOW}[步骤 9] Alice 查看回复${NC}"
agent-speaker agent inbox --as "$ALICE_NICK" --decrypt=true
echo -e "${GREEN}✅ 回复查看完成${NC}\n"

# 步骤 10: 历史记录
echo -e "${YELLOW}[步骤 10] 查看历史记录${NC}"
echo "--- 消息统计 ---"
agent-speaker history stats

echo -e "\n--- 对话记录 ---"
agent-speaker history conversation --with "$BOB_NICK" --limit 10

echo -e "\n--- 搜索 'Logo' ---"
agent-speaker history search --query "Logo"

echo -e "\n${BLUE}========================================${NC}"
echo -e "${GREEN}✅ 所有测试步骤完成！${NC}"
echo -e "${BLUE}========================================${NC}"
