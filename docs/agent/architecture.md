# Architecture — 技术骨架与不可破边界

> **权威文档是 [`../protocol-v2.md`](../protocol-v2.md),不是本文件。**
> 本文件只做一件事:把 `run` 循环里**最容易被违反**的架构约束提炼出来,让每个 Task 开工前能一眼看到边界。
> 有冲突以 `protocol-v2.md` 为准,并回来修本文件。
> 最后更新:2026-09-03

## 三层协议栈(2026-05-13 锁定,不重新讨论)

```
L3  应用行为协议  JSON-in-Nostr-Event-Content
    register / publish / inquire / tip / subscribe / drifting-bottle
L2  中继路由协议  ⚠️ 标准 Nostr 没有,需自建
    邻居(每 relay 6 邀请名额)+ TTL 跳数转发 + AAstar Point 计费 + 漂流瓶 20% 冗余带宽
L1  标准 Nostr    WebSocket / Event / Sig / NIP-44 / NIP-11 —— 兼容层,不动
```

## 不可破的边界

1. **L1 保持标准**。普通 Nostr 客户端必须能读我们 relay 上的 Kind 0 / Kind 1 / 我们的 behavior kind。任何让标准客户端**报错**的改动都不许做(读不懂 JSON 是允许的,报错不行)。见 `protocol-v2.md` §11。
2. **L2 以插件形式叠加在 khatru 之上,不动 core**。目的是能持续 `git fetch upstream` 跟进官方更新。
3. **不新增 kind 来表达新功能**。新 behavior 一律进统一信封的 `["b","<behavior>"]` tag —— 这是我们与 Buzz 明确不同的设计决策(Buzz 走「新功能=新 kind+新 NIP 草案」的路子),不要跟风。
4. **私钥永不出本机**。`private` 字段永不出本机,`match-only` 只出向量摘要,`public` 才上花名册。
5. **支付走 AAstar Point(ERC-20)+ SuperPaymaster gasless,不用 Lightning**。理由:Lightning 是比特币 L2,与本生态无关联;AAstar 已有 gasless 代付 + ERC-4337 抽象账户。见 `protocol-v2.md` §7。

## 包边界(当前 + M3 计划新增)

| 包 | 职责 | 状态 |
|---|---|---|
| `internal/nostr/` | Nostr 原语:密钥、事件签名、relay pub/sub、编解码 | 现有 |
| `internal/identity/` | keystore(`~/.hyphae/keystore.json`)、身份与联系人 CRUD | 现有 |
| `internal/messaging/` | 收发/历史、outbox/inbox | 现有 |
| `internal/daemon/` | 后台:outbox 重试、inbox watch、通知、自动回复 | 现有 |
| `internal/storage/` | SQLite(WAL),消息与 outbox,含 `audit_log` 哈希链 | 现有 |
| `internal/profile/` | 花名册:publish/discover/search | 现有 |
| **`internal/behavior/`** | **behavior 信封编解码 + 各 behavior 收发** | **M3 新增** |
| `internal/driftingbottle/` | 本地向量计算与匹配 | M4 计划 |
| `internal/vault/` | `profile.enc` 三层加密 | M4 计划 |
| `internal/workflow/` `internal/task/` | workflow 引擎 + 任务状态机 | M5 计划 |
| `pkg/payment/` | SuperPaymaster / AirAccount 对接 | M7 计划 |

## 已知的架构级技术债

| 债 | 影响 | 归属 |
|---|---|---|
| `AgentKind` 与 `ProfileKind` 共用 30078 | 靠 tag 区分,是巧合非设计 | **T3.5.2** |
| `SaveOutbox` 无锁读-改-写 + 固定临时文件名 | 丢更新 + 文件损坏 | **T3.5.1** |
| `scripts/deploy-relay.sh` 硬编码 `examples/basic` | 上游已改名,`local`/`tunnel` 跑不通 | T2.10 一并 |

## 参考

- [`../protocol-v2.md`](../protocol-v2.md) — **权威**架构决策
- [`../buzz-comparison-analysis.md`](../buzz-comparison-analysis.md) — 借/不借的边界(§7)
- [`../02-architecture-design.md`](../02-architecture-design.md) — V1 原始架构(历史记录)
- [`../GO_PROJECT_LAYOUT.md`](../GO_PROJECT_LAYOUT.md) — `internal/`+`pkg/` 布局约定
