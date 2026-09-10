# Tasks — 执行台账

> 一个 Task = 一个分支 = 一个 PR。`pilot run` 每轮挑**第一个 `READY` 且依赖全 `DONE`** 的任务。
> 编号 `<里程碑>-F<n>-T<n>`,与 [`roadmap.md`](roadmap.md) 对应。
> 状态:`READY` / `IN_PROGRESS` / `IN_REVIEW` / `DONE` / `BLOCKED`(等人类决策)。
> **验收命令必须机器可判定**——写不出来的说明还太大或太模糊,继续拆或标 `BLOCKED`。
> 最后更新:2026-09-10

---

## M2-F5 · 存储层加固(当前 Feature)

PR #35 已经修掉这一层的两个缺陷(`StoreMessage` 的 `INSERT OR REPLACE` 语义、legacy 迁移的 id 碰撞),但**它的三轮对抗评审在同一层挖出四个既有缺陷**,全部还在。下面按「先修会让别的修复失效的那个」排序。

### M2-F5-T1 · SQLite PRAGMA 只在一条连接上生效 · S · READY
**机制**:`internal/storage/db.go:35-70` 的 `PRAGMA` 是对 `*sql.DB` **连接池** `Exec` 的。`db.Exec` 只借一条连接、用完归还,**pragma 只留在那一条上**;池里其余连接(以及后续按需新建的连接)拿的是驱动默认值。

**实测**(`modernc.org/sqlite` v1.48.2,同一个 `*sql.DB` 上同时持有两条连接分别读):

| PRAGMA | 不设任何 pragma(对照) | 复刻 `InitDB` 之后 | 坏了吗 |
|---|---|---|---|
| `busy_timeout` | 0 / 0 | 5000 / **0** | ✅ 坏 |
| `foreign_keys` | 0 / 0 | 1 / **0** | ✅ 坏 |
| `synchronous` | 2 (FULL) / 2 | 1 (NORMAL) / **2** | ✅ 坏 |
| `journal_mode` | delete | wal / **wal** | ❌ 没坏 |

8 条并发连接上读 `foreign_keys`,**某次运行**得到 `[0 0 0 1 1 1 0 1]`,另一次独立运行得到 `[1 0 0 0 0 0 0 0]`——**具体比例随连接池当时的状态变(设了几条 pragma、复用了几条连接),不要把某一个向量当成可复现的读数。** 可复现的是性质:同一时刻不同连接会给出不同的值。

`journal_mode` 是唯一幸免的:**WAL 是写进数据库文件的属性,不是每连接状态**,所以设一次对所有连接都算数。这也解释了为什么这个 bug 一直没被发现——最显眼的那条恰好是有效的。

**后果**,按严重度:
1. **`foreign_keys` 在多数连接上是关的** —— 外键约束时开时关,取决于你抽到哪条连接。
2. **`busy_timeout=0`** —— 并发写不重试,直接 `database is locked`。代码注释明写这条是为了应对并发 DDL,实际上大部分连接没有。
3. **`synchronous` 停留在 FULL** —— 不是正确性问题,是每次提交都多一次 fsync,与注释声称的 NORMAL 不符。

**为什么排第一**:后面几个 Task(尤其 T2 的 outbox 并发)的验收信号都要靠「并发写不再失败」来判断,而 `busy_timeout=0` 会让它们继续以 `database is locked` 收场——**人会以为修错了地方,而不是以为还差一层。**

> ⚠️ **量默认值,别假定它是 0。** 这条最初的描述只点名了 `busy_timeout`。外部评审用 `mattn/go-sqlite3` 复核时得出「`busy_timeout` 本来就是 5000、这条不成立」——**它那个驱动的默认值确实是 5000**,但本仓库用的是 `modernc.org/sqlite`,默认是 0。教训对双方都成立:验证「没有 X 就会退回默认值」这类命题,**必须去量那个默认值,而且要用本仓库真正在用的驱动量**。上表的「对照」那一列就是为此存在的。

**做什么**:把 pragma 移进 DSN(`file:...?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(1)`),或改用 `connector`/`ConnectHook`,确保**池里每条连接**都带上。`journal_mode` 留在原处也行,但一起搬更一致。

