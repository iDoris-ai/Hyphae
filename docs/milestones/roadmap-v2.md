# Hyphae V2 里程碑计划

> 版本目标：从基础消息工具升级为去中心化 Agent 协作网络
> 权威架构决策见 [`../protocol-v2.md`](../protocol-v2.md)（L1/L2/L3 协议栈、fork khatru、AAstar Point、漂流瓶、字段级隐私 —— 2026-05-13 锁定）
> 本文件是**详细任务清单**，按 `protocol-v2.md` §9 的 M1/M1.5/M2/M2.5/M3/M4/M5 七段式展开，每个里程碑给出：目标、架构改动、子任务、交付物
> 最后更新：2026-07-24（对照 [`../buzz-comparison-analysis.md`](../buzz-comparison-analysis.md) 调研结论做了一轮修订，见文末「本次修订说明」）

---

## 🎯 核心愿景

Hyphae 不是一个人对人的加密聊天工具，而是**去中心化的 Agent 协作网络基础设施**：

1. **任何人可自部署 relay**，无需依赖任何一方的中心化基础设施
2. **人和 Agent 是同一套身份体系里的平等成员**（各自的 Nostr keypair），Agent 可以被授权代表 owner 做有限的事（对话、打赏、暴露信息摘要）
3. **发现和协作有代价但代价对陌生人友好**：花名册发现、漂流瓶低成本触达、AAstar Point 按用付费，而不是"要么免费广播骚扰所有人，要么完全找不到人"

> **产品方向不变**：以下所有借鉴自 Buzz（block/buzz，Block Inc. 开源的团队协作平台）的具体设计，都是"拿一个已验证可行的协议/机制细节来用"，不是把我们的架构改成它的多租户托管 SaaS 模式。完整对比见 [`buzz-comparison-analysis.md`](../buzz-comparison-analysis.md)，"借"与"不借"的边界见该文档 §7。

---

## 📋 里程碑总览

| # | 名称 | 目标 | 状态 |
|---|---|---|---|
| M1 | TUI 聊天 + 群聊 | 打造去中心化版即时通讯基础体验 | ✅ 已完成 |
| M1.5 | Relay 自部署 + 花名册 | 脱离对单一公共 relay 的依赖，建立发现机制 | 🔄 代码任务已完成（10/10），relay-khatru 仓库待建 |
| M2 | L3 行为协议标准化 | 把"消息"升级为可扩展的 Agent 行为语言 | ⏳ 未开始 |
| M2.5 | 跨 relay 接力 + 漂流瓶 + 支付 | 去中心化广度（多 relay 互联）+ 低成本触达 + 激励层 | ⏳ 未开始 |
| M3 | 指令型自主任务 | "说需求 → Agent 自动执行 → 看结果" | ⏳ 未开始 |
| M4 | 长期背景任务 | 24/7 自动维护关系、发现机会 | ⏳ 未开始 |
| M5 | 支付/信誉/安全收尾 | 真实资金闭环 + 可信度 | ⏳ 未开始 |

---

## M1 · TUI 聊天 + 群聊 ✅ 已完成

