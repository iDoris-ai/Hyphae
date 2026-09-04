# Tasks — 执行台账

> 一个 Task = 一个分支 = 一个 PR。`pilot run` 每轮挑**第一个 `READY` 且依赖全 `DONE`** 的任务。
> 编号 `T<M>.<F>.<n>`,与 [`roadmap.md`](roadmap.md) 的 M/F 对应。
> 状态:`READY`(可开工) / `IN_PROGRESS` / `IN_REVIEW`(PR 已开,等裁决) / `DONE` / `BLOCKED`(等人类决策)。
> **验收命令必须是机器可判定的**——写不出来的说明还太大或太模糊,继续拆或标 `BLOCKED`。
> 最后更新:2026-09-03

---

## 当前可开工(READY)

### T3.5.1 · 修复 `SaveOutbox` 并发写不安全 · M · READY
**为什么先做**:[#26](https://github.com/iDoris-ai/Hyphae/issues/26)。F3.2 会新增 `publish`/`tip`/`subscribe` 三种 behavior 各自的失败重试,outbox 并发写者只增不减。修复必须在新 behavior 上线**之前**。
**两种失败模式**(M1.5 期间用 20 个并发 goroutine 实测复现):
1. **丢更新** — `LoadOutbox → 内存改 → SaveOutbox` 是无锁的读-改-写,20 个并发操作里常有 18-19 个被静默撤销。
2. **文件损坏** — 临时文件名固定为 `outbox.json.tmp`,两个并发写者 `O_TRUNC` 到同一 inode、按各自 offset 写入,`rename` 发布的已是损坏 JSON。PR #33 的并发轮在等价实现上实测 **3000 次并发对中 11% 产出不可解析文件**。

**做什么**:
- (a) `SaveOutbox` 改用 `os.CreateTemp` —— 消除失败模式 2,不改磁盘 JSON 格式。**可直接照抄 `internal/identity/keystore.go` 的 `SaveKeyStore`**,PR #33 已经在那边做完并配了并发测试。
- (b) 给 load-modify-save 关键区加真正的互斥(flock 锁文件,或把所有 outbox 写路由到 daemon 持有的 channel/actor)—— 才能解决失败模式 1。**(a) 单独做不能解决 (b)**。

**验收命令**:
```bash
go test ./internal/messaging/... -race -count=1 -run 'Outbox'
# 必须包含一个新测试:N 个并发 goroutine 各自 load→改一条→save,
# 断言 (1) 最终文件可解析 (2) N 个改动全部存活(不丢更新)
```
**依赖**:— · **交付物**:`internal/messaging/outbox.go` + 并发测试 · **关联**:PR #33 的 FU-2

---

### T3.5.2 · 给 behavior 信封分配独立 kind · S · READY
**为什么**:[#27](https://github.com/iDoris-ai/Hyphae/issues/27)。`internal/messaging.AgentKind`(`agent msg`)和 `internal/profile.ProfileKind`(`profile publish`)**都是 30078**,目前靠各自的 `d`/`c` tag 区分——是巧合不是设计。M3 本来就要重新定义 kind/tag 约定,这是成本最低的时机;越晚改,已发布事件的兼容包袱越重。

**做什么**:给新 behavior 信封分配一个**独立** kind(不再与 `agent msg` 共用 30078),并确认不破坏已发布事件的可读性(标准客户端仍读得到旧的 30078 消息,只是不再有新 behavior 混进同一 kind)。

**验收命令**:
```bash
go test ./internal/behavior/... ./internal/messaging/... ./internal/profile/... -count=1
# 外加一条真实 relay 验证(第 2 道门):
go run scripts/minirelay.go &   # 本地 relay
./bin/hyphae agent msg --from alice --to bob --content x --relay ws://localhost:7777
./bin/hyphae behavior register --mode simple --relay ws://localhost:7777
# 用 nak 或 minirelay 日志核对:两者落在不同 kind 上,且旧 30078 消息仍可被标准客户端查到
```
**依赖**:— · **交付物**:kind 常量定义 + 迁移说明

---

### T3.1.1 · Behavior 信封编解码器 · M · READY
**做什么**:新包 `internal/behavior/`,实现 Kind 信封的编解码:`["c","agent-v2"]` / `["b","<behavior>"]` / `["z","zstd"]` tag + zstd 压缩 content(复用 `pkg/compress`)。`pkg/types/` 新增 `Behavior`、`BehaviorEnvelope` 类型。

**验收命令**:
```bash
go test ./internal/behavior/... ./pkg/types/... -race -count=1
# 覆盖:zstd 往返、tag 解析、未知 behavior 的降级处理、
#       畸形/截断 content 不 panic、空 tag / 重复 tag 的边界
```
**依赖**:T3.5.2(先定 kind 再写编解码器,避免返工) · **交付物**:`internal/behavior/envelope.go` + 测试

---

## 阻塞中(BLOCKED — 等人类决策,不由自动化代拍)

### T2.10 · 创建 `relay-khatru` fork 仓库 · BLOCKED
**卡在什么**:`protocol-v2.md` §10 的 **D3** —— fork 放 `AuraAIHQ/relay-khatru` 还是 `iDoris-ai/relay-khatru`?仓库刚改名到 `iDoris-ai/Hyphae`,倾向后者,但这是组织决策。
**为什么不自动化**:`specs/m1.5/README.md` 明确把「创建 GitHub 仓库、配 CI/发布」列为一次性人类基建操作。
**顺带需要一起定的**:`scripts/deploy-relay.sh` 的 `local`/`tunnel` 模式目前对着真实 khatru 上游**跑不起来**——脚本硬编码 `examples/basic`,而上游已把该目录拆成 `basic-badger`/`basic-sqlite3`/... 。fork 建好后指向 fork 根目录,这个问题大概率自然消失;若短期不建 fork,则需要把子路径做成 `RELAY_EXAMPLE_DIR` 环境变量。
**不阻塞 M3**。

### T4.0 · 拍板 D1/D2/D4/D5 · BLOCKED
M4(跨 relay + 漂流瓶)开工前必须定:D1 邀请券是否上链(ERC-721)、D2 漂流瓶向量算法(倾向 sentence-transformers ONNX)、D4 花名册查询是否要 NIP-42 auth、D5 1对1 通道协议格式。**定不下来就写不出 M4 任务的可验证验收标准**,所以卡在这里而不是硬拆。

---

## 待细化(等 F3.1 落地后再拆成可执行 Task)

按 `roadmap-v2.md` M2 一节,这些的**内容**已经写清楚了,但要等信封编解码器定型才能写出精确验收:

| Task | Feature | 说明 |
|---|---|---|
| T3.2.x | F3.2 | `register`/`publish`/`inquire`/`subscribe` 四种 behavior 的收发实现,预计 1 behavior = 1 Task |
| T3.3.1 | F3.3 | `tip`/`drifting-bottle` 的 payload schema(**只定义,不实现执行逻辑**) |
| T3.4.1 | F3.4 | Owner-attestation 字段:`["auth","<owner-pubkey>","<conditions>","<sig>"]` 的等价设计,owner 身份对接 AirAccount。本里程碑只做数据结构 + 签名/校验函数,**不接真实 SDK**(那是 M7) |
| T3.6.1 | F3.6 | 标准 Nostr 客户端兼容性验证(`protocol-v2.md` §11 承诺的验收测试) |
| T3.6.2 | F3.6 | **与 Agent24 跨仓联调**(第 3 道门),走 Seeder `Cooperation-Center` 的 `repo:speaker` 任务 |

---

## 三道验收门(从 M3 起每个里程碑都要过)

来自 [`../milestones/testing-integration-plan.md`](../milestones/testing-integration-plan.md) §1,**第 3 道不能跳过**:

| # | 门 | 谁做 |
|---|---|---|
| 1 | 单元测试 `go test ./...` + 新功能配新测试 | 实现者 |
| 2 | 本地真实网络(起 relay,跑真实 CLI,肉眼核对 relay 上的原始事件) | 实现者 |
| 3 | **跨仓联调**——真实外部消费者(Agent24)拿真实二进制跑一遍契约 | 双方工兵 |

CC-82 的教训:那次挖出的 4 个 bug 里,只有「`--json` 没接」是读代码能发现的;`d` 标签覆盖要真实 NIP-01 relay 才测得出;`id` 编码损坏和 `from` 截断是对端拿真实二进制跑自己的 bridge 才现形的。**任何一边单独测都测不出来。**
