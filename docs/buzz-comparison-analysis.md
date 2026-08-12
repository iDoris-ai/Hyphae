# Buzz（block/buzz）vs Hyphae：架构对比分析与借鉴建议

> 调研触发：https://blog.mushroom.cv/blog/block-buzz-nostr-human-agent-workspace/ 提及的 `block/buzz`
> 调研方式：克隆源码（Apache 2.0，7393★，2026-03-06 开源，最后 push 2026-07-24）+ 通读 `ARCHITECTURE.md`/`NOSTR.md`/`VISION*.md`/关键 crate 源码
> 对照对象：本仓库当前 V1 实现 + `docs/protocol-v2.md`（2026-05-13 锁定）+ `docs/agent-protocols-summary.md`
> 撰写日期：2026-07-24（2026-07-24 复核并补充落地事项）
> **范围说明**：本报告目的是从 Buzz 的具体技术实现和产品理念中挑值得借鉴的点，**不改变 Hyphae Protocol V2 已锁定的产品方向和架构**（去中心化、任何人可自部署、无中心化托管）。第 8 节的 TODO 已按里程碑拆分落地到 [`protocol-v2.md`](./protocol-v2.md) §12「借鉴 Buzz（block/buzz）调研的落地事项」（中长期，挂在 M1.5–M5）和 [`TODO.md`](./TODO.md)（短期，独立于 V2 协议重构可先做）。

---

## 0. 一句话结论

**Buzz 和 Hyphae 不是同一类产品，不存在"抄谁作业"的问题**——Buzz 是 Block（原 Square）出品、面向"一个组织内部团队"的**中心化托管协作工作区**（Slack + GitHub + CI 的替代品，人和 Agent 共享一个 relay 里的房间）；Hyphae/Protocol V2 的目标是**任何人可自部署、relay 之间互联的去中心化 Agent 对等网络**（没有"一个组织拥有一个 relay"这个假设，出发点更接近比特币/Nostr 本身的无许可精神，也更贴合 Mycelium Protocol「数字主权、拒绝平台垄断」的价值观）。

但两者共享同一个底层协议（Nostr）和同一个核心命题（"Agent 应该像人类队友一样拥有独立身份、被审计、可协作"），Buzz 背靠 Block 的工程资源，把这个命题的很多"深水区"细节（Agent 身份模型、workflow 引擎、审计链、CLI 的机器可读接口）已经做出了可运行、可复现的具体实现，**这些具体设计值得我们直接参考，但 Buzz 的重量级多租户后端架构不值得照搬**。以下逐项展开。

---

## 1. 项目背景对比

| 维度 | Buzz | Hyphae |
|---|---|---|
| 出品方 | Block Inc.（原 Square，Jack Dorsey） | iDoris.ai（Mycelium Protocol 生态） |
| 开源时间 | 2026-03-06（内部项目开源，非从零起步——CHANGELOG PR 编号已到 #2589+） | 从 0.1 版本原生开源迭代 |
| 当前状态 | v0.4.24，发布节奏密集（近乎连续交付，每版本 10-20 条变更） | V1 已完成 TUI/群聊/profile/daemon，V2（协议层重构）设计已锁定，未开始实现 |
| 代码规模 | Rust workspace，27 个 crate + 4 个前端 workspace（desktop/web/admin-web/mobile） | Go 单仓库，`cmd/` + `internal/`(11 包) + `pkg/`(3 包) |
| 团队/资源假设 | 公司级团队，含专职做形式化验证的工程师 | 小团队/个人开发者驱动的开源协议项目 |
| 部署模型 | 官方托管（`squareup/buzz-releases`）为主，自托管为辅 | 自托管为唯一模型，无官方托管 SaaS 规划 |

**关键判断**：Buzz 的资源量级和团队假设与我们完全不在一个尺度上。它的很多"正确答案"（比如 Postgres+Redis+S3+多租户）是"一个公司要同时服务多个内部团队"这个具体约束下的正确答案，不是"去中心化协议应该长什么样"的正确答案。看它的设计时要先问"这是不是因为他们要服务多租户 SaaS 才这样做的"，是的话大概率对我们没有参考价值。

---

## 2. 整体架构对比

### 2.1 Buzz 架构（relay 中心化单体 + 多租户）

```
Clients (桌面 Tauri+React / Web / Mobile Flutter / buzz-cli / buzz-acp)
        │ WebSocket / REST
        ▼
   buzz-relay (Axum, Rust)  ← 唯一编排入口，NIP-01 + NIP-42 + 自定义 REST
        │              │              │
   Postgres(事件+FTS)  Redis(pub/sub) S3/MinIO(媒体)
```