**交付**：PR [#3](https://github.com/iDoris-ai/hyphae/pull/3)（SQLite 存储）、[#4](https://github.com/iDoris-ai/hyphae/pull/4)（TUI 聊天界面，`internal/tui/`）、[#5](https://github.com/iDoris-ai/hyphae/pull/5)（Agent Profile v0.25.0 + daemon 自动回复 + 群聊，`internal/profile/`/`internal/daemon/`/`internal/group/`）、[#9](https://github.com/iDoris-ai/hyphae/pull/9)（Apache 2.0 合规）均已合并。当前 `main` 已具备：身份/联系人管理、点对点加密消息（NIP-44）、SQLite 历史存储、群聊、Agent Profile 发布/发现、daemon 后台重试与自动回复、Bubble Tea TUI。

### M1 测试状态（2026-07-24 复核）

**自动化测试**：`go build ./...`、`go vet ./...` 全部干净；`go test ./...` 全部包 `ok`，无失败用例；`./test.sh`（构建 + 单元测试 + CLI 冒烟检查）全绿。单元测试覆盖率（`go test ./... -cover`）：

| 包 | 覆盖率 | 备注 |
|---|---|---|
| `pkg/crypto`（NIP-44） | 85.7% | 最关键的加密路径覆盖最好 |
| `pkg/compress` | 83.3% | |
| `internal/storage` | 48.8% | |
| `internal/group` | 36.7% | |
| `internal/profile` | 36.2% | |
| `internal/identity` | 34.5% | |
| `internal/tui` | 27.4% | |
| `internal/messaging` | 26.6% | |
| `internal/daemon` | 0.5% | ⚠️ 后台重试/自动回复几乎无单元测试覆盖 |
| `internal/nostr`、`internal/common` | 0% | ⚠️ 核心 Nostr 原语层没有单元测试 |

**真实网络自测**（本地起 `scripts/minirelay.go`，用 `.env` 里的真实 Alice/Bob 身份跑通全链路，因为 `wss://relay.aastar.io` 当前不可达——`curl` 返回 530）：identity/contact → 加密消息发送（NIP-44）→ 接收解密 → history stats/conversation → group create/list → profile publish/search → daemon 启动+outbox 处理，**全部链路真实跑通**，不是只测了 mock。

**过程中发现的真实问题（已记入 `../TODO.md`）**：
1. `cmd/hyphae/main.go` 里 `color.NoColor = false` 是硬编码，不管 stdout 是否被重定向都强制输出 ANSI 颜色转义符——脚本化/管道消费 CLI 输出时会看到乱码。
2. `history conversation --with <name>` 要求 `<name>` 必须在 contact 列表里，而 `agent msg --to <name>` 的解析更宽松——两个命令对"名字"的处理规则不一致，容易让人以为是 bug。
3. `daemon` 的 outbox 重试机制里发现历史遗留的失效队列条目（重试全部失败），怀疑是跨环境/跨 relay 测试遗留的脏数据，值得在 M1.5 顺手加一个 `hyphae storage` 的 outbox 清理/诊断命令。

**结论**：自动化测试真实可信但覆盖有盲区（daemon/nostr 两个核心包基本零覆盖），真实网络链路验证通过。**已经不需要"再充分自测"才能进入 M1.5**——但 daemon/nostr 补测试值得作为 M1.5 的前置小任务（低成本，见下）。人工测试指南见仓库根目录 [`QUICK_MANUAL_TEST.md`](../../QUICK_MANUAL_TEST.md)（双人/三人协作测试 + Profile/TUI 部分），建议你实际拉一个朋友测一遍 Part 1-3，尤其是 TUI 的交互体验，这部分自动化测不出来。

---

## M1.5 · Relay 自部署 + 花名册 + register/discover 🔄 代码任务已完成，relay-khatru 仓库待建

### 实际完成情况（2026-07-29 复核）

`specs/m1.5/` 拆出的 10 个子任务已全部 `done`（PR [#13](https://github.com/iDoris-ai/hyphae/pull/13)-[#23](https://github.com/iDoris-ai/hyphae/pull/23) 均已合并，见 [`../../specs/m1.5/README.md`](../../specs/m1.5/README.md) 任务表），但里程碑完成定义里还有一项没做：**`AuraAIHQ/relay-khatru`（或 `iDoris-ai/relay-khatru`，`protocol-v2.md` D3 尚未拍板）fork 仓库尚未创建**——这是一次性人类基础设施操作，明确排除在自动化 loop 之外。`scripts/deploy-relay.sh` 目前仍克隆 khatru 官方 `basic` 示例（且该示例路径在上游已改名，`local`/`tunnel` 模式暂时跑不通真实部署，见 `specs/m1.5/README.md` 记录的已知问题）。

顺带发现但未在本里程碑修复的两个问题：`AgentKind`/`ProfileKind` 共用 Kind 30078（[#27](https://github.com/iDoris-ai/hyphae/issues/27)，建议 M2 一并解决）、`SaveOutbox` 并发写不安全（[#26](https://github.com/iDoris-ai/hyphae/issues/26)）。分阶段测试/联调的具体安排见 [`testing-integration-plan.md`](testing-integration-plan.md)。

### 目标
脱离对 `wss://relay.aastar.io` 这一个公共 relay 的依赖：任何人能一条命令拉起自己的 relay；Agent 能在花名册上注册自己、被别人发现。

### 架构改动
- **新仓库 `AuraAIHQ/relay-khatru`**（独立于 `hyphae` 本仓库）：fork [`fiatjaf/khatru`](https://github.com/fiatjaf/khatru)，作为 L2 插件的宿主（`protocol-v2.md` §8）。`scripts/deploy-relay.sh` 目前克隆的是 khatru 官方 `basic` 示例，待这个仓库建立后切换过去。
- `internal/profile/` 扩展现有 `AgentProfile` 类型，加入 `mode` 字段（`simple`/`tagged`/`structured` 三档，对应 Kind 0 扩展 schema）。
- `internal/group/`、`internal/identity`（contact 模型）新增成员角色字段（`Human`/`Agent`），为 M2 的 owner-attestation 和 Agent 作为一等成员打基础。
- `internal/storage/` 新增 `audit_log` 表（独立于花名册功能，可与本里程碑其他任务并行）。

### 子任务

**relay 自部署**
- [ ] 创建 `AuraAIHQ/relay-khatru` 仓库，`git remote add upstream fiatjaf/khatru` 保持可持续 `fetch upstream`（`protocol-v2.md` §8 约束）
- [ ] `scripts/deploy-relay.sh` 从"克隆 khatru basic 示例"切换为"克隆 `relay-khatru`"（`RELAY_REPO` 已留好环境变量占位）
- [ ] Docker 镜像 + `docker-compose` 示例，降低"任何人可自部署"的门槛

**花名册协议**
- [ ] Kind 0 profile 扩展 schema：`register` 三种 mode（simple 仅名字 / tagged 加标签 / structured 完整 capabilities+价格+评分，沿用本文件旧版 Week 4 设计）
- [ ] `hyphae profile publish` 扩展支持 `--mode`
- [ ] `hyphae profile discover` 扩展过滤条件：`--capability`/`--price-min`/`--price-max`/`--rating-min`/`--online-only`（当前 `internal/profile` 已有 discover/search 基础，此项是扩展 filter，不是新建）

**（借鉴 Buzz）成员角色模型**
- [ ] `internal/group`、`internal/identity` 的成员/联系人结构增加角色字段（至少 `Human`/`Agent`），可见范围逻辑跟随角色走，参考 Buzz `Owner/Admin/Member/Guest/Bot` 的思路但不需要照搬完整权限系统（详见 `protocol-v2.md` §12）

**并行、不阻塞上面任务、可现在就做（来自 `../TODO.md`）**
- [ ] CLI 全局 `--json` 输出模式：stdout 纯 JSON、stderr 结构化错误、退出码语义化，为 Agent24 等外部 Agent 工具化调用 CLI 做准备（借鉴 `buzz-cli`）
- [ ] `audit_log` 表：SHA-256 append-only 哈希链，记录身份创建/消息收发/群组增删成员/daemon 自动回复（借鉴 `buzz-audit`）

### 交付物（示例）
```bash
hyphae relay-khatru deploy --local          # 或沿用 scripts/deploy-relay.sh
hyphae profile publish --mode structured --capability "seo:..." --rate "audit:page:50"
hyphae profile discover --capability seo --online-only --rating-min 4.5
```

---

## M2 · L3 应用行为协议标准化 ⏳ 未开始

### 目标
把"发消息"升级为"发行为"：`register`/`publish`/`inquire`/`tip`/`subscribe`/`drifting-bottle` 统一收敛到 Kind 30078 的 JSON 信封里（`protocol-v2.md` §4），而不是像 Buzz 那样每个功能开一个新 kind（我们已经做出的、不需要跟风的设计决策，见 `buzz-comparison-analysis.md` §3）。

### 架构改动
- 新包 `internal/behavior/`：Kind 30078 事件的编解码器（`["c","agent-v2"]`/`["b","<behavior>"]`/`["z","zstd"]` tag + zstd 压缩 content），复用 `pkg/compress`。
- `pkg/types/` 新增 `Behavior`、`BehaviorEnvelope` 及各 behavior 的 payload 结构体。
- `internal/behavior/` 下按 behavior 分文件：`register.go`/`publish.go`/`inquire.go`/`subscribe.go`（`tip`/`drifting-bottle` 的**执行**逻辑留到 M2.5/M5，本里程碑只定义 schema）。

### 子任务
- [ ] Behavior envelope 编解码器 + 单元测试（覆盖 zstd 压缩/解压、tag 解析）
- [ ] `register`/`publish`/`inquire`/`subscribe` 四种 behavior 的收发实现
- [ ] `tip`/`drifting-bottle` 的 payload schema 先定义好（供 M2.5 直接用），本里程碑不实现执行逻辑
- [ ]（**借鉴 Buzz NIP-OA**）在 behavior schema 里预留 owner-attestation 字段：`["auth", "<owner-pubkey-hex>", "<conditions>", "<sig-hex>"]` 的等价设计，但 owner 身份对接 **AAstar AirAccount**（而非泛化 pubkey）。本里程碑只做数据结构 + 签名/校验函数，不接入真实 AirAccount SDK（那是 M5 范畴）——现在预留，避免 M2.5/M5 做 `tip`/`drifting-bottle` 时才发现协议要推翻重来
- [ ] 标准 Nostr 客户端兼容性验证：确认非 L2/L3-aware 客户端读到 Kind 30078 不报错（`protocol-v2.md` §11 兼容性承诺的验收测试）
- [ ] **（[#27](https://github.com/iDoris-ai/hyphae/issues/27) 遗留债）** `AgentKind`（`agent msg`）和 `ProfileKind`（`profile publish`）目前共用 Kind 30078，纯属巧合非设计。既然本里程碑要把 `register`/`publish`/`inquire`/`tip`/`subscribe` 统一收敛进新的 behavior 信封，这是重新定义 kind/tag 约定的天然时机——本任务给新 behavior 信封分配一个独立 kind（不再和 `agent msg` 共用 30078），并确认这不破坏已发布事件的可读性（标准客户端仍能读到旧的 Kind 30078 消息，只是不再有新的 behavior 事件混进同一个 kind）
- [ ] **（[#26](https://github.com/iDoris-ai/hyphae/issues/26) 遗留债）** `internal/messaging/outbox.go` 的 `SaveOutbox` 并发读-改-写无锁，有丢更新和文件损坏两种失败模式（daemon 自动重试循环和用户手动 CLI 已经会真的并发操作同一个 `outbox.json`）。本里程碑新增的 behavior 收发会进一步增加对 outbox 的并发写入场景（`publish`/`tip`/`subscribe` 各自的失败重试），修复应先于这些新 behavior 上线，而不是让并发写者数量继续增加

### 交付物（示例）
```bash
hyphae behavior register --mode structured ...
hyphae behavior inquire --target <npub> --capability seo
hyphae behavior subscribe --filter '{"tags":["AI"]}'
```

---

## M2.5 · 跨 relay 接力 + 漂流瓶 + AAstar Point 支付 ⏳ 未开始

### 目标
relay 之间能互联互通（邀请邻居 + 付费转发），陌生人之间能用漂流瓶低成本触达，个人隐私数据分级加密。这是 V2 里技术难度最高、也是去中心化广度的核心里程碑。

### 架构改动
- `relay-khatru` 仓库新增 L2 中间件：邻居邀请管理、TTL 转发、`fee_paid` 校验（`protocol-v2.md` §5）。
- `hyphae` 侧新增 `internal/driftingbottle/`：本地 profile 向量计算、topic 向量匹配、阈值判断。
- `internal/vault/`（新包，或并入 `internal/profile`）：`profile.enc` 三层加密（public/match-only/private），AES-256-GCM，密钥与 keystore 同源。

### 子任务

**relay 间互联**
- [ ] 6 邀请名额邻居机制（`relay-khatru` 内部表实现，D1 决策"先不上链，M2.5 用内部表"）
- [ ] 1 对 1 长连接通道（两 relay 直连 WebSocket 互转发）
- [ ] 跨 relay TTL 转发中间件 + `fee_paid`（AAstar Point）校验
- [ ]（**借鉴 Buzz `buzz-relay-mesh`**，但换成 Go 生态）调研并选定邻居发现/隧道传输方案（候选：`libp2p-go` 的 `gossipsub`，或自研简化 gossip），产出一份技术选型对比文档 —— Buzz 用 Rust 的 iroh(QUIC)+scuttlebutt 解决的是"节点怎么发现邻居、怎么建隧道"这个共通问题，值得参考实现思路，不必照搬语言/库

**漂流瓶**
- [ ] Topic vector 生成（D2 决策"倾向轻量本地模型，如 sentence-transformers ONNX"，需要在本里程碑内定稿）
- [ ] 本地 `profile_vector` 匹配（余弦相似度 + 可配置阈值，默认 0.7）
- [ ] 20% 冗余带宽随机转发逻辑（relay 侧）

**字段级隐私**
- [ ] `profile.enc` 三层加密实现（public 明文上花名册 / match-only 只出向量摘要 / private 永不出本机）
- [ ] 密码解密显示流程（明文只在内存）

**资金安全（借鉴 Buzz 的态度，不借鉴其工具链重量级）**
- [ ]（**借鉴 Buzz 形式化验证的动机，但降级为 property-based test**）针对 TTL 转发 `fee_paid` 校验补充攻击场景测试：双花、重放、伪造授权 tag。用 Go 的 `testing/quick` 或引入 `gopter` 即可，**不引入 TLA+/Tamarin**——Buzz 用重型工具证明多租户隔离性是因为它有真实的多租户 SaaS 风险敞口，我们这里的风险敞口是"钱有没有被伪造/重放"，用针对性的属性测试覆盖足够，见 `buzz-comparison-analysis.md` §6

### 交付物（示例）
```bash
hyphae behavior drifting-bottle --content "..." --topic AI,创业 --ttl 10
hyphae profile vault set --field interests --tier match-only
```

---

## M3 · 指令型自主任务（RFP / 协商 / 状态机） ⏳ 未开始

### 目标
"说需求 → Agent 自动发现候选人 → 协商 → 执行 → 验收"。

### 架构改动
- 新包 `internal/workflow/`：workflow 定义 + 执行引擎。**Schema 设计以 Buzz `buzz-workflow` 为蓝本**（`WorkflowDef`/`TriggerDef`/`ActionDef`/`Step`，`evalexpr` 风格条件求值），而不是照搬本文件旧版设想的"自然语言理解 + 自研 DSL"——理由见 `buzz-comparison-analysis.md` §5：Buzz 的极简 4 触发器+7 action 设计经过生产验证，我们没必要重新发明。
- `internal/task/`：任务状态机（`Created→Discovering→Negotiating→Contracted→Executing→Monitoring→Completed/Failed`，沿用本文件旧版设计，这部分本来就是我们自己的领域逻辑，不是通用 workflow 引擎该管的）。

### 子任务
- [ ]（**借鉴 `buzz-workflow` schema**）workflow 定义格式：触发器类型对应我们的 Kind 30078 behavior 事件（`behavior_received`/`schedule`/`webhook`），有限 action 集（`send_behavior`/`request_approval`/`delay` 等），单遍模板变量解析（`{{trigger.field}}`），条件求值加超时保护防对抗表达式
- [ ] 任务状态机 + SQLite 持久化（`internal/storage` 新增 `tasks` 表）
- [ ] RFP 生成 + 并行协商引擎（`inquire` behavior 批量发送，收集报价）
- [ ] 决策算法：价格/能力/评分加权评分，选择最优候选
- [ ]（**吸取 Buzz `WF-08` 的教训**）approval / 挂起-恢复机制：设计阶段就规划"挂起状态可持久化、可从挂起点恢复执行"，不要等做完主流程才发现漏了这一步——Buzz 自己的 `request_approval` 至今只能返回 `Suspended` 却不能恢复，是一个现成的反面案例
- [ ] `hyphae task` 命令组：`create --task ... --budget ... --deadline ...` / `list` / `show <id>` / `logs <id>`

---

## M4 · 长期背景任务（调度 / 触发 / 智能匹配） ⏳ 未开始

### 目标
24/7 自动维护人脉、发现机会，不需要用户手动触发。

### 架构改动
- 复用 M3 的 `internal/workflow/` 作调度底座，新增 cron 支持（`robfig/cron`）。
- 新包 `internal/match/`：兴趣标签 Jaccard 相似度 + 向量相似度（复用 M2.5 的 `profile_vector`）。
- `internal/tui/` 新增 Agent 活动展示视图。

### 子任务
- [ ] 后台调度器：cron 表达式 + `continuous` 持续监听模式（复用 daemon 现有 inbox watch 循环，不新起一套轮询机制）
- [ ] 条件触发器：按 behavior kind/tag/author 匹配
- [ ] 智能匹配：兴趣标签 Jaccard 相似度 + `profile_vector` 余弦相似度加权
- [ ]（**借鉴 Buzz `VISION_ACTIVITY.md` 的"动词-宾语-结果"渲染哲学**）TUI 增加 Agent 活动 Feed：daemon/workflow 在做什么事，用"谁-做了什么-结果如何"的三元组渲染，而不是把原始事件 JSON 甩给用户看，方便人一眼判断要不要介入
- [ ] `hyphae bg` 命令组：`create --schedule ... --condition ... --action ...` / `list` / `logs <id>` / `pause`/`resume`/`stop` / `summary --today`

---

## M5 · 加密 / 信誉 / 真支付集成收尾 ⏳ 未开始

### 目标
把 M2 预留的授权字段、M2.5 的 `fee_paid` 机制接上真实资金，补齐信誉系统。

### 架构改动
- 新包 `pkg/payment/`：对接 AAstar SuperPaymaster（gasless 代付）+ AirAccount（身份/账户）SDK。
- `tip` behavior（M2 已定义 schema）在本里程碑接入真实执行逻辑。
- 信誉统计基于 M1.5 引入的 `audit_log` 哈希链做衍生统计，不单独起一套信誉存储。

### 子任务
- [ ] `tip` behavior 真实支付执行：调用 SuperPaymaster/AirAccount，替换 M2 阶段的字段占位
- [ ] SuperPaymaster gasless 接入验证（用户无需持有原生 gas 代币）
- [ ] 信誉系统：基于 `audit_log` 的完成率/响应率统计；可选参考 Buzz `VISION_PROJECTS.md` 提到的 web-of-trust 声誉思路做**轻量版**（不做跨 relay 完整实现，那本身在 Buzz 自己的路线图里也还是"💭 构想阶段"，见 `buzz-comparison-analysis.md` §6）
- [ ] 资金路径安全测试收尾：验收 M2.5 阶段的 property-based test 覆盖率，确认双花/重放/伪造场景都有测试覆盖
- [ ] 文档完善：用户指南、API 文档、示例教程

---

## 本次修订说明（2026-07-24）

相对旧版（2026-05-13 之前的周计划版本），本次修订：
1. 把已经过期的"Week 1-12"日历式排期，替换成按 M1/M1.5/M2/M2.5/M3/M4/M5 组织，并标注每个里程碑的真实完成状态（核对了 GitHub PR #1-#10 的实际合并记录，M1 三个关联 PR 均已合并）。
2. 把 [`buzz-comparison-analysis.md`](../buzz-comparison-analysis.md) 的借鉴结论拆解成可执行的子任务，直接嵌入到相关里程碑里（而不是作为附录），并在每处标注"借鉴来源"和"为什么不照搬全部"，方便以后追溯决策依据。
3. 删除了旧版里"Lightning Network 支付"（已在 `protocol-v2.md` §7 明确否决，改用 AAstar Point）、"bbolt 本地缓存"（已用 SQLite 取代，见 `internal/storage`）等与当前决策不一致的内容。
4. M3/M4 的任务拆解结构沿用了旧版本身已经设计得不错的"状态机"、"Jaccard 匹配"部分，只是把 workflow/触发器那部分换成了 Buzz 验证过的更简单的 schema。

## 🔗 相关文档

- [`testing-integration-plan.md`](testing-integration-plan.md) — **分阶段测试与联调计划**：每个里程碑要过的三道验收门（单元测试/本地真实网络/跨仓联调），以及 M2-M5 各自具体的联调对象和过线标准
- [`../../specs/m1.5/`](../../specs/m1.5/) — **M1.5 可执行任务包**：把本文件 M1.5 一节拆成 10 个独立、可被 `/loop` 自动化循环逐条实现+自测+双重 review+开 PR 的任务规格（接口/设计/数据/流程/验收标准），配一份 `LOOP_PLAYBOOK.md` 操作手册
- [`../protocol-v2.md`](../protocol-v2.md) — 架构决策权威文档（L1/L2/L3、Token、Relay 软件选型、待决策事项）
- [`../buzz-comparison-analysis.md`](../buzz-comparison-analysis.md) — Buzz（block/buzz）架构对比与借鉴分析
- [`../TODO.md`](../TODO.md) — 不依赖 V2 重构、可独立推进的短期工程任务
- [`../02-architecture-design.md`](../02-architecture-design.md) — V1 原始架构（历史记录）
- [`../agent-protocols-summary.md`](../agent-protocols-summary.md) — MCP / A2A / ACP 协议对比
