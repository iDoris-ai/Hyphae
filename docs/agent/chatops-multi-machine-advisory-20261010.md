# Hyphae 是否应当建立「多机 Agent ChatOps」机制 —— 调研与决策建议

- 日期：2026-10-10
- 调研对象：[共工空间 / gonggong-space](https://github.com/yoqu/gonggong-space)（Apache-2.0，2026-09-24 创建）
- 触发材料：[Mushroom Research 博客文章](https://blog.mushroom.cv/blog/gonggong-space-yoqu-ai-agent-chatops-self-hosted-claude-code/)
- Hyphae 基线：`main` @ `48e81dd7`（F2a 已合并；F1 见 PR #169）
- 参与：2 个独立调研 subagent（共工拆解 / Hyphae 现状盘点）+ Codex **sol 6.1** 独立顾问（Mac Mini，Orca dispatch `ctx_64c920f1da48`）

## 0. 结论摘要

**立场：部分借鉴；保留「不建」作为可退出基线；一切落在 M1 验收之后。**

1. **值得借鉴的不是「群聊机器人」，而是三件事**：把机器能力变成*可授权、可观察的协作对象*（资源主人审批 + 定向实例 + 持久执行回执）；用**出站连接**避免开端口；用**共享正反例样例**固化跨语言契约。
2. **不要 fork、不要照搬**。对方 16 天、378 commits、12 stars、2 位贡献者、0 外部 PR，且实测存在 TLS `AcceptAny`（不校验服务器证书）、`@Bot` 等价于放开远程执行、key 明文落盘等硬伤。它是「可研究的样本」，不是「可依赖的基础设施」。
3. **Hyphae 的正确角色是通信与回执，不是执行**。现有资产（durable outbox、收件恰一次、Nostr 身份与端到端加密、机器可读 CLI）正好承担「受授权的任务请求 / 有界进度 / 回执」；执行、审批、恢复归 Agent24；预算与隐私归 iDoris。**Hyphae daemon 不新建第二套调度器或执行器。**
4. **成本量级**：最小闭环约 15–30 工程人日，前置闭环未就绪时可达 30–60 人日（顾问推测，非工期承诺，且不含 M1 收尾）。
5. **触发条件**（不满足就维持「不建」）：M1 组合验收通过；T01-E 与执行链门槛通过；≥2 位机器主人愿意提供隔离执行环境；过去两周每周 ≥10 次跨机派活/交接，或 >2 小时重复协调。先做 2 周 / 20 个真实任务的小试点，无持续需求即停。
6. **红线**：`@` 只选择目标，群成员资格 ≠ 主机权限；relay ACK ≠ 执行成功；消息去重 ≠ 副作用恰一次；不跨机共用私钥；不默认 full；不自造 workspace 同步、桌面直播或中央明文任务库。

## 0.1 决定记录（用户 2026-10-10）

**决定：暂停「共工空间式多机 Agent ChatOps」方向；本报告只作记录与备查。**

理由：共工空间的核心价值落在**远程执行**（把任务派到某台机器上跑、并让群成员实时看到执行过程），
而 Hyphae 的定位是**沟通**。按 `docs/agent/ecosystem-roadmap.md` 的架构边界：

> - Agent24：执行、审批、记忆、调度；加载能力模块、领域 OS 和外部 workspace。
> - Hyphae：核心网络：身份、发现、加密通信、行为及协作协议；**执行交给 Agent24**。

因此：**不启动**本文档 §3 的任何借鉴项，**不新增** run/approval/handoff 相关契约或信封类型，
M1 验收边界不受影响。若将来真的出现跨机执行需求，应由 Agent24 侧先提出，并重新评估 §5/§7 的触发条件与红线。

本文档保留的价值：共工空间的技术快照、其安全设计的反面教材，以及「若将来要做，需要哪些前置与验收」的清单。

## 1. 共工空间到底做了什么

**一句话**：自托管 ChatOps 层，把每位队友本机已登录的 Claude Code / Codex 变成群聊里可 @ 的 Bot；任务在本机执行，过程实时回流群聊，Bot 之间可以交接任务。

**架构**（证据见 `gonggong-brief.md` §2）：

```
群成员 @Bot  →  React/Vite Web  →  Fastify + PostgreSQL 服务端
                                   （只做路由/中继/审计，不跑 agent、不持模型 key）
                                          ↓
                              本机 Rust `gg` daemon（只发出站连接，不监听端口）
                                          ↓
                              per-(group × bot) ACP adapter（stdio）
                                          ↓
                              本机 Claude Code / Codex CLI
```

- **协议边界 = ACP（Agent Client Protocol）**：daemon 是 ACP client，薄 adapter 包装 CLI；`session/update`、`session/request_permission`、`session/resume`，靠协议而不是解析 stdout。
- **密钥不出本机**：模型 API 请求从本机直出，Relay 只传消息文本。
- **安全**：三级权限（只读 / workspace 写 / 完全）+ 越权需 Bot 主人批准 + 零开放端口（NAT/防火墙透明）。
- **接力**：显式 `hand_off`，登记后等 `run.done` 再开下一跳，默认 3 跳，`origin_user_id` 每跳鉴权。
- **体验**：实时 reasoning/工具/命令输出、diff、git 状态、文件树、每轮 token 用量、可发多选题让人决策。

**成熟度与实测风险**（同 brief §7/§9）：

| 项 | 事实 |
|---|---|
| 活跃度 | 2026-09-24 创建；378 commits；2 贡献者（其中一位是 AI）；12 stars；0 外部 PR / 0 issue |
| 许可证 | Apache-2.0 |
| **TLS** | `crates/gonggong/src/tls.rs` 返回 `ServerCertVerified::assertion()`，**跳过服务器证书校验**（作者已知并接受 MITM 风险） |
| **权限** | `Rules::decide` 对 full 先直接放行；`@Bot` 即等价于允许群成员在你机器上执行代码 |
| **凭据** | `providers.json` 明文（0600）；Web 侧可远程管理 |
| **文章勘误** | 文章里的「Sentinel 安全组件」在仓库中**不存在**（只有 UI 滚动哨兵和一个 Bot 人格名）——已按「未核实」标注，不重复引用 |

## 2. Hyphae 现在有什么（证据见 `hyphae-inventory.md`）

**真有货的三件**：

1. **可靠离线发送队列** —— `internal/messaging/outbox.go`（`UpdateOutbox`/`AttemptSend`/`CleanupOutbox`，跨进程锁 `outbox_lock_unix.go`）：签名事件先落盘再发布，失败复用同一 event ID，有真实 relay 验收，125 条离线积压 + 重启去重实测过。
2. **收件恰一次 + 单一 inbox watcher** —— `internal/storage/message.go` 的 `StoreIncomingMessageOnce` 原子条件 upsert；`internal/messaging/inbox_watch.go` 单订阅、历史+live、NIP-67 分页。
3. **去中心化加密传输与身份** —— `internal/nostr`、`internal/relayquery`、`pkg/crypto/nip44.go`、内嵌 khatru `internal/relay`、`internal/identity/keystore.go`（AES-256-GCM）：每个成员/agent 独立 keypair，relay 可自部署，私钥不出本机。

**三个缺口**：

1. **协作语义基本空白** —— 无任务状态机、无 dispatcher、无 agent 间 handoff、无审批门、无产物通道。
2. **群 fanout 本体曾长期停在分支上** —— F1（fanout 记账/CAS/状态机）即本仓 PR #169，正在评审收口中；群 TUI 仍是占位。
3. **跨机器/多设备没有成体系的同步协议** —— keystore 与 SQLite 都是单机的（`internal/identity/keystore.go`；`internal/audit/audit.go` 明确单机），全仓也没有多设备设计文档（只有 `docs/agent/138-adversarial-review-20261008.md` 提到 multi-device）。该文档的 **F6** 记录了「同一密钥两台设备，一台 accept、一台 decline」的竞态；**该症状已修**：断言已按文档要求从「当前的错误行为」改成期望行为并并入正式测试 `TestMultiDeviceAcceptThenDeclineCancelsPendingGroup`（`internal/groupchat/store_test.go:599`），在 `main @ be6d0a70` 实测 **PASS**。准确表述应为「个别竞态已修，但没有成体系的跨机器一致性设计」，不是「明确未修」。**「团队多台机器」仍是短板，但性质是「缺设计」而非「已知未修」**。

> **更正记录（2026-10-10，外部评审发现）**：本文初版误引 `specs/m1/E4-r4-i4-multidevice.md` —— 该路径在仓库中不存在（`specs/` 下只有 `specs/m1.5/`），且把 F6 的编号记成「R4/I4」、状态记成「明确未修」。经核实后已按上述内容更正。

**一句话判断**：方向接近（可靠加密群消息骨架一半以上可复用），但落地程度中等——传输与队列是强项，协作语义与跨机器是空白。

## 3. 建议借鉴什么（按增量 ROI 排序）

成本量级 L=2–5 / M=5–10 / H=10–20+ 人日，均为**推测**；全部在 **M1 冻结范围之外**。

| # | 借鉴点 | 来源机制 | Hyphae 落点 | 成本 |
|---|---|---|---|---|
| ① | **共享正反例与跨语言契约验证** | TS zod 权威定义 + Rust 手工镜像 + 同一批 JSON fixture 双端往返 | M2/T01-B、T01-E；`docs/agent/t01-contract-gates.md` 已要求 Go/Agent24 共用样例 | L |
| ② | **资源主人审批 / 有限授权 / 超时拒绝**（次序第二，但**执行前必须先有**） | 只有 Bot 主人能批、默认 30 分钟超时拒绝、管理员不能代批 | T01-C、E-M1 T15/T16；Hyphae 只搬运请求与审批回执，策略归 Agent24 | M |
| ③ | **定向实例 + 持久执行回执 + 只出站连接**（构成最小产品） | 显式注册、一次性绑定码、离线 `offline_wait`、Run 状态机、daemon 主动连接 | M2/E-M1 T07、T14–T17；`internal/profile/`、`internal/messaging/outbox.go`、`internal/groupchat/fanout*.go` | M–H |
| ④ | **分级进度、源端脱敏与背压** | 16 MiB 待发预算；丢可替代文本、保留工具状态与终态 | M2 进度契约；Agent24 产出安全摘要，Hyphae 投递 | M |
| ⑤ | **ACP 作为执行适配边界**（先探索再采用） | ACP client + stdio adapter，固定版本、可换 mock | Agent24 执行 provider 层；Hyphae `internal/daemon` 不嵌执行器 | M |
| ⑥ | **可追溯、限跳、授权收窄的串行 handoff**（需求证明后再做） | 显式 `hand_off`、默认 3 跳、`origin_user_id` 每跳鉴权、`relayOf` 去重 | M3/E-M3 T01/T03/T04/T06 | H |
| ⑦ | **按 run 收窄的协作工具接口**（MCP） | 每会话回环 MCP，服务器从进行中的 run 推导群/Bot，不信参数 | 先复用现有 `--json` CLI，有需求再在 Agent24 加薄 MCP | M |

**每个借鉴点的验收标准**（摘要）：①双方验证器对固定正反例判定一致，重复 key/tag、未知版本、截断均拒绝；②陌生人/错主人/过期/改参的批准执行 0 次，批准后开跑前再校验，重启保留审批状态；③自然语言 `@` 与同名昵称只展示不执行，relay ACK / received / awaiting approval / running / completed / failed / unknown 分开，同 ID 改参拒绝；④建议每 run 每 5 秒一个摘要，断线 30 分钟资源仍有界；⑤固定 adapter hash，取消/resume 降级有测试；⑥默认 ≤3 跳，A→B、B→C 不推出 A→C，父完成 ≠ 整链完成。

## 4. 不要照搬的十件事

1. **`AcceptAny` TLS** —— 丢掉服务器身份验证；保留标准证书校验。
2. **`@Bot` 自动取得主机权限 / full 默认放行** —— 同事与 prompt 注入都可能触发危险副作用；默认不可执行。
3. **把命令黑名单当沙箱** —— 脚本、间接工具、网络外发都能绕开；隔离与能力权限才是边界。
4. **复制中心服务器权威 + 明文搜索库** —— 与 Hyphae「不信任 relay」的假设冲突；NIP-44 继续端到端，搜索在有权接收者本机做。
5. **明文 provider 文件 / 长期机器 token / Web 远程 key 管理** —— 权限位挡不住同 UID 恶意进程；**不采纳**中央下发 Nostr 私钥。
6. **自造共享工作区版本协议** —— 中央版本 + 逐路径 hash + 三方合并在删除/rename/权限/崩溃/冲突上未证明；先独立 worktree/分支 + 固定 base commit + 受审补丁。
7. **反向隧道 / LiveKit / `gg-cast`** —— 预览与远程操作产品，不是任务协议必需品；outbound 保留、隧道延期。
8. **原样广播 thought / 工具输出 / diff，靠服务端脱敏兜底** —— 源端最小化 + 分类权限 + 保留策略。
9. **静默安装 adapter / 借主人个人订阅当团队算力** —— 供应链与额度争用风险；固定版本/hash、明确费用归属。
10. **因「零 issue」认定安全，或依赖整个平台 / Sentinel** —— 16 天、2 贡献者的快照不能证明经过验证；只引用真实权限与测试机制。

## 5. 路线图落点与 M1 隔离

| 阶段 | 建议交付 | 边界 |
|---|---|---|
| **M1（当前）** | 完成原 fanout、路由、群 TUI、offline 与三人真实验收 | **不新增 run/approval/handoff/envelope type**；ChatOps 不成为 M1 发布前置 |
| M1.5 | 按原计划完成 relay 自部署/发现稳定性 | 单 relay 试点不依赖 L2 邻居、TTL、支付或公网发现 |
| M2 / T01 / E-M1 | ①②③的协议、共享样例、目标身份与数据最小化 | T01-A～E 冻结；旧消息不能升级为执行请求 |
| E-M1 C 段 | Agent24 授权单步、登记、恢复、结果回执；简洁群摘要 | 20 任务试点不能替代 T19 四仓总出口 |
| M3 / E-M3 | 团队任务、协商、取消、授权收窄 handoff；⑤⑦按需 | 每节点结果可对账，重放不扩权 |
| M4 / E-M4 | 长期活动与自治/长跑 | 限流、循环防护、恢复有验收 |

**隔离做法**：M1 验收单不改；ChatOps 另立契约与分支/worktree，绑定已验收基线，运行时显式 opt-in，默认只展示未知/普通消息；只复用一个 inbox 解密/路由入口和一套 durable outbox，**不创建第二个 watcher，不在 auto-reply 里调用执行器**。

**一处需先解决的文档冲突**：老 roadmap 的「每功能一个 kind」与 architecture 的「统一 kind/b tag」方向冲突；T01 指出 30078 是 addressable，保留/覆盖语义需先核对。**本建议不拍板 kind 编号。**

## 6. 主要风险

| 风险 | 后果 / 依据 | 控制 |
|---|---|---|
| 群信任放大、prompt 注入 | 消息或仓库内容诱导调用工具 | 默认不可执行；主人有限授权 + 隔离文件/网络；未知 sender / 改参 / 转委派的负例 0 副作用 |
| 重放、重启、乱序、审批撤回 | 收件去重 ≠ 执行去重；结果落盘前有崩溃窗口 | durable 请求/run/结果 + 待发回执；SIGKILL/断线场景；状态不明不自动重跑 |
| 隐私 | NIP-44 不隐藏 pubkey / 时间 / 收件路由；本机发 API ≠ 本地推理 | 只传摘要与受授权引用；`local_only` 零外发；明确保留政策 |
| 密钥换机/丢失/吊销 | 无集中轮换；群内历史无法收回 | 每实例独立密钥；换机需重新授权；本机可立即撤销执行策略 |
| 成本放大 | 每进度事件按 N 收件人加密签名（6 节点每秒一条 30 分钟 ≈ 9000 个 recipient event；改 5 秒摘要 ≈ 1800，均为推算） | 源端合并、有界队列、保留上限、并发/时长预算 |
| 变成第二套调度器 | `ecosystem-tasks.md` 禁止 Hyphae daemon 另起执行器 | 通信归 Hyphae、执行/审批归 Agent24、预算/隐私归 iDoris；试点不达标就停 |

**与「去中心化 + 私钥不出本机」的关系**：薄层方案兼容，完整照搬会冲突。任务请求与结果由端点各自签名、逐收件人加密，每个资源主人本机验权，relay 可自部署/替换，不依赖持有全部授权与明文数据的中心服务器。注意：「私钥不出本机」**不等于**「所有工作数据不出本机」，也**不等于**「本机已登录的订阅能供团队无限使用」——Nostr 身份、模型认证、Git 凭据、群数据权限要分别定义。

## 7. 建议的下一步

1. **现在**：批准「借鉴方向 + M1 后的有界试点设计」，**不在 M1 内启动开发**（不改 M1 验收单）。
2. **接着**：复用 T01 / Agent24 既有主线，先做授权单步闭环（②③），用真实任务证明团队多机需求。
3. **然后**：需求被证明才投入 M3 handoff；未证明则维持现有消息网络 + 手工协作（即「不建」基线）。

## 附录：证据与来源

| 文件 | 内容 |
|---|---|
| `~/.pi/agent/research/hyphae-chatops-20261010/article.md` | 原始文章（Jina Reader 抓取，2026-10-10） |
| `~/.pi/agent/research/hyphae-chatops-20261010/gonggong-brief.md` | 共工空间技术拆解（505 行，含实测源码引用与文章勘误） |
| `~/.pi/agent/research/hyphae-chatops-20261010/hyphae-inventory.md` | Hyphae 现状盘点（能力矩阵 + 对照表 + 有/缺两栏） |
| `~/.pi/agent/research/hyphae-chatops-20261010/sol-advisory.md` | Codex sol 6.1 独立顾问建议全文（150 行） |

外部链接：[共工空间仓库](https://github.com/yoqu/gonggong-space) · [博客原文](https://blog.mushroom.cv/blog/gonggong-space-yoqu-ai-agent-chatops-self-hosted-claude-code/) · [TLS 源码](https://raw.githubusercontent.com/yoqu/gonggong-space/main/crates/gonggong/src/tls.rs) · [权限判定源码](https://raw.githubusercontent.com/yoqu/gonggong-space/main/crates/gonggong/src/local.rs)

**口径声明**：本文档中「共工空间」的事实均来自上述材料（subagent 抓取的原仓库文件）；Hyphae 的成熟度结论基于 `f62ddd1`/`48e81dd7` 的代码阅读，**代码存在 ≠ 已发布或已通过真实联调**。所有工日、试点阈值、事件数量均为**建议或推算**，非承诺。未核实项已逐条标注。
