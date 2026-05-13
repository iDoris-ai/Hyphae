# Agent-Speaker Protocol V2

> 取代 `docs/03-development-plan.md`（已删除）
> 协同文档：[`milestones/roadmap-v2.md`](milestones/roadmap-v2.md)、[`02-architecture-design.md`](02-architecture-design.md)
> 最后更新：2026-05-13

---

## 0. 总览

Agent-Speaker V2 从"单 relay 加密 IM"演进为"**去中心化 Agent 协作网络**"：

- 任何人可以**自部署 relay**，加入网络
- Relay 之间通过**邀请邻居 + 1对1 通道**互联
- 跨 relay 转发支付 **AAstar Point**（ERC-20），构成激励层
- 客户端通过统一 **JSON 行为协议** 发起 register/publish/inquire/tip/subscribe 等操作
- **漂流瓶**让无强支付能力的用户也能通过"消息嵌入主题向量 + 接收方本地匹配"低成本触达
- 个人隐私数据**字段级控制**（公开 / 仅匹配 / 私密），**本地 AES 加密落盘**

---

## 1. 三层协议结构

```
╔══════════════════════════════════════════════════════════╗
║  L3  应用行为协议 (JSON-in-Nostr-Event-Content)           ║
║      register / publish / inquire / tip / subscribe       ║
║      drifting-bottle (带主题向量)                         ║
╠══════════════════════════════════════════════════════════╣
║  L2  中继路由协议  ⚠️ 标准 Nostr 没有，需自建             ║
║   ┌────────────────────────────────────────────────┐      ║
║   │ 邻居:  每 relay 6 邀请名额                     │      ║
║   │        + 1对1 长连接通道(治理后期再做)        │      ║
║   │ 跨relay forwarding: TTL 跳数 + AAstar Point 付费│     ║
║   │ 漂流瓶: 20% 冗余带宽随机转发,本地向量匹配      │      ║
║   └────────────────────────────────────────────────┘      ║
╠══════════════════════════════════════════════════════════╣
║  L1  标准 Nostr (兼容层,不动)                             ║
║      WebSocket / Event / Sig / NIP-44 / NIP-11            ║
╚══════════════════════════════════════════════════════════╝
```

**核心原则**：
- L1 保持标准，普通 Nostr 客户端可读基础事件
- L2 是我们在 relay 软件里的扩展，不破坏 L1 兼容
- L3 是事件 `content` 字段内的 JSON 结构，标准客户端见到也不会出错

---

## 2. 架构对比

### V1（当前 — main + 待合并 PR）

```
                     User (alice / bob / charlie)
                             │
              ┌──────────────┴──────────────┐
              ▼                             ▼
       agent-speaker CLI/TUI         agent-speaker daemon
       ┌──────────────────────┐      ┌─────────────────────┐
       │ key / event / req    │      │ outbox 重试         │
       │ identity / contact   │      │ inbox 拉取          │
       │ agent msg / history  │      │ 桌面通知            │
       │ tui chat  (PR #4)    │      │                     │
       │ group chat (PR #5)   │      │                     │
       └──────────┬───────────┘      └──────────┬──────────┘
                  │                             │
                  └──────────────┬──────────────┘
                                 ▼
                  ┌──────────────────────────────┐
                  │  ~/.agent-speaker/           │
                  │   ├─ keystore.json (AES)     │  仅 nsec 加密
                  │   └─ messages.db (SQLite)    │
                  └──────────────┬───────────────┘
                                 │ WebSocket
                                 │ NIP-44 + zstd
                                 ▼
                  ┌──────────────────────────────┐
                  │  wss://relay.aastar.io       │  单 relay
                  │  (标准 Nostr，软件未知)      │
                  └──────────────────────────────┘
```

**V1 痛点**：单点故障 / Relay 互不通 / 无 profile/发现 / 无支付 / 隐私数据无字段级控制

### V2（目标）

