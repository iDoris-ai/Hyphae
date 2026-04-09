#!/bin/bash
set -e

echo "Building agent-speaker..."

# 清理构建目录
rm -rf build/*
mkdir -p build

# 复制 nak 代码
cp -r third_party/nak/* build/

# 复制我们的代码
cp agent.go build/
cp -r pkg build/

# 修改 build/go.mod
cd build

# 使用我们的 go.mod
cp ../go.mod ./go.mod

# 确保能引用 pkg/compress
mkdir -p github.com/jason/agent-speaker
cp -r ../pkg github.com/jason/agent-speaker/

# 修改 import
go mod edit -replace github.com/jason/agent-speaker/pkg/compress=./pkg/compress

# 下载依赖
go mod tidy 2>/dev/null || true

# 修改 main.go 添加 agent 命令
if ! grep -q "agentCmd" main.go; then
    sed -i '' '/profile,/a\            agentCmd,' main.go
fi

# 修改 agent.go 的 import
sed -i '' 's|github.com/fiatjaf/nak/pkg/compress|github.com/jason/agent-speaker/pkg/compress|g' agent.go

# 构建
go build -o ../bin/agent-speaker .

echo "✓ Build complete: bin/agent-speaker"
