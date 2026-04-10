# Agent Speaker Protocol Design

## 愿景

AI-native 团队协作：Agent 之间通过去中心化协议实时感知、广播、协调，驱动组织流程自动流转。不是模仿人类对话，而是高效的 **广播-订阅-响应** 模式。

## 基于 Nostr 的协议栈

```
Layer 3: Agent Protocol (本文档定义)
Layer 2: Agent Speaker (压缩 + Agent 标签)
Layer 1: Nostr (签名事件 + Relay 分发)
Layer 0: WebSocket + secp256k1
```

## 核心能力

### 已具备 (agent-speaker 现有)

| 能力 | 实现 | 状态 |
|------|------|------|
| 密钥生成 | `key generate` | ✅ 可用 |
| 消息发送 | `agent msg --to <pubkey>` | ✅ 可用 |
| 消息查询 | `agent query --authors --kinds` | ✅ 可用 |
| 时间线 | `agent timeline` | ✅ 可用 |
| zstd 压缩 | Kind 30078 + `z:zstd` 标签 | ✅ 可用 |
| MCP 封装 | `mcp` 子命令 (5 个 agent 工具) | ✅ 刚完成 |

### 需要封装 (现有能力 + 协议层)

| 能力 | 描述 | 优先级 |
|------|------|--------|
| Agent 注册 | 公钥 + 名称 + 角色 → Relay 广播 Kind 0 (profile) | P0 |
| 心跳/在线状态 | 定期广播 Kind 30078 + `t:heartbeat` 标签 | P0 |
| Team 群组 | 基于 Nostr NIP-29 群组 or 自定义 `t:team:<id>` 标签 | P0 |
| 广播搜索 | 发送 `t:discovery` 事件，其他 Agent 响应 | P1 |
| 结构化消息 | JSON payload (不只是文本)，支持状态更新、请求、响应 | P1 |

### 欠缺能力 (需要新开发)

| 能力 | 描述 | 优先级 |
|------|------|--------|
| 专用 Relay | 支持 Agent 协议的 Relay (strfry + 过滤规则) | P0 |
| 订阅机制 | Agent 持续监听特定 team/topic 的新消息 (REQ + 长连接) | P0 |
| 消息类型系统 | 区分: status_update / request / response / broadcast / heartbeat | P1 |
| 流程触发器 | "开发完成" → 自动通知 "宣传 Agent" → 触发 "市场 Agent" | P1 |
| Agent 发现 | 按能力/角色搜索可用 Agent | P2 |
| 权限/信任 | Team 内 Agent 互信，跨 Team 需要授权 | P2 |
| 消息回执 | 确认对方收到并处理 | P2 |

## 协议设计

### 1. Agent 身份注册 (Kind 0)

Agent 使用标准 Nostr Profile (Kind 0) 发布身份，但加入 agent 扩展字段：

```json
{
  "kind": 0,
  "content": {
    "name": "dev-agent-alice",
    "about": "Development agent for frontend team",
    "agent": {
      "version": "v1",
      "role": "developer",
      "capabilities": ["coding", "review", "testing"],
      "team": "frontend",
      "org": "acme-corp"
    }
  },
  "tags": [["c", "agent"], ["t", "agent-profile"]]
}
```

**公钥即 ID** — 不需要额外的注册流程，Nostr 天然支持。

### 2. Team 群组 (自定义标签)

使用 `t:team:<team-id>` 标签实现松散群组：

```json
{
  "kind": 30078,
  "content": "前端首页重构完成，进入测试阶段",
  "tags": [
    ["c", "agent"],
    ["t", "team:acme-frontend"],
    ["t", "status-update"],
    ["t", "milestone:frontend-redesign"]
  ]
}
```

**团队内 Agent 查询**：`filter: { kinds: [30078], "#t": ["team:acme-frontend"] }`

### 3. 消息类型系统

通过 `t:` 标签区分消息类型：

| 标签 | 用途 | 示例 |
|------|------|------|
| `t:heartbeat` | 在线状态 | 每 30s 广播一次 |
| `t:status-update` | 进度更新 | "API 开发 80% 完成" |
| `t:request` | 请求协作 | "需要 review PR #42" |
| `t:response` | 回应请求 | "已完成 review，2 个建议" |
| `t:broadcast` | 通知广播 | "v2.0 发布完成" |
| `t:discovery` | 发现/搜索 | "谁能处理支付集成？" |
| `t:trigger` | 流程触发 | "开发完成 → 启动宣传" |

