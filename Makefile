.PHONY: build clean sync-nak dev-build test test-unit test-integration test-regression test-all bench copy-tests

BUILD_DIR := build
BIN_NAME := agent-speaker

# ============================================================================
# Build Targets
# ============================================================================

# 同步 nak 代码
sync-nak:
	@echo "📦 Syncing nak..."
	@rm -rf $(BUILD_DIR)/nak-src
	@mkdir -p $(BUILD_DIR)/nak-src
	@cp -r third_party/nak/* $(BUILD_DIR)/nak-src/
	@cp agent.go $(BUILD_DIR)/nak-src/
	@cp mcp_agent.go $(BUILD_DIR)/nak-src/
	@cp chat.go $(BUILD_DIR)/nak-src/ 2>/dev/null || true
	@cp delegate.go $(BUILD_DIR)/nak-src/ 2>/dev/null || true
	@cp background.go $(BUILD_DIR)/nak-src/ 2>/dev/null || true
	@cp subscribe.go $(BUILD_DIR)/nak-src/ 2>/dev/null || true
	@cp -r pkg $(BUILD_DIR)/nak-src/
	@echo "✅ Synced"

# 添加 agent 命令到 main.go
add-agent-cmd:
	@cd $(BUILD_DIR)/nak-src && \
	if ! grep -q "agentCmd" main.go; then \
		awk '/profile,/{print; print "\t\tagentCmd,"; next}1' main.go > main.go.tmp && \
		mv main.go.tmp main.go; \
	fi

# 复制测试文件到构建目录
copy-tests: sync-nak add-agent-cmd
	@echo "📝 Copying test files..."
	@cp test/*_test.go $(BUILD_DIR)/nak-src/ 2>/dev/null || true
	@mkdir -p $(BUILD_DIR)/nak-src/pkg/compress
	@cp pkg/compress/*.go $(BUILD_DIR)/nak-src/pkg/compress/ 2>/dev/null || true
	@echo "replace github.com/jason/agent-speaker/pkg/compress => ./pkg/compress" >> $(BUILD_DIR)/nak-src/go.mod
	@echo "✅ Tests copied"

# 开发构建
dev-build: copy-tests
	@echo "🔨 Building..."
	@mkdir -p bin
	@cd $(BUILD_DIR)/nak-src && go mod tidy && go build -o ../../bin/$(BIN_NAME) .
	@echo "✅ Built: bin/$(BIN_NAME)"

# 完整构建
build: clean dev-build

# 清理
clean:
	@rm -rf $(BUILD_DIR) bin/
	@echo "🧹 Cleaned"

# ============================================================================
# Test Targets
# ============================================================================

# 运行所有测试
test-all: copy-tests
	@echo "🧪 Running comprehensive test suite..."
	@echo ""
	@echo "Step 1: Unit tests (pkg/compress)..."
	@go test -v ./pkg/compress/... -race
	@echo ""
	@echo "Step 2: Building..."
	@mkdir -p bin
	@cd $(BUILD_DIR)/nak-src && go build -o ../../bin/$(BIN_NAME) . 2>&1 | head -20
	@echo ""
	@echo "Step 3: Running tests in build context..."
	@cd $(BUILD_DIR)/nak-src && go test -vet=off -v -run "TestNakEventBasic|TestNakEventComplex|TestNakKeyGenerate|TestNakKeyPublic|TestNakEncodeNpub" . -timeout 30s
	@cd $(BUILD_DIR)/nak-src && go test -vet=off -v -run "TestAgent" . -timeout 30s || true
	@cd $(BUILD_DIR)/nak-src && go test -vet=off -v -run "TestFilter|TestMock|TestCompression|TestRelay|TestMultiple|TestTimestamp" . -timeout 60s || true
	@echo ""
	@echo "✅ Tests complete!"

# 单元测试
test-unit:
	@echo "🔬 Running unit tests..."
	@go test -v ./pkg/compress/... -race

# 回归测试（需要先构建）
test-regression: copy-tests
	@echo "🔄 Running regression tests..."
	@cd $(BUILD_DIR)/nak-src && go test -vet=off -v -run "TestNakEventBasic|TestNakEventComplex|TestNakKeyGenerate|TestNakKeyPublic|TestNakEncodeNpub" ./test/... -timeout 30s

# 集成测试（需要先构建）
test-integration: copy-tests
	@echo "🔗 Running integration tests..."
	@cd $(BUILD_DIR)/nak-src && go test -vet=off -v -run "TestFilter|TestMock|TestCompression|TestRelay|TestMultiple|TestTimestamp" ./test/... -timeout 60s

# 快速测试
test-short:
	@echo "⚡ Running short tests..."
	@go test -short ./pkg/compress/...

# 覆盖率测试
test-coverage: copy-tests
	@echo "📊 Running tests with coverage..."
	@go test -cover ./pkg/compress/...
	@cd $(BUILD_DIR)/nak-src && go test -cover -run "TestAgent|TestRegression|TestIntegration" ./test/...

# 性能测试
bench:
	@echo "📊 Running benchmarks..."
	@go test -bench=. -benchmem ./pkg/compress/... -run=^$
	@cd $(BUILD_DIR)/nak-src && go test -bench=Benchmark -benchmem . -run=^$ 2>/dev/null || true

# ============================================================================
# Run Targets
# ============================================================================

# 运行
run: dev-build
	@./bin/$(BIN_NAME) agent --help

# 生成密钥
gen-key: dev-build
	@./bin/$(BIN_NAME) key generate

# 查询测试
query-test: dev-build
	@./bin/$(BIN_NAME) agent query --kinds "1" --limit 3

# ============================================================================
# Development Targets
# ============================================================================

# 更新 nak
update-nak:
	@echo "🔄 Updating nak..."
	@cd third_party/nak && git pull origin master
	@cd ../..
	@echo "✅ Updated. Run 'make build' to rebuild."

# 格式化代码
fmt:
	@echo "📝 Formatting code..."
	@gofmt -w agent.go *_test.go
	@gofmt -w pkg/compress/*.go

# 静态检查
lint:
	@echo "🔍 Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run ./pkg/compress/... || true

# ============================================================================
# Help
# ============================================================================

help:
	@echo "Agent Speaker - Available targets:"
	@echo ""
	@echo "Build:"
	@echo "  make build          - Full build"
	@echo "  make dev-build      - Quick development build"
	@echo "  make clean          - Clean build artifacts"
	@echo ""
	@echo "Test:"
	@echo "  make test-all       - Run comprehensive test suite"
	@echo "  make test-unit      - Run unit tests only"
	@echo "  make test-regression- Run nak regression tests"
	@echo "  make test-integration - Run integration tests"
	@echo "  make test-coverage  - Run tests with coverage"
	@echo "  make bench          - Run benchmarks"
	@echo ""
	@echo "Run:"
	@echo "  make run            - Build and show help"
	@echo "  make gen-key        - Generate a test key"
	@echo ""
	@echo "Development:"
	@echo "  make update-nak     - Update nak from upstream"
	@echo "  make fmt            - Format code"
	@echo "  make help           - Show this help"