- **单一 relay 进程**扛下所有职责：事件存储、全文搜索、pub/sub 扇出、审计链、workflow 引擎、git 托管、媒体存储代理。
- **多租户（"community"）通过 host 域名解析**，同一套 Postgres/Redis/S3 服务多个隔离的 community，隔离性用 TLA+ 证明，用 Tamarin 证明鉴权协议健全性，还有一个 `buzz-conformance` crate 在**运行时**用轨迹回放校验实现是否仍然符合形式化模型。
- 12 步严格顺序的事件处理管线（AUTH→PUBKEY MATCH→...→WORKFLOW TRIGGER），fan-out 用 DashMap 三层索引，安全边界是"先查权限再注册订阅"，避免竞态泄露私有频道。

### 2.2 Hyphae 架构（对等 CLI + daemon，无中心）

```
Identity/Contact (keystore.json)  →  hyphae CLI/TUI ⇄ daemon(outbox/inbox/自动回复)
                                              │ NIP-44 + zstd
                                              ▼
                                    wss://relay.aastar.io（单一公共 relay，V1）
                                    → V2: N 个自部署 relay，6 邀请邻居 + AAstar Point 付费转发
```

- **没有服务端**——relay 只是标准 Nostr 中继（V2 计划 fork `khatru`），业务逻辑全部在客户端（CLI + daemon），存储是每个用户本地的 SQLite。
- 我们的"多租户"问题在 V2 里根本不存在：每个人自己的 relay 就是自己的边界，不需要在同一套 Postgres 里做行级隔离——这是架构起点的根本差异，Buzz 要解决"共享基础设施但要隔离"，我们要解决"没有共享基础设施还要能互通"（L2 邻居转发 + 漂流瓶）。

**结论**：这一层不构成可比性，两者是两种拓扑哲学（中心化托管 SaaS vs 无许可对等网络）。Buzz 的多租户 TLA+/Tamarin 投入对我们没有直接迁移价值，但它验证了一件事——**"隔离性/授权健全性值得被严肃对待"这件事本身是对的**，只是我们该验证的是"跨 relay 转发的 fee_paid 会不会被伪造/重放/双花"，而不是"多租户行级隔离"。

---

## 3. Nostr 协议使用对比

| 维度 | Buzz | Hyphae V1 | Hyphae V2（规划） |
|---|---|---|---|
| 底层协议 | NIP-01 + 原生 NIP-29 群组 relay | NIP-01 + NIP-44 加密 | NIP-01（L1，不变） |
| Kind 分配策略 | 40000-49999 为 Buzz 自定义区，81 个具名常量，按功能分段（channel/agent/forum/workflow/audit/presence） | Kind 4（加密DM）/ Kind 1（明文） | Kind 30078（L3 JSON 行为协议）、Kind 30079（漂流瓶，新增） |
| 应用层扩展方式 | **新增 kind + 官方草案 NIP 文档**（`docs/nips/` 13 份自定义 NIP，如 NIP-AA/NIP-AE/NIP-OA） | 无 | **单一 kind + JSON `content` 承载多种 behavior**（register/publish/inquire/tip/subscribe/drifting-bottle） |
| 对标准客户端的兼容性声明 | 有（NIP-34 git 事件可被 gitworkshop.dev 等标准客户端读取） | 天然兼容（用的都是标准 kind） | 明确写了兼容性承诺（`docs/protocol-v2.md` §11）：标准客户端能读 Kind 0/1/30078，看不懂 L2/L3 语义也不会报错 |
| 加密 | relay 层支持存储/路由 NIP-17 信封（kind:1059，ephemeral 签名 key），但 `NOSTR.md` 明确写着 **"NIP-04/NIP-44 not implemented"**——也就是说 DM 的端到端加密载荷本身还没做，relay 只是在搬运"信封"（**并未领先我们**） | 已用 NIP-44 做端到端加密（`pkg/crypto`） | 继续用 NIP-44 + 本地 AES-256-GCM 分级加密 profile |