```
                         User
                          │
                ┌─────────┴─────────┐
                ▼                   ▼
        agent-speaker CLI/TUI     agent-speaker daemon
        ┌───────────────┐         ┌──────────────────┐
        │ 现有命令      │         │ outbox / inbox   │
        │ + register    │         │ + 漂流瓶接收     │
        │ + discover    │         │ + 本地向量匹配   │
        │ + 漂流瓶发送  │         │ + 自动回复       │
        │ + tip / 询价  │         │                  │
        └───────┬───────┘         └─────────┬────────┘
                │                           │
                └─────────────┬─────────────┘
                              ▼
        ┌─────────────────────────────────────────────┐
        │  ~/.agent-speaker/ (本地数据全部加密落盘)   │
        │   ├─ keystore.json    (AES-256,密码)        │
        │   ├─ profile.enc      (AES-256,密码)        │
        │   │    ├─ public 字段     → 上 relay 花名册 │
        │   │    ├─ match-only 字段 → 只出向量摘要    │
        │   │    └─ private 字段    → 永不出本机      │
        │   ├─ messages.db                            │
        │   └─ profile_vector.bin (运行时内存计算)    │
        └────────────────┬────────────────────────────┘
                         │
   ╔═════════════════════▼═════════════════════════════╗
   ║          L3 / L2 / L1  三层协议栈                 ║
   ╚════════════════════╤══════════════════════════════╝
                        │
       ┌────────────────┼────────────────────────┐
       ▼                ▼                        ▼
 ┌──────────┐     ┌──────────┐            ┌──────────┐
 │ Relay A  │◀───▶│ Relay B  │◀── 1对1 ──▶│ Relay X  │
 │ khatru   │ 6邀请│ khatru   │            │ khatru   │
 │ + L2插件 │ 邻居 │ + L2插件 │            │ + L2插件 │
 └────┬─────┘     └────┬─────┘            └──────────┘
      │ 花名册          │
      ├─▶ Relay C       ├─▶ Relay E
      ├─▶ Relay D       └─▶ ...
      └─▶ ...

 花名册:        Kind 0 扩展，每个 relay 自存
 跨 relay 费用:  AAstar Point (ERC-20)
 Gasless:       SuperPaymaster 代付
 身份:          AirAccount (来自 AAstar)
```

---

## 3. 关键差异

| 维度 | V1 | V2 |
|------|----|----|
| Relay 数量 | 1 个（公共） | N 个（任何人可部署） |
| Relay 之间 | 不通 | 邀请邻居 + 跨 relay 付费转发 |
| Relay 软件 | 未知 | fork khatru，加 L2 插件 |
| Profile | 无 | 公开 / 仅匹配 / 私密 三层 |
| 本地加密对象 | 仅 nsec + 传输消息 | + profile 全本地 AES |
| 发现机制 | 无 | 花名册 + discover + 漂流瓶 |
| 支付 | 无 | AAstar Point 跨 relay 转发费 |
| 协作行为 | 仅 msg | register/publish/inquire/tip/subscribe |
| Token 系统 | 无 | AAstar Point + SuperPaymaster gasless |

---

## 4. L3 应用行为协议（JSON Schema）

事件结构（基于 Nostr Kind 30078）：

```json
{
  "kind": 30078,
  "tags": [
    ["c", "agent-v2"],
    ["b", "<behavior>"],
    ["z", "zstd"]
  ],
  "content": "<zstd(JSON 行为体)>"
}
```

### 4.1 behaviors 一览

| behavior | 用途 | content schema 摘要 |
|----------|------|--------------------|
| `register` | 在 relay 花名册注册自己 | `{ name, mode, tags, structured? }` |
| `publish` | 发广播消息 | `{ body, broadcast: { fee, radius } }` |
| `inquire` | 询价/询能力 | `{ target, capability, params }` |
| `tip` | 打赏 | `{ to, amount, ref_event? }` |
| `subscribe` | 关注某 npub 或标签 | `{ filter }` |
| `drifting-bottle` | 漂流瓶 | `{ topic_vec, content, ttl, fee, threshold }` |

### 4.2 注册三种模式

