#!/bin/bash
# 设置测试环境 - 生成测试账户

set -e

echo "🚀 设置 Hyphae 测试环境"
echo ""

# 检查 hyphae 是否已构建
if [ ! -f "./bin/hyphae" ]; then
    echo "❌ 请先构建项目: make build"
    exit 1
fi

# 创建 .env 文件
ENV_FILE=".env"

if [ -f "$ENV_FILE" ] && [ "$1" != "--force" ]; then
    echo "⚠️  .env 文件已存在，使用 --force 覆盖"
    echo "    或手动编辑 $ENV_FILE"
    exit 0
fi

echo "📝 生成测试账户..."

# 生成 Alice 密钥
echo "  - 生成 Alice 账户..."
ALICE_SEC=$(./bin/hyphae key generate)
ALICE_PUB=$(./bin/hyphae key public "$ALICE_SEC")
ALICE_NPUB=$(./bin/hyphae encode npub "$ALICE_PUB" 2>/dev/null || echo "npub1alice$(openssl rand -hex 20 | cut -c1-50)")

# 生成 Bob 密钥
echo "  - 生成 Bob 账户..."
BOB_SEC=$(./bin/hyphae key generate)
BOB_PUB=$(./bin/hyphae key public "$BOB_SEC")
BOB_NPUB=$(./bin/hyphae encode npub "$BOB_PUB" 2>/dev/null || echo "npub1bob$(openssl rand -hex 20 | cut -c1-50)")

# 生成 Charlie 密钥
echo "  - 生成 Charlie 账户..."
CHARLIE_SEC=$(./bin/hyphae key generate)
CHARLIE_PUB=$(./bin/hyphae key public "$CHARLIE_SEC")
CHARLIE_NPUB=$(./bin/hyphae encode npub "$CHARLIE_PUB" 2>/dev/null || echo "npub1charlie$(openssl rand -hex 20 | cut -c1-50)")

# 写入 .env 文件
cat > "$ENV_FILE" << EOF
# ============================================
# Hyphae 测试环境配置
# 生成时间: $(date)
# ============================================

# 测试用户 A: Alice (发起人)
ALICE_NSEC=$ALICE_SEC
ALICE_PUB=$ALICE_PUB
ALICE_NPUB=$ALICE_NPUB
ALICE_NAME=Alice
ALICE_ROLE=发起人

# 测试用户 B: Bob (接收人)
BOB_NSEC=$BOB_SEC
BOB_PUB=$BOB_PUB
BOB_NPUB=$BOB_NPUB
BOB_NAME=Bob
BOB_ROLE=接收人

# 测试用户 C: Charlie (观察者)
CHARLIE_NSEC=$CHARLIE_SEC
CHARLIE_PUB=$CHARLIE_PUB
CHARLIE_NPUB=$CHARLIE_NPUB
CHARLIE_NAME=Charlie
CHARLIE_ROLE=观察者

# Relay 配置
RELAY_PUBLIC=wss://relay.damus.io
RELAY_BACKUP=wss://nos.lol
RELAY_LOCAL=ws://localhost:7777

# 测试配置
TEST_TIMEOUT=30
TEST_COMPRESS=true
LOG_LEVEL=info
EOF

# 设置权限
chmod 600 "$ENV_FILE"

echo ""
echo "✅ 测试环境配置完成!"
echo ""
echo "配置文件: $ENV_FILE"
echo ""
echo "测试账户:"
echo "  Alice:   $ALICE_NPUB"
echo "  Bob:     $BOB_NPUB"
echo "  Charlie: $CHARLIE_NPUB"
echo ""
echo "⚠️  重要: .env 文件包含私钥，请勿提交到 Git!"
echo "    已自动添加到 .gitignore"

# 确保 .gitignore 包含 .env
if ! grep -q "^\.env$" .gitignore 2>/dev/null; then
    echo ".env" >> .gitignore
    echo "    已更新 .gitignore"
fi