**验收命令**:
```bash
go test ./internal/storage/... -race -count=1
# 新测试(必须带正对照,否则证明不了它会动):
#   在同一个 *sql.DB 上用 db.Conn() 同时持有 >=2 条连接,各读
#   PRAGMA busy_timeout / foreign_keys / synchronous
#   修好后:每条连接都必须是 5000 / 1 / 1
#   正对照:改回对池 Exec 的写法,断言出现 5000/0 这种不一致
```
**依赖**:— · **来源**:FU-5(PR #35 评审)+ 本仓库实测修正

### M2-F5-T2 · `SaveOutbox` 并发写不安全 · M · READY
**为什么**:[#26](https://github.com/iDoris-ai/Hyphae/issues/26)。两种失败模式,M1.5 期间用 20 个并发 goroutine 实测复现过:
1. **丢更新** — `LoadOutbox → 内存改 → SaveOutbox` 是无锁读-改-写,20 个并发操作里常有 18-19 个被静默撤销。
2. **文件损坏** — 临时文件名固定 `outbox.json.tmp`,两个并发写者 `O_TRUNC` 到同一 inode 按各自 offset 写,`rename` 发布的已是损坏 JSON。PR #33 在等价实现上实测 3000 次并发对中 **11%** 产出不可解析文件。

**做什么**:
- (a) 改用 `os.CreateTemp` —— 消除模式 2,不改磁盘格式。**照抄 `internal/identity/keystore.go` 的 `SaveKeyStore`**,PR #33 已经在那边做完并配了并发测试。
- (b) 给 load-modify-save 关键区加真正互斥(flock 锁文件,或把写路由到 daemon 持有的 channel/actor)—— 才能解决模式 1。**(a) 单独做解决不了 (b)。**

**验收命令**:
```bash
go test ./internal/messaging/... -race -count=1 -run 'Outbox'
# 新测试:N 个并发 goroutine 各自 load→改一条→save,
# 断言 (1) 最终文件可解析 (2) N 个改动全部存活(不丢更新)
```
**依赖**:M2-F5-T1(否则并发测试会被 `busy_timeout=0` 导致的 `database is locked` 干扰,难判是不是真修好了) · **来源**:#26 / FU-2

### M2-F5-T3 · outbox 重试把密文当明文存 · S · READY
**为什么**:`internal/messaging/outbox.go:281` 的重试调 `StoreOutgoingMessage(&event, entry.RecipientNpub, event.Content, true)`,而 `event` 是从 `entry.EventJSON` 反解出来的**已加密(可能还 zstd 压缩过)**事件——所以 `event.Content` 是密文,被当 `plaintext` 参数传进去。

单独发生时是脏数据;若 daemon 已先存过该 id 的解密明文,**重试会用密文覆盖它**。PR #35 的 UPSERT 守卫挡不住(它只挡空值,密文非空),这一点由 `TestPlaintextGuardDoesNotCoverNonEmptyOverwrite` 钉住。

**做什么**:在源头修。重试路径要么解密后再存,要么传空 plaintext 让 UPSERT 的保留分支生效。**修好后把那条 `TestPlaintextGuardDoesNotCoverNonEmptyOverwrite` 删掉**(它的注释里写了这句),不要把它改弱。

**验收命令**:
```bash
go test ./internal/messaging/... ./internal/storage/... -race -count=1
# 新测试:daemon 先存解密明文 → 走重试路径再存同一 id → 断言明文没被密文覆盖
```
**依赖**:— · **来源**:FU-3(PR #35 评审)

### M2-F5-T4 · `group_messages` 仍在用 `INSERT OR REPLACE` · S · READY
**为什么**:`internal/group/db.go:319`,schema 形状与 `messages` 相同(`id` PK + `event_id` UNIQUE)。PR #35 修掉的 delete-then-insert 清空列问题**同样适用**——group 消息没有 `is_incoming`,但「第二次写入清空 plaintext」这条成立。**即那个修复在全仓范围内只做了一半。**

**做什么**:照 `internal/storage/message.go` 的 UPSERT 改,包括空值保留守卫;不需要 `is_incoming` 的单调合并。

**验收命令**:
```bash
go test ./internal/group/... -race -count=1
# 新测试:先写带 plaintext 的 group 消息,再写同 id 不带 plaintext 的 → 断言 plaintext 还在
```
**依赖**:— · **来源**:FU-7(PR #35 评审)

### M2-F5-T5 · 给 behavior 信封分配独立 kind · S · READY
**为什么**:[#27](https://github.com/iDoris-ai/Hyphae/issues/27)。`internal/messaging.AgentKind` 和 `internal/profile.ProfileKind` **都是 30078**,靠各自的 `d`/`c` tag 区分——是巧合不是设计。M2 本来就要重新定义 kind/tag 约定,这是成本最低的时机;越晚改,已发布事件的兼容包袱越重。

**做什么**:给新 behavior 信封分配独立 kind,并确认不破坏已发布事件的可读性(标准客户端仍读得到旧的 30078 消息)。

**验收命令**:
```bash
go test ./internal/behavior/... ./internal/messaging/... ./internal/profile/... -count=1
# 外加第 2 道门(本地真实 relay):
go run scripts/minirelay.go &
./bin/hyphae agent msg --from alice --to bob --content x --relay ws://localhost:7777
./bin/hyphae behavior register --mode simple --relay ws://localhost:7777
# 核对两者落在不同 kind 上,且旧 30078 消息仍可被标准客户端查到
```
**依赖**:— · **来源**:#27

### M2-F5-T6 · daemon 自动回复把发布结果传进了 `isEncrypted` · XS · READY
**为什么**:`internal/daemon/daemon.go:514` 的 `StoreOutgoingMessage(event, toNpub, replyText, success)` —— `success` 是「relay 发布是否成功」,却被传进 `isEncrypted` 参数。后果:自动回复发布失败时,一条实际加密的消息被记成 `is_encrypted=0`。看起来就是参数写串了。

**验收命令**:
```bash
go test ./internal/daemon/... -race -count=1
# 新测试:发布失败路径下断言存下来的行 is_encrypted 反映真实加密状态,而非发布结果
```
**依赖**:— · **来源**:FU-6(PR #35 评审)

---

## M2-F1 · Behavior 信封编解码器

### M2-F1-T1 · 信封编解码器 · M · BLOCKED(等 M2-F5-T5)
新包 `internal/behavior/`,实现信封编解码:`["c","agent-v2"]` / `["b","<behavior>"]` / `["z","zstd"]` tag + zstd 压缩 content(复用 `pkg/compress`)。`pkg/types/` 新增 `Behavior`、`BehaviorEnvelope`。

**先定 kind 再写编解码器**,否则要返工——所以卡在 M2-F5-T5 上,而不是并行。

**验收命令**:
```bash
go test ./internal/behavior/... ./pkg/types/... -race -count=1
# 覆盖:zstd 往返、tag 解析、未知 behavior 的降级、畸形/截断 content 不 panic、空 tag / 重复 tag 边界
```

---

## 阻塞中(BLOCKED — 等人类决策,不由自动化代拍)

### M1.5-F4-T1 · 创建 `relay-khatru` fork 仓库
**卡在**:`protocol-v2.md` §10 的 **D3** —— fork 放 `AuraAIHQ/relay-khatru` 还是 `iDoris-ai/relay-khatru`?仓库已改名到 `iDoris-ai/Hyphae`,倾向后者,但这是组织决策。
**为什么不自动化**:`specs/m1.5/README.md` 明确把「建 GitHub 仓库、配 CI/发布」列为一次性人类基建操作。
**顺带要一起定**:`scripts/deploy-relay.sh` 的 `local`/`tunnel` 模式对着真实 khatru 上游**跑不起来**——脚本硬编码 `examples/basic`,上游已拆成 `basic-badger`/`basic-sqlite3`/...。fork 建好后指向 fork 根目录,这个问题大概率自然消失;若短期不建,则需要把子路径做成 `RELAY_EXAMPLE_DIR` 环境变量。
**不阻塞 M2。**

### M2.5-F0-T1 · 拍板 D1/D2/D4/D5
M2.5 开工前必须定:D1 邀请券是否上链(ERC-721)、D2 漂流瓶向量算法(倾向 sentence-transformers ONNX)、D4 花名册查询是否要 NIP-42 auth、D5 1对1 通道协议格式。**定不下来就写不出 M2.5 任务的可验证验收标准**,所以卡在这里而不是硬拆。

---

## 待细化(等 M2-F1-T1 落地后再拆)

内容在 `roadmap-v2.md` M2 一节已写清,但要等信封定型才能写出精确验收:

| Task | Feature | 说明 |
|---|---|---|
| M2-F2-T* | M2-F2 | 四种 behavior 收发,预计 1 behavior = 1 Task |
| M2-F3-T1 | M2-F3 | `tip`/`drifting-bottle` payload schema(**只定义,不实现执行**) |
| M2-F4-T1 | M2-F4 | Owner-attestation:`["auth","<owner-pubkey>","<conditions>","<sig>"]` 等价设计,对接 AirAccount。只做结构 + 签名/校验函数,**不接真实 SDK**(那是 M5) |
| M2-F6-T1 | M2-F6 | 标准 Nostr 客户端兼容性验证(`protocol-v2.md` §11 承诺的验收测试) |
| M2-F6-T2 | M2-F6 | **与 Agent24 跨仓联调**(第 3 道门),走 Seeder `Cooperation-Center` 的 `repo:speaker` 任务 |

---

## 三道验收门(从 M2 起每个里程碑都要过)

来自 [`../milestones/testing-integration-plan.md`](../milestones/testing-integration-plan.md) §1,**第 3 道不能跳过**:

| # | 门 | 谁做 |
|---|---|---|
| 1 | 单元测试 `go test ./...` + 新功能配新测试 | 实现者 |
| 2 | 本地真实网络(起 relay,跑真实 CLI,肉眼核对 relay 上的原始事件) | 实现者 |
| 3 | **跨仓联调**——真实外部消费者(Agent24)拿真实二进制跑一遍契约 | 双方工兵 |

CC-82 的教训:那次挖到的 4 个 bug 里,只有「`--json` 没接」是读代码能发现的;`d` 标签覆盖要真实 NIP-01 relay 才测得出;`id` 编码损坏和 `from` 截断是对端拿真实二进制跑自己的 bridge 才现形的。**任何一边单独测都测不出来。**

Agent24 交还 `StoreMessage` 补丁(→ PR #35)是同一条经验的又一次印证:那个缺陷是**下游消费者**在自己的活性探针上撞出来的,本仓库的单元测试全绿、也没人读代码读出来。
