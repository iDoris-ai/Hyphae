#!/bin/bash
# 运行所有测试（包括构建后的集成测试）

set -e

echo "🧪 Running comprehensive test suite..."

# ============================================================================
# 1. 单元测试 - 不依赖 nak 的包
# ============================================================================
echo ""
echo "📦 Step 1: Unit tests (pkg/compress)..."
go test -v ./pkg/compress/... -race

# ============================================================================
# 2. 构建项目
# ============================================================================
echo ""
echo "🔨 Step 2: Building project..."
make dev-build

# ============================================================================
# 3. 在构建后的代码中运行测试
# ============================================================================
echo ""
echo "🧪 Step 3: Running tests in build context..."
cd build/nak-src

# 运行 nak 回归测试
echo "  - Regression tests..."
go test -v -run "TestEventBasic|TestEventComplex|TestKeyGenerate|TestKeyPublic|TestEncodeNpub" . -timeout 30s

# 运行 agent 测试
echo "  - Agent tests..."
go test -v -run "TestAgent" . -timeout 30s

# 运行集成测试
echo "  - Integration tests..."
go test -v -run "TestEndToEnd|TestAgentMessageFlow|TestFilter|TestMock" . -timeout 60s

cd ../..

# ============================================================================
# 4. 性能测试
# ============================================================================
echo ""
echo "📊 Step 4: Benchmarks..."
go test -bench=. -benchmem ./pkg/compress/... -run=^$

echo ""
echo "✅ All tests passed!"
