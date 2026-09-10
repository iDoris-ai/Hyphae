# Roadmap — Milestone → Feature

> **本文件不是新的规划**,是把已有规划整理成 pilot 的三级结构。权威来源不变:
> - [`../protocol-v2.md`](../protocol-v2.md) §9 — 里程碑总表 + L1/L2/L3 架构决策(2026-05-13 锁定)
> - [`../milestones/roadmap-v2.md`](../milestones/roadmap-v2.md) — 每个里程碑的目标/架构改动/子任务
> - [`../milestones/testing-integration-plan.md`](../milestones/testing-integration-plan.md) — 三道验收门
>
> 上面三份说「做什么、为什么」;本文件只把子任务编号化,让 `pilot run` 能一条条消费。
> 冲突时以上游文档为准,并回来修本文件。
> 最后更新:2026-09-10

---

## 编号约定

**里程碑沿用原名,不重新编号**:`M1` / `M1.5` / `M2` / `M2.5` / `M3` / `M4` / `M5`。

Task ID 用 `-` 分隔:**`<里程碑>-F<n>-T<n>`**,例如 `M2-F5-T1`。

> 早先版本曾把 `M1.5→M2`、`M2→M3` 重映射成整数,好让 Task ID 写成 `T3.5.1` 那种点号形式。**已撤销。** 代价算错了:原编号在 `protocol-v2.md`、`roadmap-v2.md`、`testing-integration-plan.md`、`specs/m1.5/` 和 GitHub issue 里到处都是且全都还活着,让「M2」在两套活文档里指不同的东西是个永久陷阱,换来的只是点号。用 `-` 分隔就没有 `M2.5` 会被解析成 `M2/F5` 的歧义,原名字一个都不用动。

---

## M1 · 去中心化 IM 基础体验 ✅ 已完成

身份/联系人、NIP-44 加密点对点消息、SQLite 历史、群聊、Agent Profile 发布/发现、daemon 后台重试与自动回复、Bubble Tea TUI。不再拆 Task,仅作坐标。

覆盖率盲区(`internal/daemon` 0.5%、`internal/nostr` 0%)已由 M1.5 补齐。

---

## M1.5 · Relay 自部署 + 花名册 🔄 代码已完成,基建待办

**目标**:脱离对 `wss://relay.aastar.io` 单点的依赖;Agent 能在花名册注册自己、被别人发现。