**值得注意的差异化设计**：Buzz 选择"每个新功能开一个新 kind + 写一份 NIP 草案"，协议表面积会持续膨胀（已经 81 个 kind 常量 + 13 份自定义 NIP）；我们选择"L3 全部收敛到 Kind 30078 一个 JSON 信封里，用 `behavior` 字段区分"，协议表面积更紧凑，标准客户端只需要认识一个 kind 就不会"看到陌生的 kind 报错"。**这是我们已经做对的设计决策，不需要跟风 Buzz 的"kind 爆炸"模式**——他们的模式更适合"relay 软件是自己团队独家实现，不追求被第三方标准客户端广泛解析"的场景，我们更看重"标准 Nostr 客户端至少不报错"这条兼容底线。

**核对结论（已逐字核对 `NOSTR.md` 源文件）**：DM 加密这块不是我们落后的地方，反而**我们 V1 已经比 Buzz 现在的实现更彻底**——Buzz 的 relay 已经能存储/路由符合 NIP-17 形状的信封（kind:1059），但 `NOSTR.md` 第 83 行原文写明 "NIP-04/NIP-44 not implemented"，即信封内该有的端到端加密载荷还没做出来；我们从 V1 一开始就用 NIP-44 做真正的端到端加密。这点可以放心，不用补课，也不必因为"别人有 gift-wrap 我们没有"而焦虑——形状（信封路由）和实质（内容加密）是两回事，我们做的是实质。

---

## 4. Agent 身份与协作模型对比（本次调研的核心差异点）

这是 Buzz investment 最深、也是**对我们最值得学的一块**。

### 4.1 Buzz 的模型

- **身份原语与人类完全相同**：secp256k1 keypair + NIP-05 handle + NIP-42/NIP-98 Schnorr 签名认证。区别只是 channel membership 表里的 `Bot` 角色标记，**不是权限位标志（permission flag），而是像添加人类同事一样把 Agent 加入具体 channel**，可见范围完全由 membership 决定。
- **Owner Attestation（NIP-OA，自定义草案）**：Agent 可以携带一个 `["auth", "<owner-pubkey-hex>", "<conditions>", "<sig-hex>"]` tag，向 relay/对方证明"我这个操作是被 owner 授权的"，但**事件作者始终是 Agent 自己的 key**（不是委托覆盖身份，而是叠加授权证据）。owner 撤销授权（移除 maintainer），其名下所有 Agent 立即失去访问权限。
- **buzz-cli：JSON in / JSON out**，专为 LLM tool call 设计——stdout 纯 JSON、stderr 结构化错误 `{"error":..,"message":..}`、退出码语义化（0=ok/1=用户错误/2=网络/3=auth/4=其他/5=写冲突）。
- **buzz-acp：relay ↔ 任意 ACP 兼容 Agent 的桥接层**，已验证支持 Goose（原生）、Codex（经 `codex-acp` 适配）、**Claude Code（经 `claude-agent-acp` 适配）**。池化 1-32 个 Agent 子进程，每 channel 同时最多一个 prompt in-flight，崩溃自动 respawn，inbound author gate 分 owner-only/allowlist/anyone/nobody 四档。
- **Persona Pack（`buzz-persona`）**：一个 persona = 模型 + system prompt，一个 team = 一组命名 persona，桌面客户端内置，operator 可自定义——这是"Agent 作为可配置队友"这个隐喻在产品层面的落地。

> **术语提醒**：Buzz 的 "ACP" = Zed 提出的 **Agent Client Protocol**（stdio JSON-RPC，编辑器/宿主 ↔ Agent），跟我们 `docs/agent-protocols-summary.md` 里对比的 "ACP" = IBM 的 **Agent Communication Protocol**（已并入 A2A）**完全是两个不同的协议，同名不同物**。以后再讨论 ACP 时要先确认说的是哪一个，避免团队内部产生误解。Zed 的 ACP 目前是 Goose/Codex/Claude Code 这类"终端里跑的编码 Agent"接入宿主应用的事实标准之一，跟我们 Agent24 的定位（"基于 Claude Code 的自主任务执行框架"）关系更近，值得单独调研。

### 4.2 Hyphae 当前状态

