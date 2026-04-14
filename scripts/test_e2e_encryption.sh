#!/bin/bash
set -e

echo "🔐 E2E 加密测试"
echo "================"

# 清理测试环境
rm -rf ~/.agent-speaker

# 创建两个测试身份
./bin/agent-speaker identity create --nickname alice_e2e --default
./bin/agent-speaker identity create --nickname bob_e2e

# 获取公钥
ALICE_PUB=$(./bin/agent-speaker identity export --nickname alice_e2e 2>/dev/null | grep "Npub:" | awk '{print $2}')
BOB_PUB=$(./bin/agent-speaker identity export --nickname bob_e2e 2>/dev/null | grep "Npub:" | awk '{print $2}')

# 添加联系人
./bin/agent-speaker contact add --nickname bob_e2e --npub "$BOB_PUB"

# 测试 1: 发送加密消息（默认开启加密）
echo ""
echo "💬 测试 1: 发送加密消息..."
./bin/agent-speaker agent msg \
  --from alice_e2e \
  --to bob_e2e \
  --content "这是加密消息 - Secret Message!" \
  --encrypt=true

sleep 3

# 测试 2: 收件箱自动解密
echo ""
echo "📬 测试 2: 收件箱自动解密..."
./bin/agent-speaker agent inbox --as bob_e2e --decrypt=true

# 测试 3: 发送明文消息（对比测试）
echo ""
echo "💬 测试 3: 发送明文消息（对比）..."
./bin/agent-speaker agent msg \
  --from alice_e2e \
  --to bob_e2e \
  --content "这是明文消息 - Plain Text" \
  --encrypt=false

sleep 3

# 测试 4: 收件箱显示混合消息
echo ""
echo "📬 测试 4: 收件箱显示混合消息..."
./bin/agent-speaker agent inbox --as bob_e2e --decrypt=true

echo ""
echo "✅ E2E 加密测试完成!"