10 个代码任务已全部合并(PR #13-#23),规格见 [`../../specs/m1.5/`](../../specs/m1.5/)。只剩一件事,且明确是人类操作:

| Feature | 内容 | 状态 |
|---|---|---|
| M1.5-F1 | 短期工程修复(ANSI 颜色 / `--json` / 联系人解析 / audit 哈希链) | ✅ done |
| M1.5-F2 | 花名册协议(register 三模式 / discover 过滤 / 成员角色) | ✅ done |
| M1.5-F3 | 测试与诊断补齐(nostr+daemon 覆盖率 / outbox 诊断 / relay 脚本加固) | ✅ done |
| M1.5-F4 | **relay-khatru fork 仓库落地** | ⏳ **BLOCKED — 待人类决策(D3)** |

**M1.5-F4 不阻塞 M2**:`testing-integration-plan.md` §2 已逐条核对,D1-D5 全是 M2.5 的决策,M2 的 behavior 信封在单 relay 上就能完整开发/测试/联调。

---

## M2 · L3 行为协议标准化 ⏳ 当前目标

**目标**:把「发消息」升级为「发行为」——`register`/`publish`/`inquire`/`tip`/`subscribe`/`drifting-bottle` 收敛进统一信封,而不是每加一个功能开一个新 kind。

**为什么现在做**:它是 M2.5/M3/M5 的共同前置(跨 relay 转发要有信封才能带 TTL/fee;workflow 触发器要按 behavior 类型匹配;`tip` 要有 schema 才能接真支付)。两笔技术债(#26/#27)也都在这一层,越晚改包袱越重。

| Feature | 内容 | 依赖 |
|---|---|---|
| **M2-F1** | Behavior 信封编解码器(`internal/behavior/`) | M2-F5 |
| **M2-F2** | 四种 behavior 收发(`register`/`publish`/`inquire`/`subscribe`) | M2-F1 |
| **M2-F3** | `tip`/`drifting-bottle` 仅定义 payload schema(不实现执行) | M2-F1 |
| **M2-F4** | Owner-attestation 字段预留(对接 AirAccount,只做结构+签名校验) | M2-F1 |
| **M2-F5** | **存储层加固**:kind 分配(#27)+ outbox 并发安全(#26)+ 密文当明文存 | — |
| **M2-F6** | 兼容性与联调验收(标准 Nostr 客户端 + Agent24 跨仓联调) | M2-F2 |

**M2-F5 先于其余 Feature**。`roadmap-v2.md` 写明了理由:M2-F2 新增的 `publish`/`tip`/`subscribe` 各自的失败重试会让 outbox 并发写者只增不减,修复必须在新 behavior 上线之前。PR #35 的三轮评审又在同一层挖出三个既有缺陷(见 `tasks.md`),把这个 Feature 的分量进一步加重了——**存储层现在是本里程碑风险最集中的地方,不是收尾工作。**

---

## M2.5 · 跨 relay 接力 + 漂流瓶 + 支付 ⏳ 未开始

V2 里技术难度最高的一段。**开工前必须先拍板 `protocol-v2.md` §10 的 D1-D5**,否则 Task 写不出可验证的验收标准。

| Feature | 内容 |
|---|---|
| M2.5-F1 | relay 间互联(6 邀请名额邻居 + 1对1 长连接 + TTL 转发中间件) |
| M2.5-F2 | 漂流瓶(topic 向量生成 + 本地余弦匹配 + 20% 冗余带宽转发) |
| M2.5-F3 | 字段级隐私(`profile.enc` 三层加密) |
| M2.5-F4 | 资金安全 property-based test(双花/重放/伪造授权) |

---

## M3 · 指令型自主任务 ⏳ 未开始

「说需求 → Agent 自动发现候选 → 协商 → 执行 → 验收」。workflow schema 以 Buzz `buzz-workflow` 为蓝本(4 触发器 + 有限 action 集),任务状态机是我们自己的领域逻辑。

| Feature | 内容 |
|---|---|
| M3-F1 | workflow 定义格式 + 执行引擎 |
| M3-F2 | 任务状态机 + SQLite 持久化 |
| M3-F3 | RFP 生成 + 并行协商 + 决策算法 |
| M3-F4 | approval 挂起-恢复(**设计阶段就要能持久化恢复**,Buzz `WF-08` 是现成反面案例) |

---

## M4 · 长期背景任务 ⏳ 未开始

24/7 自动维护人脉、发现机会。复用 M3 的 workflow 作调度底座。

| Feature | 内容 |
|---|---|
| M4-F1 | 后台调度器(cron + continuous,复用 daemon 现有 inbox watch 循环) |
| M4-F2 | 智能匹配(Jaccard + 向量余弦加权) |
| M4-F3 | TUI Agent 活动 Feed(「动词-宾语-结果」三元组渲染,不甩原始 JSON) |

---

## M5 · 支付 / 信誉 / 安全收尾 ⏳ 未开始

把 M2 预留的授权字段、M2.5 的 `fee_paid` 机制接上真实资金。

| Feature | 内容 |
|---|---|
| M5-F1 | `tip` 真实支付执行(SuperPaymaster gasless + AirAccount) |
| M5-F2 | 信誉系统(基于 M1.5 的 `audit_log` 哈希链做衍生统计,不另起存储) |
| M5-F3 | 资金路径安全测试收尾 + 文档完善 |

---

## 跨里程碑的长期跟踪项(不进任何里程碑,不排期)

**Buxin / 不信** — 基于本仓库 Nostr 栈的自建家庭 IM + 音视频通话构想。消息层可复用 M1.5 的 relay 自部署;净新增是 WebRTC 信令、NAT 穿透、移动端。**明确不进 M2-M5 范围**,未来可能独立开仓库把 hyphae 当 SDK 用。