- 身份模型本质上和 Buzz 相同的起点：每个 identity 一个 Nostr keypair，存在 `keystore.json` 里——**这一步我们已经做对了，不用改**。
- 但目前止步于"点对点消息 + 群聊成员"，**没有 Buzz 那种"Agent 是 channel/group 的一等成员，可见范围由 membership 决定"的显式建模**——我们的 `internal/group` 目前应该只有扁平的"成员列表"，没有区分 Human/Agent/Bot 角色，也没有"channel-scoped 可见性"这个概念（我们本来就是点对点转发，没有 relay 侧的 channel 隔离逻辑）。
- **没有 owner attestation / 委托授权机制**——这一点其实对我们更重要，因为我们规划中 Agent 要执行 `tip`（打赏，涉及 AAstar Point 真实资金）和 `drifting-bottle`（涉及个人 profile 向量），"Agent 代表谁在花钱/代表谁在暴露信息"这个授权链条现在是空白的。
- CLI 目前是人类可读文本输出为主，**没有统一的机器可读（JSON）输出模式**，这会成为 Agent24 或其他 Agent 想直接调用 hyphae CLI 时的摩擦点。
- **没有 persona / team 的概念**，但这本来就不是我们的核心场景（我们更像"通信协议 SDK/CLI"，不是"团队协作产品"），不需要照搬。

---

## 5. Workflow / 自动化对比

| 维度 | Buzz `buzz-workflow` | Hyphae |
|---|---|---|
| 现状 | YAML-as-code 引擎，4 类触发器（message_posted/reaction_added/schedule/webhook）+ 7 类 action（send_message/send_dm/set_channel_topic/add_reaction/call_webhook/request_approval/delay），`{{trigger.text}}` 模板变量，`evalexpr` 条件求值，100 并发信号量 | 只有 `daemon --auto-reply` 这一种硬编码自动化行为（收到消息就回一句固定前缀的回复） |
| 成熟度 | approval gate 未打通（`request_approval` 返回 Suspended 但不能恢复，官方 issue WF-08）、`send_dm`/`set_channel_topic` 是桩（WF-07）——**即便 Block 的资源，这块也还没完全做完** | 无 workflow 概念，M3（指令型自主任务）、M4（长期背景任务）尚未开始 |

**这是最值得直接借鉴具体设计的一块**，原因：
1. 我们 M3/M4 milestone 描述（"指令型自主任务：RFP/协商/状态机"、"长期背景任务：调度/触发/智能匹配"）本质上就是在重新发明一个 workflow/trigger 系统，与 Buzz 已经落地的 schema 是同一个问题。
2. Buzz 的设计足够"小气"（4 触发器、7 action、单遍模板解析、100ms 条件求值超时防对抗表达式），没有过度设计，很适合作为我们从零实现时的起点，而不是我们自己去发明一套复杂 DSL。
3. 连 Buzz 都还没把 approval gate 做完——说明这块"看起来简单、做全了很难"，我们不需要一开始就追求完整，可以按他们暴露出的坑（approval 需要能持久化挂起状态、支持恢复执行）提前在自己的设计里留好口子。

---

## 6. 技术成熟度信号对比

| 信号 | Buzz | Hyphae |
|---|---|---|
| 形式化验证 | TLA+（多租户隔离性证明）+ Tamarin（鉴权协议健全性，32 lemma）+ 运行时 conformance checker（轨迹回放校验实现是否符合模型）+ mutation testing | 无 |
| 审计日志 | 每 community 独立 SHA-256 append-only 哈希链，10 种审计动作，`pg_advisory_lock` 单写者保证 | 无 |
| 代码纪律信号 | 核心 crate 统一 `#![deny(unsafe_code)]` + `#![warn(missing_docs)]`，`deny.toml` 对每条被忽略的 RUSTSEC 漏洞写明具体理由 | 未系统化（Go 项目本身没有 unsafe 顾虑，但也没有系统性的依赖审计流程） |
| 测试 | 134 个 e2e 测试 + proptest 属性测试 + Playwright/Vitest 前端测试 | `go test ./...` 单元测试 + 5 个 shell e2e 脚本（Alice/Bob/Charlie 约定） |
| 速率限制 | **设计了 4 层 tier（human/agent-standard/agent-elevated/agent-platform），但承认"未实现"**（`AlwaysAllowRateLimiter` 仅测试桩） | 无 |

**结论**：Buzz 在"安全/审计/形式化"这条线上投入远超我们能承受的量级，但即便如此，它自己也有没做完的部分（速率限制、approval gate）。这说明——**安全基础设施投入应该跟着"资金/隐私风险敞口"走，而不是跟着"别人做了多少"走**。我们现阶段（V1 收尾 + M1.5 relay 自部署）风险敞口很小（没有真实资金流转），不需要现在就上 TLA+；但**一旦 M2.5 涉及 AAstar Point 真实转账**，风险敞口会跳变，那时候"双花/重放/伪造 fee_paid"这类问题就必须认真对待——不必用 TLA+/Tamarin 这种重型工具，但至少要有针对性的攻击场景测试（property-based test）。