| mode | content |
|------|---------|
| 简单 | `{ name: "alice", mode: "simple" }` |
| 标签 | `{ name: "alice", mode: "tagged", tags: ["dev","go","AI"] }` |
| 结构化 | 完整 schema，含 capabilities / availability / rating 等（沿用 [roadmap-v2.md](milestones/roadmap-v2.md) M2 设计） |

---

## 5. L2 中继路由协议

### 5.1 邻居模型（邀请制）

```
Relay-A (创世)
  └── 持有 6 张邀请券
       ├── invite → Relay-B   B 自动成为 A 的邻居（双向）
       ├── invite → Relay-C
       └── ...
独立部署的 Relay-X：
  - 不在任何人邀请链上
  - 想成为邻居只能反向邀请别人接受、或建立 1对1 通道
```

**邀请券 = ERC-721 NFT**（M2.5 决定，可后调整），由邻居网络治理合约发放。

### 5.2 1对1 长连接通道

两个 relay 直接建 WebSocket 长连接，互相代发消息。

**当前阶段**：先跑通技术（双向 WebSocket + 简单 forwarding），治理/staking 延后。

### 5.3 跨 relay 转发（小世界路由）

```
Layer 0:  发送方 → 自己 relay        (无费用)
Layer 1:  本 relay → 6 个邻居        (基础费 × 6)
Layer 2:  每邻居 → 它的 6 个邻居     (费 × 36)
Layer 3:  ...                         (费 × 216)
```

事件携带 TTL 标签：
```
["ttl", "3"]       # 最多再跳 3 层
["fee_paid", "..."]  # 已支付的 AAstar Point 数量及 receipt
```

每跳 relay：
1. 验证 `fee_paid` 是否含本 relay 应得份额
2. 扣 1 TTL，转发给自己 6 个邻居
3. 若 TTL == 0 则丢弃

### 5.4 漂流瓶（P1 优先级）

**核心思想**：消息**自带主题向量**，接收方**本地用自己 profile 向量匹配**。
不是把发送方 profile 向量化散发出去 → 避免暴露发送方隐私。

```
发送方 (A):
  写一段内容 (e.g. 讨论 AI 创业)
  本地对内容做向量化 → embedding_summary
  组装漂流瓶事件 + 支付基础费 (远低于广播)
  扔给本 relay

中间 relay (B/C/D...):
  把 20% 的冗余带宽用于随机转发漂流瓶
  每跳扣 1 TTL，无需付费给下一跳 (基础费已 cover)

接收方 (Z):
  收到漂流瓶 → 提取 topic_vec
  本地用 profile_vector 计算余弦相似度
  > 自己设定的 threshold (默认 0.7) → 落地推送
  ≤ threshold → 再随机扔回网络 (若 TTL > 0)
```

漂流瓶事件结构：
```json
{
  "kind": 30079,
  "tags": [
    ["b", "drifting-bottle"],
    ["vec", "<base64 embedding>"],
    ["topic", "AI", "创业"],
    ["ttl", "10"],
    ["fee", "10"],
    ["threshold_hint", "0.6"]
  ],
  "content": "<zstd(实际正文)>"
}
```

---

## 6. 字段级隐私模型

Profile 字段分三类：

| 类别 | 存储 | 出 relay | 出网络 |
|------|------|---------|--------|
| `public` | 本地明文 + 上 relay 花名册 | ✅ 明文 | ✅ |
| `match-only` | 本地 AES 加密 | ❌ | 只出向量摘要 |
| `private` | 本地 AES 加密 | ❌ | ❌ 永不出本机 |

**本地存储**：
- `~/.agent-speaker/profile.enc` 整体 AES-256-GCM 加密
- 主密钥与 keystore 同源（密码派生）
- **显示需输入密码解密**，明文只在内存
- 向量化在解密后内存中完成 → 向量缓存可落盘（无法反推原文）

