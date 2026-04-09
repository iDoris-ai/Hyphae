# Agent Nostr CLI 架构设计

## 核心概念

### 两种架构方案

**方案 A: 标准 Nostr (Phase 1)**
```
Alice-Client ──▶ Public-Relay ◀── Bob-Client
                     │
               ┌─────┴─────┐
               ▼           ▼
         Alice-Mini    Bob-Mini
         (本地缓存)    (本地缓存)
```

- Mini Relay 只缓存本地数据
- 真正消息存储在 Public Relay
- 100% 兼容 Nostr 协议

**方案 B: 扩展协议 (Phase 2)**
- 向后兼容: 标准客户端可读
- 扩展 kind:30078+ 用于 Agent 通信
- DHT 发现机制

## 技术栈

| 组件 | 选择 | 理由 |
|------|------|------|
| 核心 CLI | nak (Go) | MIT/Unlicense, 功能最全 |
| 压缩算法 | zstd | 压缩率高，Go 原生支持 |
| Relay | strfry (C++) | Apache 2.0, 高性能 |
| 数据格式 | JSON + zstd | 标准 + 压缩 |

## Agent 专用通道

```json
{
  "kind": 30078,
  "tags": [["c", "agent-v1"], ["z", "zstd"]],
  "content": "<zstd-compressed-data>"
}
```

## 目录结构

```
github.com/fiatjaf/nak/
├── cmd/
│   ├── key.go          # 已有: 密钥管理
│   ├── event.go        # 已有: 发布事件
│   ├── req.go          # 已有: 查询事件
│   ├── agent/          # 🆕 新增: Agent 功能
│   │   ├── msg.go      # 压缩私信
│   │   ├── query.go    # 批量查询
│   │   ├── relay.go    # 启动 mini relay
│   │   └── sync.go     # 离线同步
│   └── bootstrap/      # 🆕 新增: DHT 发现 (Phase 2)
├── pkg/
│   ├── compress/       # 🆕 zstd 压缩
│   └── relay/          # 已有: Relay 连接
└── main.go
```