---

## 7. 借鉴建议：明确"借"与"不借"

### ✅ 强烈建议借鉴（具体、低成本、直接命中我们规划中的空白）

1. **CLI 统一 JSON 输出模式**（借鉴 `buzz-cli`）—— stdout 纯 JSON、stderr 结构化错误、退出码语义化。这是我们要被 Agent24/其他 Agent 工具化调用的前提，成本很低。
2. **Group/Contact 成员角色显式化**（借鉴 Buzz `Owner/Admin/Member/Guest/Bot`）—— 把 `internal/group`、`internal/identity` 里的"联系人/成员"概念升级为带角色的 membership，为"Agent 是一等成员"这个我们自己 Mycelium 生态也认同的理念（AI 代理是数字分身、不是工具）打基础。
3. **Owner Attestation 式的授权委托机制**（借鉴 NIP-OA 思路，但接入 AAstar AirAccount）—— 设计一个类似的 `auth` tag：Agent 用自己的 key 签名执行 `tip`/`drifting-bottle` 等操作，但携带 owner（人类，AirAccount 身份）的授权证明，owner 撤销即时生效。这直接解决 iDoris.ai/AAstar 集成里"Agent 该怎么获得有限授权去花 owner 的 gasless 额度"这个尚未设计的问题。
4. **Workflow 引擎的极简 schema**（借鉴 `buzz-workflow`：4 触发器+7 action+单遍模板+超时条件求值+并发信号量）——直接作为 M3/M4 的设计蓝本，避免自己发明一套过度复杂的 DSL；同时提前学习他们暴露的坑（approval gate 需要可持久化挂起状态）。
5. **审计哈希链**（借鉴 `buzz-audit`：SHA-256 append-only chain）—— 成本很低（在 SQLite 里加一张 `audit_log` 表，`hash(prev_hash + record)` 即可），直接呼应 Mycelium Protocol「透明是默认值」的价值观，也为将来「代码即公共物品」的可审计性提供证据。
6. **Agent 活动 Feed 的"动词-宾语-结果"渲染哲学**（`VISION_ACTIVITY.md`）—— 对我们 TUI 未来做"Agent 在干什么"的可观测性展示（而不是原始 JSON 转储）有直接参考价值，属于"人类监督者扫一眼就能判断是否要介入"的 UX 原则，值得抄。
7. **relay 间成员发现 / mesh 传输的实现思路**（`buzz-relay-mesh`：iroh QUIC + scuttlebutt gossip）—— 虽然 Buzz Mesh 的目的是算力共池而不是我们的 relay 联邦，但"节点间怎么发现邻居、怎么建隧道"这个底层问题是共通的，值得作为我们 M2.5 邻居 relay mesh 的技术选型参考对象（Go 生态对应 libp2p-go/gossipsub，不需要照搬 Rust 的 iroh）。

### ⚠️ 不建议照搬（架构目标不同，或投入产出比在我们阶段不划算）

1. **Postgres + Redis + S3 + 多租户重量级后端** —— 我们的核心差异化是"任何人可自部署的轻量 relay"，SQLite + 单进程 Go daemon 的零运维模式正是我们和 Buzz 的分野所在。抄这套架构等于把自己变成一个要运维的 SaaS，违背了 fork khatru 的初衷。
2. **27-crate 级别的 Rust 微服务化拆分** —— 我们的代码规模和团队规模决定了应该维持 Go 单二进制 + `internal/`/`pkg/` 简单分层，过度拆分是过早优化。
3. **TLA+/Tamarin 全套形式化验证** —— 投入产出比在我们当前阶段（V1 收尾、无真实资金流转）不成立，是 Block 级别资源才玩得起的投入。等 M2.5 涉及真实 AAstar Point 转账时，用更轻量的 property-based test 覆盖资金安全场景即可，不必对标 Buzz 的重型工具链。
4. **完整 Git 托管后端**（`git-credential-nostr`/`git-sign-nostr`/smart HTTP git backend） —— 不是我们的核心场景，除非 Agent24/iDoris.ai 生态明确出现"Agent 自动开 PR 协作"的真实需求，否则不要为了对标而做。
5. **Flutter 移动客户端** —— 我们 TUI 优先，Buzz 自己的移动端都还在"being wired up"阶段，不急，等他们蹚完路（或我们真的有移动端需求时）再评估技术选型。
6. **"新功能=新 kind+新 NIP 草案"的协议膨胀模式** —— 我们已经选择了更收敛的"单 kind + JSON behavior 信封"设计（`docs/protocol-v2.md` §4），不需要跟风。