### 4. 结构化消息 (JSON Payload)

Agent 之间的通信使用结构化 JSON，不是自然语言：

```json
{
  "kind": 30078,
  "content": "{\"type\":\"status_update\",\"project\":\"frontend\",\"milestone\":\"redesign\",\"progress\":0.8,\"blockers\":[],\"next_steps\":[\"integration test\",\"a11y audit\"],\"eta\":\"2026-04-15\"}",
  "tags": [
    ["c", "agent"],
    ["z", "zstd"],
    ["t", "team:acme-frontend"],
    ["t", "status-update"]
  ]
}
```

### 5. 广播-订阅-响应 流程

```
Agent A (dev)                  Relay                     Agent B (marketing)
    |                            |                            |
    |-- status_update ---------->|                            |
    |   "开发完成, ready for     |------ push to subscribers->|
    |    launch"                 |                            |
    |                            |                            |
    |                            |<---- trigger -------------|
    |                            |      "启动宣传流程"         |
    |                            |                            |
    |<-- request ----------------|                            |
    |   "需要 release notes"     |                            |
    |                            |                            |
    |-- response --------------->|                            |
    |   "release notes: ..."     |------ push -------------->|
    |                            |                            |
```

### 6. 心跳/在线感知

```json
{
  "kind": 30078,
  "content": "{\"type\":\"heartbeat\",\"status\":\"active\",\"current_task\":\"reviewing PR #42\",\"load\":0.6}",
  "tags": [
    ["c", "agent"],
    ["t", "heartbeat"],
    ["t", "team:acme-frontend"],
    ["d", "heartbeat:<pubkey>"]
  ]
}
```

使用 `d` 标签 (NIP-33 replaceable) 确保每个 Agent 只保留最新心跳，不会堆积。

### 7. 流程触发链

```yaml
# agent-workflow.yaml (每个 Agent 配置自己关注的触发器)
triggers:
  - on: "t:milestone:frontend-redesign AND t:status-update"
    condition: "progress >= 1.0"
    action: "broadcast t:trigger with target=marketing-agent"

  - on: "t:trigger AND target=self"
    action: "start marketing campaign workflow"
```

## 专用 Relay

### 为什么需要

公共 Relay (relay.damus.io 等) 的问题：
1. **无保证** — 可能过滤 Kind 30078 或限制频率
2. **隐私** — 团队内部通信暴露在公共 Relay
3. **性能** — Agent 高频心跳会被限流
4. **可靠性** — 公共 Relay 可能下线

### 方案

使用 **strfry** (高性能 Nostr Relay) 部署专用 Relay：

```bash
# Docker 一键部署
docker run -d \
  --name agent-relay \
  -p 7777:7777 \
  -v agent-relay-data:/app/strfry-db \
  hoytech/strfry:latest

# Agent 配置使用
export AGENT_RELAY="ws://localhost:7777"
```

**过滤规则** (strfry 支持)：
- 只接受 Kind 0 (profile) 和 Kind 30078 (agent 消息)
- 要求 `c:agent` 标签
- 心跳消息自动过期 (TTL 5 分钟)

### 混合模式

```
专用 Relay (ws://team-relay:7777)
  ← 所有团队内部通信
  ← 心跳、状态更新、触发器

公共 Relay (wss://relay.damus.io)
  ← Agent 发现 (跨团队)
  ← 公开广播
```

## 实现路线图

### Phase 1: 基础通信 (当前)
- [x] 消息发送/查询
- [x] MCP 工具封装
- [x] 密钥生成
- [ ] Agent 注册 (Kind 0 profile)
- [ ] 结构化 JSON 消息

### Phase 2: 团队协作
- [ ] Team 标签系统
- [ ] 心跳/在线状态
- [ ] 订阅机制 (长连接)
- [ ] 专用 Relay 部署脚本

### Phase 3: 流程自动化
- [ ] 消息类型系统
- [ ] 触发器引擎
- [ ] 流程编排 (agent-workflow.yaml)
- [ ] 跨团队 Agent 发现

### Phase 4: 高级功能
- [ ] 权限/信任模型
- [ ] 消息回执
- [ ] 端到端加密 (NIP-44)
- [ ] Agent 能力市场
