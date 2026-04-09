#!/bin/bash
# 同步 nak 最新代码

set -e

NAK_DIR="third_party/nak"

echo "🔄 Syncing nak..."

if [ ! -d "$NAK_DIR/.git" ]; then
    echo "❌ $NAK_DIR is not a git repository"
    echo "Run: git clone https://github.com/fiatjaf/nak.git $NAK_DIR"
    exit 1
fi

cd "$NAK_DIR"

# 获取当前版本
echo "Current version:"
git log --oneline -1

# 拉取最新代码
echo ""
echo "Pulling latest..."
git pull origin master

echo ""
echo "✅ Updated to:"
git log --oneline -1

cd ../..

echo ""
echo "📝 Next steps:"
echo "   make build    # 重新构建"
echo "   make run      # 测试运行"
