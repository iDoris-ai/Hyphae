#!/bin/bash
set -e  # 遇到错误立即退出

# 清理函数
cleanup() {
    rm -f /tmp/alice_* /tmp/bob_*
}
trap cleanup EXIT

# 去除 ANSI 颜色码的函数
strip_ansi() {
    sed 's/\x1b\[[0-9;]*m//g'
}

echo "🎭 模拟两个同事通信测试"
echo "========================"

# 1. 生成 Alice 的密钥
echo ""
echo "👩 Alice 生成密钥..."
KEY_OUTPUT=$(./bin/agent-speaker key generate 2>/dev/null | strip_ansi)
ALICE_SEC=$(echo "$KEY_OUTPUT" | grep "nsec1" | head -1 | awk '{print $NF}')
ALICE_PUB=$(echo "$KEY_OUTPUT" | grep "npub1" | head -1 | awk '{print $NF}')
echo "   Alice 公钥: $ALICE_PUB"
echo "   Alice 私钥: ${ALICE_SEC:0:20}..."

# 2. 生成 Bob 的密钥
echo ""
echo "👨 Bob 生成密钥..."
KEY_OUTPUT=$(./bin/agent-speaker key generate 2>/dev/null | strip_ansi)
BOB_SEC=$(echo "$KEY_OUTPUT" | grep "nsec1" | head -1 | awk '{print $NF}')
BOB_PUB=$(echo "$KEY_OUTPUT" | grep "npub1" | head -1 | awk '{print $NF}')
echo "   Bob 公钥: $BOB_PUB"
echo "   Bob 私钥: ${BOB_SEC:0:20}..."

# 3. Alice 发送消息给 Bob
echo ""
echo "📤 Alice 发送消息给 Bob..."
./bin/agent-speaker agent msg \
  --sec "$ALICE_SEC" \
  --to "$BOB_PUB" \
  --content "嗨Bob帮我设计Logo预算500明天要"

# 4. 等待消息传播
echo ""
echo "⏳ 等待消息传播（5秒）..."
sleep 5

# 5. Bob 查询消息
echo ""
echo "📥 Bob 查看收到的消息..."
./bin/agent-speaker agent query \
  --authors "$ALICE_PUB" \
  --decompress 2>&1 | head -40

# 6. Bob 回复 Alice
echo ""
echo "📤 Bob 回复 Alice..."
./bin/agent-speaker agent msg \
  --sec "$BOB_SEC" \
  --to "$ALICE_PUB" \
  --content "收到今晚做明早给初稿"

# 7. 等待回复传播
echo ""
echo "⏳ 等待回复传播（5秒）..."
sleep 5

# 8. Alice 查询发给她的消息
echo ""
echo "👩 Alice 查看 Bob 的回复..."
./bin/agent-speaker agent query \
  --authors "$BOB_PUB" \
  --decompress 2>&1 | head -40

echo ""
echo "✅ 通信测试完成！"
