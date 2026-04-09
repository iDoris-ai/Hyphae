# Agent Nostr CLI 开发计划

## Phase 1: 标准兼容 (1-2 周)

### Step 1: Fork & 基础框架 (1 天)
```bash
# Fork nak
git clone https://github.com/fiatjaf/nak.git agent-nostr-cli
cd agent-nostr-cli

# 添加压缩模块
go get github.com/klauspost/compress/zstd
```

### Step 2: 实现压缩模块 (2 天)
```go
// pkg/compress/zstd.go
func Compress(data []byte) ([]byte, error)
func Decompress(data []byte) ([]byte, error)
```

### Step 3: Agent 命令扩展 (2 天)
- `agent msg` - 发送压缩消息
- `agent query` - 批量查询
- `agent relay` - relay 管理

### Step 4: Relay 自动化脚本 (1 天)
```bash
# scripts/relay-up.sh
# 一键启动本地 relay，自动配置白名单
```

### Step 5: 集成测试 (1-2 天)
- 压缩/解压测试
- 跨 relay 查询测试
- 性能基准测试

## Phase 2: 扩展协议 (待定)

- DHT 发现机制
- 发送方存储模式
- 真正的去中心化

## 交付物

| 模块 | 输出 |
|------|------|
| agent-cli | 可执行文件 + 配置文件 |
| relay-docker | docker-compose.yml |
| sdk | Go module，其他 Agent 可导入 |

## 开源协议

MIT（与 nak 保持一致）
