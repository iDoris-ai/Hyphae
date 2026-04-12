#!/bin/bash

echo "🎭 昵称系统测试"
echo "================"

# 清理之前的测试数据
rm -rf ~/.agent-speaker

# 1. Alice 创建身份
echo ""
echo "👩 Alice 创建身份..."
./bin/agent-speaker identity create --nickname alice --default

# 2. Bob 创建身份
echo ""
echo "👨 Bob 创建身份..."
./bin/agent-speaker identity create --nickname bob --default

# 3. 查看身份列表
echo ""
echo "👤 身份列表:"
./bin/agent-speaker identity list

# 4. Alice 添加 Bob 为联系人
echo ""
echo "📇 Alice 添加 Bob 为联系人..."
BOB_PUB=$(./bin/agent-speaker identity export --nickname bob 2>/dev/null | grep "Npub:" | awk '{print $2}')
./bin/agent-speaker contact add --nickname bob --npub "$BOB_PUB"

# 5. Bob 添加 Alice 为联系人
echo ""
echo "📇 Bob 添加 Alice 为联系人..."
ALICE_PUB=$(./bin/agent-speaker identity export --nickname alice 2>/dev/null | grep "Npub:" | awk '{print $2}')
./bin/agent-speaker contact add --nickname alice --npub "$ALICE_PUB"

# 6. 查看联系人列表
echo ""
echo "📋 Alice 的联系人:"
./bin/agent-speaker contact list

# 7. Alice 发送消息给 Bob（使用昵称！）
echo ""
echo "💬 Alice 发送消息给 Bob（使用昵称）..."
./bin/agent-speaker agent msg --from alice --to bob --content "嗨 Bob，帮我设计Logo！"

sleep 3

# 8. Bob 查看收件箱
echo ""
echo "📬 Bob 查看收件箱:"
./bin/agent-speaker agent inbox --as bob

echo ""
echo "✅ 测试完成！"
