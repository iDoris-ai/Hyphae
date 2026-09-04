# Roadmap — Milestone → Feature

> **本文件不是新的规划,是把已有规划翻译成 pilot 的三级结构。**
> 权威来源不变:
> - [`../protocol-v2.md`](../protocol-v2.md) §9 — 里程碑总表 + L1/L2/L3 架构决策(2026-05-13 锁定)
> - [`../milestones/roadmap-v2.md`](../milestones/roadmap-v2.md) — 每个里程碑的目标/架构改动/子任务
> - [`../milestones/testing-integration-plan.md`](../milestones/testing-integration-plan.md) — 三道验收门
>
> 上面三份文档说「做什么、为什么」,本文件只做一件事:**把子任务编号化成 `M→F→T`,让 `pilot run` 能一条条消费**。
> 两边冲突时以上游文档为准,并回来修本文件。
> 最后更新:2026-09-03

---

## 编号映射(与原里程碑一一对应,不重新发明)

原里程碑用了 `M1/M1.5/M2/M2.5/M3/M4/M5` 的半档编号,pilot 的 Task 编号 `T<M>.<F>.<T>` 不支持小数点里再带小数点。映射规则如下,**原名字在文档里继续用,只有 Task ID 用整数序**:

| 原里程碑 | pilot 编号 | 状态 |
|---|---|---|
| M1 · TUI 聊天 + 群聊 | `M1` | ✅ 已完成(PR #3/#4/#5/#9 均已合并) |
| M1.5 · Relay 自部署 + 花名册 | `M2` | 🔄 代码任务 10/10 done,`relay-khatru` 仓库待建 |
| M2 · L3 行为协议标准化 | `M3` | ⏳ 未开始 ← **当前目标** |
| M2.5 · 跨 relay + 漂流瓶 + 支付 | `M4` | ⏳ 未开始 |
| M3 · 指令型自主任务 | `M5` | ⏳ 未开始 |
| M4 · 长期背景任务 | `M6` | ⏳ 未开始 |
| M5 · 支付/信誉/安全收尾 | `M7` | ⏳ 未开始 |

> ⚠️ **读文档时注意**:`docs/milestones/roadmap-v2.md` 里的「M2」= 本文件的 `M3`。这是这次翻译带来的唯一认知负担,换来的是 Task 编号能机械排序。若你更希望保留原编号(接受 `T2.5.1` 这种 ID),说一声我改回去。

---

## M1 · 去中心化 IM 基础体验 ✅ 已完成

身份/联系人、NIP-44 加密点对点消息、SQLite 历史、群聊、Agent Profile 发布/发现、daemon 后台重试与自动回复、Bubble Tea TUI。**不再拆 Task**,仅作坐标。

覆盖率盲区(`internal/daemon` 0.5%、`internal/nostr` 0%)已由 M2 的 T2.9 补齐。

---

## M2 · Relay 自部署 + 花名册(原 M1.5)🔄 代码已完成,基建待办

**目标**:脱离对 `wss://relay.aastar.io` 单点的依赖;Agent 能在花名册注册自己、被别人发现。

10 个代码任务已全部合并(PR #13-#23),规格见 [`../../specs/m1.5/`](../../specs/m1.5/)。**本里程碑只剩一件事**,且明确是人类操作:

| Feature | 内容 | 状态 |
|---|---|---|
| F2.1 | 短期工程修复(ANSI 颜色 / `--json` / 联系人解析 / audit 哈希链) | ✅ done |
| F2.2 | 花名册协议(register 三模式 / discover 过滤 / 成员角色) | ✅ done |
| F2.3 | 测试与诊断补齐(nostr+daemon 覆盖率 / outbox 诊断 / relay 脚本加固) | ✅ done |
| F2.4 | **relay-khatru fork 仓库落地** | ⏳ **BLOCKED — 待人类决策** |

F2.4 卡在 `protocol-v2.md` §10 的 **D3**(fork 放 `AuraAIHQ/` 还是 `iDoris-ai/`)。考虑到仓库刚改名到 `iDoris-ai/Hyphae`,倾向 `iDoris-ai/relay-khatru`,但**这是产品/组织决策,不由自动化代拍**。详见 `tasks.md` 的 T2.10。

> **F2.4 不阻塞 M3 开工** —— `testing-integration-plan.md` §2 已逐条核对过:D1-D5 全部是 M4(跨 relay + 漂流瓶)的决策,M3 的 behavior 信封在单 relay 上就能完整开发/测试/联调。

---

## M3 · L3 行为协议标准化(原 M2)⏳ 当前目标

**目标**:把「发消息」升级为「发行为」——`register`/`publish`/`inquire`/`tip`/`subscribe`/`drifting-bottle` 收敛进统一的 Kind 信封,而不是每加一个功能就开一个新 kind。

**为什么现在做**:它是 M4/M5/M7 的共同前置(跨 relay 转发要有信封才能带 TTL/fee;workflow 的触发器要按 behavior 类型匹配;`tip` 要有 schema 才能接真支付)。M2 留下的两笔技术债(#26/#27)也都在这一层,越晚改包袱越重。

| Feature | 内容 | 依赖 |
|---|---|---|
| **F3.1** | Behavior 信封编解码器(`internal/behavior/`) | — |
| **F3.2** | 四种 behavior 收发实现(`register`/`publish`/`inquire`/`subscribe`) | F3.1 |
| **F3.3** | `tip`/`drifting-bottle` 仅定义 payload schema(不实现执行) | F3.1 |
| **F3.4** | Owner-attestation 字段预留(对接 AirAccount,本里程碑只做结构+签名校验) | F3.1 |
| **F3.5** | 技术债清理:kind 分配(#27)+ outbox 并发安全(#26) | — |
| **F3.6** | 兼容性与联调验收(标准 Nostr 客户端 + Agent24 跨仓联调) | F3.2 |

**F3.5 先于 F3.2 上线**:`roadmap-v2.md` 明确写了理由——F3.2 新增的 `publish`/`tip`/`subscribe` 各自的失败重试会让 outbox 的并发写者只增不减,修复必须在新 behavior 上线之前。

---

## M4 · 跨 relay 接力 + 漂流瓶 + 支付(原 M2.5)⏳ 未开始

V2 里技术难度最高的一段。**开工前必须先拍板 `protocol-v2.md` §10 的 D1-D5**(邀请券是否上链 / 漂流瓶向量算法 / relay fork 位置 / NIP-42 auth / 1对1 通道协议格式),否则 Task 无法写出可验证的验收标准。

| Feature | 内容 |
|---|---|
| F4.1 | relay 间互联(6 邀请名额邻居 + 1对1 长连接 + TTL 转发中间件) |
| F4.2 | 漂流瓶(topic 向量生成 + 本地余弦匹配 + 20% 冗余带宽转发) |
| F4.3 | 字段级隐私(`profile.enc` 三层加密) |
| F4.4 | 资金安全 property-based test(双花/重放/伪造授权) |

---

## M5 · 指令型自主任务(原 M3)⏳ 未开始

「说需求 → Agent 自动发现候选 → 协商 → 执行 → 验收」。workflow schema 以 Buzz `buzz-workflow` 为蓝本(4 触发器 + 有限 action 集),任务状态机是我们自己的领域逻辑。

| Feature | 内容 |
|---|---|
| F5.1 | workflow 定义格式 + 执行引擎 |
| F5.2 | 任务状态机 + SQLite 持久化 |
| F5.3 | RFP 生成 + 并行协商 + 决策算法 |
| F5.4 | approval 挂起-恢复机制(**设计阶段就要能持久化恢复**,Buzz `WF-08` 是现成反面案例) |

---

## M6 · 长期背景任务(原 M4)⏳ 未开始

24/7 自动维护人脉、发现机会。复用 M5 的 workflow 作调度底座。

| Feature | 内容 |
|---|---|
| F6.1 | 后台调度器(cron + continuous,复用 daemon 现有 inbox watch 循环) |
| F6.2 | 智能匹配(Jaccard + 向量余弦加权) |
| F6.3 | TUI Agent 活动 Feed(「动词-宾语-结果」三元组渲染,不甩原始 JSON) |

---

## M7 · 支付 / 信誉 / 安全收尾(原 M5)⏳ 未开始

把 M3 预留的授权字段、M4 的 `fee_paid` 机制接上真实资金。

| Feature | 内容 |
|---|---|
| F7.1 | `tip` 真实支付执行(SuperPaymaster gasless + AirAccount) |
| F7.2 | 信誉系统(基于 M2 的 `audit_log` 哈希链做衍生统计,不另起存储) |
| F7.3 | 资金路径安全测试收尾 + 文档完善 |

---

## 跨里程碑的长期跟踪项(不进任何里程碑,不排期)

- **Buxin / 不信** — 基于本仓库 Nostr 栈的自建家庭 IM + 音视频通话构想。消息层可复用 M2 的 relay 自部署;净新增是 WebRTC 信令、NAT 穿透、移动端。**明确不进 M3-M7 范围**,未来可能独立开仓库把 hyphae 当 SDK 用。
