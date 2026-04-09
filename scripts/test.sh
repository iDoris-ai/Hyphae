#!/bin/bash
# 运行所有测试

set -e

echo "🧪 Running tests..."

# 确保依赖
echo "📦 Installing dependencies..."
go mod tidy

# 运行单元测试
echo ""
echo "🔬 Running unit tests..."
go test -v ./pkg/compress/... -race

# 运行回归测试
echo ""
echo "🔄 Running regression tests..."
go test -v -run "TestEventBasic|TestEventComplex|TestKeyGenerate|TestKeyPublic|TestEncodeNpub" -timeout 30s

# 运行 agent 测试
echo ""
echo "🤖 Running agent tests..."
go test -v -run "TestAgent" -timeout 30s

# 运行集成测试
echo ""
echo "🔗 Running integration tests..."
go test -v -run "TestEndToEnd|TestAgentMessageFlow|TestFilterConstruction" -timeout 60s

# 运行所有测试（简短模式）
echo ""
echo "⚡ Running all tests (short mode)..."
go test -short ./... -timeout 120s

echo ""
echo "✅ All tests passed!"