**示例 profile**：
```json
{
  "public": {
    "name": "alice",
    "role": "AI engineer",
    "looking_for": "co-founder"
  },
  "match_only": {
    "interests": ["AI", "Go", "decentralization"],
    "skills": ["backend", "protocol design"]
  },
  "private": {
    "budget": 5000,
    "height_cm": 175,
    "schedule": "night-owl"
  }
}
```

---

## 7. Token 与支付

| 用途 | 选型 |
|------|------|
| 跨 relay 转发费 | **AAstar Point (ERC-20)** |
| 漂流瓶基础费 | AAstar Point |
| Gas 抽象 | SuperPaymaster 代付 |
| 身份/账户 | AirAccount |

**为何不用 Lightning Network**：Lightning 是比特币 L2，跟我们生态无关联；AAstar Point 已有 gasless 代付 + ERC-4337 抽象账户支持，对我们更对路。

---

## 8. Relay 软件 — fork khatru

- 原仓库：[`fiatjaf/khatru`](https://github.com/fiatjaf/khatru)
- 选择理由：
  - Go 编写，与本项目同语言
  - fiatjaf 官方维护，活跃
  - 中间件/插件机制清晰
  - 已完整实现 Nostr NIP

**约束**：
- 严格遵守 khatru 的 middleware / event-handler 约定
- L2 路由插件以 plugin 形式叠加，不动 core
- 这样可以持续 `git fetch upstream` 跟进官方更新

---

## 9. Milestone 修订（取代 `03-development-plan.md`）

| Milestone | 内容 | 周期 |
|-----------|------|------|
| **M1** | TUI 聊天 + 群聊收尾（合并 #4 #5 #9） | 1-2 周 |
| **M1.5** | Relay 自部署 + 花名册 + register/discover | 2-3 周 |
| **M2** | L3 应用行为协议 JSON schema 标准化 | 2 周 |
| **M2.5** | 跨 relay 接力 + 漂流瓶 + AAstar Point 支付 | 3-4 周 |
| **M3** | 指令型自主任务（RFP / 协商 / 状态机） | 2-3 周 |
| **M4** | 长期背景任务（调度 / 触发 / 智能匹配） | 2-3 周 |
| **M5** | 加密 / 信誉 / 真支付集成完善 | 1-2 周 |

详细任务清单见 [`milestones/roadmap-v2.md`](milestones/roadmap-v2.md)。

---

## 10. 待决策（M1.5 启动前）

| # | 问题 | 当前倾向 |
|---|------|---------|
| D1 | 邀请券是否上链 (ERC-721)？ | 倾向"是"，但 M2.5 先用内部表 |
| D2 | 漂流瓶向量算法？ | 倾向轻量本地模型（sentence-transformers ONNX）|
| D3 | Relay 软件 fork 仓库放哪？ | `github.com/AuraAIHQ/relay-khatru`? |
| D4 | 花名册查询是否要 NIP-42 auth？ | 倾向"公开模式不需要"，"私密模式需要" |
| D5 | 1对1 通道协议格式？ | 倾向直接复用 WebSocket + L2 事件 |

---

## 11. 兼容性承诺

- ✅ 标准 Nostr 客户端可以查到我们 relay 上的 Kind 0 / Kind 1 / Kind 30078
- ✅ 标准 Nostr 客户端发到我们 relay 的事件正常存储和检索
- ⚠️ 跨 relay 路由 / 漂流瓶 / 支付校验只对实现 L2 的 relay 起效
- ⚠️ L3 行为协议对未实现的客户端表现为"看不懂的 JSON content"，不会破坏 Nostr 兼容

---

## 12. 相关文档

- [`milestones/roadmap-v2.md`](milestones/roadmap-v2.md) — 详细任务清单
- [`02-architecture-design.md`](02-architecture-design.md) — V1 原始架构（仍有效，作历史记录）
- [`agent-protocol-design.md`](agent-protocol-design.md) — Agent 行为协议初稿
- [`agent-protocols-summary.md`](agent-protocols-summary.md) — MCP / A2A / ACP 对比
- [`archive/`](archive/) — 过期文档归档
