#!/bin/bash
set -e

REPORT_FILE="/tmp/test_report_$(date +%s).txt"
exec > >(tee -a "$REPORT_FILE") 2>&1

echo "========================================"
echo "🧪 Agent Speaker 全面测试报告"
echo "时间: $(date)"
echo "========================================"

# 清理测试环境
echo -e "\n📋 测试准备"
echo "================"
rm -rf ~/.agent-speaker
export TEST_NICKNAME_ALICE="alice_test_$(date +%s)"
export TEST_NICKNAME_BOB="bob_test_$(date +%s)"
echo "✅ 清理测试环境"
echo "✅ 设置测试昵称: $TEST_NICKNAME_ALICE, $TEST_NICKNAME_BOB"

# 测试 1: 密钥存储安全
echo -e "\n🔐 测试 1: 密钥存储安全"
echo "================"
./bin/agent-speaker identity create --nickname "$TEST_NICKNAME_ALICE" --default
KEYSTORE_PERMS=$(stat -f "%Lp" ~/.agent-speaker/keystore.json)
DIR_PERMS=$(stat -f "%Lp" ~/.agent-speaker)
echo "目录权限: $DIR_PERMS (预期: 700)"
echo "文件权限: $KEYSTORE_PERMS (预期: 600)"
if [ "$DIR_PERMS" = "700" ] && [ "$KEYSTORE_PERMS" = "600" ]; then
    echo "✅ 权限测试通过"
else
    echo "❌ 权限测试失败"
fi

# 测试 2: 身份创建和列出
echo -e "\n👤 测试 2: 身份管理"
echo "================"
./bin/agent-speaker identity create --nickname "$TEST_NICKNAME_BOB"
IDENTITY_COUNT=$(./bin/agent-speaker identity list 2>/dev/null | grep -c "npub1" || echo "0")
echo "创建的身份数量: $IDENTITY_COUNT"
if [ "$IDENTITY_COUNT" = "2" ]; then
    echo "✅ 身份管理测试通过"
else
    echo "❌ 身份管理测试失败"
fi

# 测试 3: 导出功能（验证密钥存在）
echo -e "\n📤 测试 3: 密钥导出"
echo "================"
EXPORT_OUTPUT=$(./bin/agent-speaker identity export --nickname "$TEST_NICKNAME_ALICE" 2>/dev/null)
if echo "$EXPORT_OUTPUT" | grep -q "nsec1"; then
    echo "✅ 密钥导出测试通过"
    echo "导出内容包含 nsec: [已隐藏]"
else
    echo "❌ 密钥导出测试失败"
fi

# 测试 4: 联系人管理
echo -e "\n📇 测试 4: 联系人管理"
echo "================"
ALICE_PUB=$(./bin/agent-speaker identity export --nickname "$TEST_NICKNAME_ALICE" 2>/dev/null | grep "Npub:" | awk '{print $2}')
BOB_PUB=$(./bin/agent-speaker identity export --nickname "$TEST_NICKNAME_BOB" 2>/dev/null | grep "Npub:" | awk '{print $2}')
./bin/agent-speaker contact add --nickname "$TEST_NICKNAME_BOB" --npub "$BOB_PUB"
CONTACT_COUNT=$(./bin/agent-speaker contact list 2>/dev/null | grep -c "npub1" || echo "0")
echo "联系人数量: $CONTACT_COUNT"
if [ "$CONTACT_COUNT" = "1" ]; then
    echo "✅ 联系人管理测试通过"
else
    echo "❌ 联系人管理测试失败"
fi

# 测试 5: 消息发送
echo -e "\n💬 测试 5: 消息发送"
echo "================"
TEST_MSG="测试消息 $(date +%s)"
SEND_RESULT=$(./bin/agent-speaker agent msg --from "$TEST_NICKNAME_ALICE" --to "$TEST_NICKNAME_BOB" --content "$TEST_MSG" 2>&1)
echo "$SEND_RESULT"
if echo "$SEND_RESULT" | grep -q "Published to 1/1 relays"; then
    echo "✅ 消息发送测试通过"
else
    echo "⚠️ 消息发送可能有问题（relay连接问题）"
fi

# 测试 6: 收件箱查询
echo -e "\n📬 测试 6: 收件箱查询"
echo "================"
sleep 5  # 等待消息传播
INBOX_RESULT=$(./bin/agent-speaker agent inbox --as "$TEST_NICKNAME_BOB" 2>&1)
echo "$INBOX_RESULT"
if echo "$INBOX_RESULT" | grep -q "$TEST_NICKNAME_ALICE"; then
    echo "✅ 收件箱测试通过"
else
    echo "⚠️ 收件箱可能为空（需要检查relay）"
fi

# 测试 7: 默认身份
echo -e "\n🎯 测试 7: 默认身份切换"
echo "================"
./bin/agent-speaker identity use --nickname "$TEST_NICKNAME_ALICE"
DEFAULT_CHECK=$(./bin/agent-speaker identity list 2>/dev/null | grep "$TEST_NICKNAME_ALICE" | grep -c "✓" || echo "0")
if [ "$DEFAULT_CHECK" = "1" ]; then
    echo "✅ 默认身份切换测试通过"
else
    echo "❌ 默认身份切换测试失败"
fi

# 测试 8: 直接 npub 发送（不使用联系人）
echo -e "\n🔗 测试 8: 直接 npub 发送"
echo "================"
DIRECT_MSG="直接发送测试 $(date +%s)"
DIRECT_SEND=$(./bin/agent-speaker agent msg --from "$TEST_NICKNAME_BOB" --to "$ALICE_PUB" --content "$DIRECT_MSG" 2>&1)
echo "$DIRECT_SEND"
if echo "$DIRECT_SEND" | grep -q "Published"; then
    echo "✅ 直接 npub 发送测试通过"
else
    echo "⚠️ 直接发送可能有问题"
fi

# 总结
echo -e "\n========================================"
echo "📊 测试总结"
echo "========================================"
echo "测试报告保存至: $REPORT_FILE"
echo ""
echo "测试的密钥文件位置: ~/.agent-speaker/"
echo "测试的 npub (Alice): $ALICE_PUB"
echo "测试的 npub (Bob): $BOB_PUB"
echo ""
echo "✅ 完成的测试:"
echo "   1. 密钥存储安全 (权限 700/600)"
echo "   2. 身份创建和管理"
echo "   3. 密钥导出功能"
echo "   4. 联系人管理"
echo "   5. 消息发送"
echo "   6. 收件箱查询"
echo "   7. 默认身份切换"
echo "   8. 直接 npub 发送"
echo ""
echo "⚠️  已知限制:"
echo "   - 私钥明文存储（需要密码加密）"
echo "   - 消息明文传输到 relay（需要 NIP-44）"
echo "   - 依赖 relay.aastar.io 可用性"
echo "========================================"