---

## 8. TODO / 路线图建议（映射到现有 M1.5–M5）

### 短期（V1 收尾 / M1.5 启动前，低成本高价值，可独立于协议重构先做）

- [ ] `hyphae` 全局增加 `--json` 输出标志（或 `AGENT_SPEAKER_OUTPUT=json` 环境变量），stdout 纯 JSON、stderr 结构化错误对象、区分退出码（成功/用户错误/网络错误/鉴权错误/写冲突），为后续被 Agent24 或其他 Agent 工具化调用做准备。
- [ ] `internal/storage` 增加 `audit_log` 表（SHA-256 append-only hash chain，记录 identity 创建、消息发送/接收、群组增删成员、daemon 自动回复等关键动作），成本低、价值高，直接呼应生态"透明是默认值"的价值观。
- [ ] `internal/group`（以及未来的 contact/membership 模型）显式引入角色概念（至少区分 Human / Agent-Bot），为 M1.5 之后"Agent 作为一等成员"打基础，不必现在就做完整权限系统。

### 中期（M1.5 / M2 阶段，配合花名册/L3 JSON schema 标准化一起设计）

- [ ] 设计我们自己的 owner-attestation 授权 tag（参考 NIP-OA，但对接 AAstar AirAccount 身份而非泛化 pubkey），明确"Agent 用自己的 key 签名 + 携带 owner 授权证明"这条链路，尤其是给 `tip`（M5 支付）和 `drifting-bottle`（M2.5，涉及 profile 向量暴露）这两个行为提前预留授权字段，避免 M2.5/M5 实现时才发现协议 schema 要推翻重来。
- [ ] 以 `buzz-workflow` 的 schema（4 触发器 + 7 action + 模板变量 + 条件求值超时 + 并发上限）为蓝本，设计 M3（指令型自主任务）/ M4（长期背景任务）的 YAML/JSON 定义格式，避免自研过度复杂的 DSL；设计阶段就把"approval 挂起后能恢复执行"这个 Buzz 自己都没做完的坑规划进去。

### 长期（M2.5 及之后，配合跨 relay mesh 与真实资金流转）

- [ ] 调研 Go 生态的 gossip/mesh 方案（如 `libp2p-go` 的 `gossipsub` 或轻量自研 scuttlebutt 风格协议）用于 L2 邻居 relay 的成员发现和隧道传输，作为技术选型参考对象是 Buzz 的 `buzz-relay-mesh`（iroh+scuttlebutt），但不必用 Rust/iroh，保持 Go 技术栈一致性。
- [ ] 待 AAstar Point 跨 relay 转发涉及真实资金后（M2.5/M5），针对"TTL 转发 fee_paid 校验、双花、重放、伪造授权 tag"等场景补充有针对性的攻击场景测试（Go 的 `testing/quick` 或引入 `gopter` 做 property-based test 即可，不必上 TLA+/Tamarin）。
- [ ] 待 TUI 或未来 web/桌面客户端做 Agent 可观测性面板时，参考 `VISION_ACTIVITY.md` 的"动词-宾语-结果"渲染哲学设计 Agent 活动展示，而不是直接甩原始事件 JSON 给用户看。
- [ ] 仅在 Agent24/iDoris.ai 生态出现明确"Agent 自动协作开发（开 PR/审代码）"需求时，再评估是否要做类似 NIP-34 的 git 事件集成，现阶段不必规划。

---

## 9. 结语

Buzz 证明了"Agent 用独立 keypair 身份、和人类同权、被统一审计"这条路线是可以在生产级产品里落地的，而且他们已经踩过一些坑（approval gate、速率限制未完工、DM 端到端加密仍是"未来项"）。我们不需要重新论证这条路线是否可行，可以直接站在它暴露出的具体设计（CLI JSON 接口、owner attestation、workflow schema、审计链）上往前走；但 Buzz 解决的是"一个组织如何把团队协作平台变成人机共居的房间"，我们解决的是"陌生的 Agent 和 Agent 之间如何在没有中心的网络里互相发现、互相验证、互相付费协作"——**多租户 SaaS 架构、微服务化拆分、企业级形式化验证这几块，是 Buzz 问题域特有的答案，不是我们该抄的作业**。把借鉴范围收窄在"身份/授权/审计/workflow 的具体协议设计"上，是投入产出比最高的做法。
