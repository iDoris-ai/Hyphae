# PR #138 对抗性复核（外部评审前自检）· 2026-10-08

- 目标：分支 `codex/m1-group-state-20261007`，复核基准 head **`1d4ea36`**
- 复核期间分支被推进到 `bba37cd`（`style: gofmt-clean groupchat test files`，只删了两个测试文件里的空行，生产代码与 `1d4ea36` 逐字节相同），所以本报告的结论同样适用于 `bba37cd`。
- 复核方式：逐行阅读 `internal/groupchat/store.go` 和 `messages.go`（PR 新增的生产代码共 1229 行）。所有怀疑点都在 `1d4ea36` 的 detached worktree 里写复现测试实际跑过，测试文件全文见附录 A，没有提交到任何分支。
- 来源说明：本报告由本地模型（Claude）做自检，不是 Codex/外部评审。自检比外部对抗评审弱，最可能漏掉的是：跨 PR 的集成语义（#147 的 fanout/outbox 还没有实现代码）、协议层 `protocol.go`/`verified.go`（已在 main，不在本 diff 内）的加密/签名细节，以及 SQLite 在真实多进程负载下更罕见的锁模式。

## 结论速览

| 级别 | 数量 | 编号 |
|---|---|---|
| **block** | 1 | B1 |
| **review** | 5 | R1–R5 |
| **info** | 8 | I1–I8 |

### 三个已修阻塞项的判定

| 阻塞项 | 判定 | 缺什么 |
|---|---|---|
| ① NULL `event_id` 扫描崩溃（`messages.go:137` 加 `COALESCE`） | **单线程下修对了；并发下的幂等没有成立** | 修复只解决了 `Scan` NULL 的崩溃。并发投递同一条消息时，`storeMessage` 先 `SELECT` 再 `INSERT`，走的是 deferred 事务，在生产配置（WAL + 连接池）下会返回 `SQLITE_BUSY` / `SQLITE_BUSY_SNAPSHOT(517)`，而不是幂等的 `(false, nil)`。数据层面没有出现重复行（有主键兜底），但幂等契约破了。见 R1，已复现：240 次并发投递里 9–12 次报错。 |
| ② 禁止「部分 activation 后取消」（`store.go:635-650`、`713-728`） | **修得不完整** | 新加的守卫只能看到**已经** `MarkActivationQueued` 的邀请。从 `ReceiveAcceptance` 把 activation envelope 交出去（`store.go:464-474`），到调用方签名、入 outbox、调用 `MarkActivationQueued` 之间有一个窗口（崩溃或并发 `CancelGroup` 都会落进来），这个窗口完全没有挡住：取消成功，已入队的 activation 照样送达，结果创建者是 `cancelled`、被邀请者是 `active`，状态永久分叉。见 B1，已复现。入口覆盖方面：进入 cancelled 的 4 条路径（`CancelGroup`、`ReceiveDecline`/`ReceiveCancel` → `cancelFromInvite`、`DeclineInvite`）都有检查，问题出在检查依据的「已标记」这个信号本身不够。 |
| ③ `ReceiveDecline` 要求 `Pending`（`store.go:729-741`） | **对单设备修对了；状态机还有别的未挡迁移** | 完整迁移表见下节。还没挡住的有：活跃群对重放或重新签名的 accept 每次都重新吐出整批 activation（R3）；同一把密钥的两台设备一台接受、一台拒绝，创建者会静默丢掉拒绝（R4）；拒绝和创建者取消交叉时，拒绝一方报错而不是幂等（I4）；`MarkActivationQueued` 不校验调用者是不是创建者（R5）。 |

### 拆分方案（本轮不执行）

见文末「规模与拆分」：**5 块，每块 ≤ 265 行**；或者走「特殊 500」预算，拆成 **3 块（约 500 / 434 / 230 行）**。

---

## 状态迁移表（逐条核对）

群状态 `G`：`pending / activating / active / left / cancelled`。邀请状态 `I`：`pending / accepted / declined / active / cancelled`。

| 入口 | 所在侧 | 允许的源状态（代码实际行为） | 目标状态 | 结论 |
|---|---|---|---|---|
| `CreateGroup` `store.go:148` | 创建者 | （无） | G=pending, I=pending | ok |
| `ReceiveInvite` `:230` | 被邀请者 | 群不存在；或群存在且 ∉{left, cancelled}，且名字/创建者/roster/hash 全部一致 | G=pending, I=pending | ok；同 invite_id 重放返回 false |
| `AcceptInvite` `:311` | 被邀请者 | G ∈ {pending, activating}，I ∈ {pending, accepted} | I=accepted | ok（被邀请者侧永远不会进入 activating） |
| `DeclineInvite` `:365` | 被邀请者 | I=pending 且 G=pending；或 (declined, cancelled) 幂等 | G=cancelled, I=declined | 已接受的人不能反悔（产品决定，I5） |
| `ReceiveAcceptance` `:414` | 创建者 | G ∉ {cancelled, left}，I ∈ {pending, accepted, active} | I=accepted；全员 accepted 时 G=activating | **G=active 时也返回整批 activation → R3** |
| `MarkActivationQueued` `:479` | 创建者（**未校验**） | G ∈ {activating, active}，I ∈ {accepted, active(同 event)} | I=active；全员 active 时 G=active | 没有 creator 校验 → R5 |
| `ReceiveActivation` `:552` | 被邀请者 | G ∉ {left, cancelled}，I ∈ {accepted, active} | G=active, I=active | ok；但见 B1，它能把一个创建者已经取消的群激活 |
| `ReceiveDecline` → `cancelFromInvite(declined)` `:605/697` | 创建者 | I=pending 且 G=pending，且没有 active 邀请或成员；(declined, cancelled) 幂等 | G=cancelled，邀请全部 cancelled，本邀请 declined | ③ 已修；**多设备 R4、和取消交叉 I4** |
| `CancelGroup` `:618` | 创建者 | G ∈ {pending, activating}，且没有 **已标记的** active 邀请或成员 | G=cancelled, I=cancelled | **B1：只看到已标记的 activation** |
| `ReceiveCancel` → `cancelFromInvite` `:685` | 被邀请者 | G ∉ {active, left}，没有 active 邀请；G=cancelled 时幂等 | G=cancelled, I=cancelled | ok |
| `LeaveGroup` `:798` | 双方 | G=active；left 幂等 | G=left | ok |

---

## Findings

### B1 [block] 交出 activation 到 `MarkActivationQueued` 之间的窗口仍可取消，导致创建者和被邀请者状态永久分叉

- **位置**：`internal/groupchat/store.go:632-650`（`CancelGroup` 守卫），`store.go:464-474`（`ReceiveAcceptance` 交出 activation 时没有在库里留下任何「已发出」标记），`store.go:499-501`（取消后再 `MarkActivationQueued` 只会报错）。
- **触发条件**：全员 accept 后，`ReceiveAcceptance` 返回 activation 列表。调用方按 #147 契约先签名、入 outbox，再调 `MarkActivationQueued`。在这两步之间，只要满足下面任意一条：(a) 进程崩溃，重启后用户执行取消；(b) TUI 和 daemon 两个进程并发，一个在发 activation，另一个执行 `CancelGroup`；(c) 调用方先取消、后标记——`CancelGroup` 数到的 `InviteActive` 都是 0，于是取消**成功**并生成取消通知。但 Bob 的 activation 已经在 outbox 或 relay 里了。
- **影响**：Bob 先收到 activation，进入 active；随后收到的 cancel 因为 `group.State == StateActive` 被拒（`store.go:710`）。最终创建者是 `cancelled`、Bob 是 `active`，Bob 会往一个创建者已经不认的群里发消息。如果顺序反过来（先 cancel 后 activation），activation 会被拒，状态一致。所以分叉只取决于网络顺序，而这正是 ② 要防的「部分激活」，只是换了个时间窗。
- **建议修法**（二选一）：
  1. 最省事：`CancelGroup` 在 `G == activating` 时直接拒绝，即创建者一旦交出 activation 就算承诺了激活。`activating` 卡住时的恢复走「重发同一个 activation」，不走取消（#147 的 `group retry` 已经覆盖这种重发）。`cancelFromInvite` 的 declined 分支已经要求 `G == pending`，两者语义一致。
  2. 在 `ReceiveAcceptance` 的同一个事务里，把每个邀请写成 `activation_issued`（或者落一行 fanout `prepared`，对齐 #147 的 T1），守卫改为检查它，而不是只看 `InviteActive`。
  另外补一条测试：交出 activation、签名、不标记、然后 `CancelGroup`，必须返回 `ErrInvalidTransition`。
- **可复现**：是。`TestRepro_F1_CancelInQueuedButUnmarkedWindow` 输出 `DIVERGENCE creator=cancelled bob=active`。
  ```bash
  git worktree add --detach /tmp/h138 1d4ea36 && cp <附录A> /tmp/h138/internal/groupchat/zz_adversarial_repro_test.go
  cd /tmp/h138 && go test ./internal/groupchat -run TestRepro_F1 -count=1 -v
  ```
- **现有测试为什么没抓到**：`TestCancelGroupRejectedAfterPartialActivation`（`store_test.go:320`）只覆盖「已调用 `MarkActivationQueued`」的情况。

### R1 [review] 并发投递同一条消息时幂等不成立，返回 `SQLITE_BUSY`（已修阻塞项 ① 的并发面）

- **位置**：`internal/groupchat/messages.go:111-171`（`db.Begin()` 开的是 deferred 事务，先读后写）。
- **触发条件**：生产用的 DB 来自 `internal/storage/db.go:36-68`，是 WAL + `busy_timeout(5000)` 的**无上限连接池**。同一个 event 被并发交给 `ReceiveMessage`。#147 规定 TUI（R1）和 daemon（R2）**各自**把 reserved 消息送进同一个 `groupchat.Store`，两个进程订阅同一批 relay，这种并发在生产中一定会出现。
- **影响**：deferred 事务从读锁升级为写锁时，如果别人已经提交，SQLite 直接返回 `SQLITE_BUSY_SNAPSHOT (517)`，`busy_timeout` 对这种情况不起作用。调用方拿到的是错误，而不是 `(false, nil)`。数据完整性仍然成立（主键和 `UNIQUE(event_id)` 兜底，没有出现重复行），但按 #147 的「不得视为已处理」语义，这类错误会触发告警或重试噪音；如果调用方把错误当成终态丢弃，这条消息在这个进程里就丢了，只能靠 relay 历史回放补回来。
- **建议修法**：`groupchat` 的写路径一律用 `BEGIN IMMEDIATE`。modernc 驱动支持 DSN 参数 `_txlock=immediate`，已在 `modernc.org/sqlite@v1.60.1/sqlite.go:395` 确认。为避免影响 legacy 表，可以给 `groupchat.Store` 单独开一个带 `_txlock=immediate` 的 `*sql.DB`，或者在 Store 内用 `conn.ExecContext("BEGIN IMMEDIATE")` 手动开事务。也可以改成 `INSERT … ON CONFLICT DO NOTHING` 后再比对。
- **可复现**：是。`TestRepro_F2_ConcurrentSameMessage`（30 轮 × 8 个 goroutine）：
  - 默认 DSN：`F2 concurrent idempotent-delivery errors: 12 / 240`，第二次跑是 `9 / 240`
  - 加 `_txlock=immediate`：`0 / 240`（修法已验证）
  ```bash
  cd /tmp/h138 && go test ./internal/groupchat -run TestRepro_F2 -count=1 -v
  REPRO_TXLOCK=immediate go test ./internal/groupchat -run TestRepro_F2 -count=1 -v
  ```

### R2 [review] 并发的最后两个 acceptance 中一个报 BUSY，群会卡在 pending

- **位置**：`internal/groupchat/store.go:422-474`（`ReceiveAcceptance`：读邀请 → 写 → `COUNT` → 写群状态，全在一个 deferred 事务里）。
- **触发条件**：Bob 和 Carol 的 accept 几乎同时到达创建者（同一个 relay 批次或两个进程），走生产 DSN。
- **影响**：一个返回 `database is locked (5)` 或 `(517)`，另一个成功但看到的还不是全员 accepted，所以不会返回 activation。如果失败的那一个不被重新投递，群会永远停在 pending。没有观察到写偏斜，SQLite 的写串行化挡住了，问题出在活性。
- **建议修法**：同 R1（`BEGIN IMMEDIATE`），并且在 #147 的路由层把 `SQLITE_BUSY` 归为 `Transient`。
- **可复现**：是。`TestRepro_F3_ConcurrentAcceptances`：默认 DSN 下 `errors 5/60, rounds with no activation batch 5/30`（第二次跑是 `2/60`、`2/30`）；加 `_txlock=immediate` 后是 `0/60`、`0/30`。

### R3 [review] 活跃群收到任意 accept（重放或重新签名）都会重新吐出全员 activation

- **位置**：`internal/groupchat/store.go:446-470`。`invite.state == InviteActive` 被当作合法状态放行，而 `G == active` 时无条件调用 `activationEnvelopes`。
- **触发条件**：群已经 active，任意成员重放自己的旧 accept event，或者用自己的私钥签一个新的 accept（成员本来就有这个能力）。
- **影响**：创建者每收到一次就得到 N 个 activation envelope。按 #147 的 T1，它们会进入 fanout，然后签名、入队、发给**所有**成员。被重新签名的 activation 有新的 event ID，`MarkActivationQueued` 对已经 active 的邀请会返回 `ErrProtocolMismatch`（`store.go:519-521`），于是调用方先把消息发了出去，之后才拿到错误。任意一个成员都能放大创建者的出站流量。
- **建议修法**：只在 `G == activating` 时返回 activation，并且只针对 `I == accepted`（还没 active）的邀请。`G == active` 时返回 `(nil, nil)` 作为幂等。
- **可复现**：是。`TestRepro_F4_AcceptReplayReturnsFullActivationBatch` 连续 3 次重放，每次都 `Len == 2`。

### R4 [review] 多设备场景下，阻塞项 ③ 让「一台接受、一台拒绝」静默分叉

- **位置**：`internal/groupchat/store.go:739-741` 和 `store.go:390-401`。
- **触发条件**：Bob 的同一把密钥在两台设备上，各自有独立的 DB（Hyphae 的身份可以导出导入）。设备 A 接受，设备 B 拒绝；创建者先收到 accept，再收到 decline。
- **影响**：创建者把 decline 当作 `ErrInvalidTransition` 丢掉。设备 B 本地已经是 `cancelled`，之后收到 activation 也会因为 `G == cancelled` 被拒（`store.go:573`）。结果创建者和设备 A 都认为 Bob 在群里，设备 B 永远看不到这个群，也没有任何错误提示给 Bob。
- **建议修法**：至少写进文档，说明 M1 假设单设备。或者在创建者侧，当 `G == pending` 且 `I == accepted` 时接受 decline 并取消（③ 真正要挡的是 activating/active 之后的拒绝）；也可以在被邀请者侧，`DeclineInvite` 之后收到 activation 时返回一个明确的「本设备已拒绝」错误。
- **可复现**：是。`TestRepro_F6_MultiDeviceAcceptThenDecline`。

### R5 [review] 测试的连接配置掩盖了生产的并发面

- **位置**：`internal/groupchat/store_test.go:35` 用了 `db.SetMaxOpenConns(1)`，而生产 `internal/storage/db.go:36` 的连接池不设上限。
- **影响**：单连接把所有事务串行化，R1、R2 这一类问题在现有测试里**不可能**出现，`go test -race` 全绿也说明不了任何问题。这不是「为了过测试而改松」，但效果上等于测试比生产更宽松。
- **建议修法**：加一组使用生产 DSN（可以复用 `storage.sqliteDSN`，或者把它导出）的并发测试，把附录 A 的 F2、F3 断言改成 `errors == 0`。
- **可复现**：是（同 R1、R2）。

### I1 [info] 死代码计入了规模

`store.go:974-1000` 里的 `normalizeMemberList`、`canonicalLocalNpub`、`rosterJSON`、`membersFromJSON` 在仓库内没有任何调用方（grep 计数为 0，`normalizeMemberList` 只被 `rosterJSON` 调用），共约 27 行，可以直接删。

### I2 [info] `GroupMessages` 的截断是死代码

`messages.go:95-97`：SQL 已经 `LIMIT ?`，`len(messages) > limit` 永远不成立。

### I3 [info] 两处断言偏弱（但当前确实打到了正确的错误）

`store_test.go:186` 和 `store_test.go:293` 只用了 `require.Error`。我加探针确认过：186 返回的是 `ErrProtocolMismatch`，293 的 6 个子用例都是 `verified encrypted Agent event is required`。将来如果有更早的无关错误（比如 DB 错误），这两个测试也会通过。建议改成 `ErrorIs` 或 `ErrorContains`。检查了 PR 内的全部 3 个 commit，**没有任何删除或放宽的断言**，生产校验也没有被改松（`c8bcfcd` 只是把 `validEventID` 内联成等价的 `len == 64 && isLowerHex`）。

### I4 [info] decline 和创建者 cancel 交叉时，decline 一方报错而不是幂等

`store.go:739`：创建者已经 `CancelGroup`（I=cancelled），之后迟到的 decline 返回 `ErrInvalidTransition`。最终状态一致，但在 #147 的路由层会变成一条错误。建议把 `I == cancelled && G == cancelled` 当作幂等。已复现：`TestRepro_F5`。

### I5 [info] 已接受的邀请不能再拒绝

`store.go:390`：`DeclineInvite` 要求 `I == pending`。如果这是有意的产品决定，建议写进设计文档。

### I6 [info] 取消路径不更新 `groupchat_members.state`

`store.go:670-677`、`752-764`。目前所有读取都先检查群状态，没有实际后果；但成员表会残留 `accepted`/`pending`，将来如果有按成员状态做的统计，会出错。

### I7 [info] `AcceptInvite` 有一个跨身份的存在性旁路

`store.go:325`：`SELECT COUNT(*) … WHERE invite_id = ?` 不带 `local_npub`，会暴露「同一个 DB 里的另一个本地身份持有这个邀请」。只在本地发生，风险很低。

### I8 [info] 消息排序依赖发送者自报的 `created_at`

`messages.go:22-23` 用的是 `v.createdAt`（event 时间戳），成员可以伪造未来时间，把自己的消息一直钉在最底部。M1 可以接受，建议在 UI 层设一个上限（比如 `min(createdAt, now+skew)`）。

---

## 越权 / 身份（规则 S3：在执行动作处也要校验）

逐个入口核对，确认是否在**同一个事务里**校验了 creator、roster hash 和邀请绑定：

| 入口 | 签名者 / 收件人 | creator 绑定 | roster hash | 邀请绑定 |
|---|---|---|---|---|
| ReceiveInvite | ✅ sender=creator, recipient=invitee | ✅ 已有群逐字段比对 | ✅（Decode 内重新计算） | ✅ invite_id 重放比对 4 个字段 |
| ReceiveAcceptance | ✅ | ✅ `group.Creator==e.CreatorNpub` | ✅ `invite.hash` | ✅ `invite.invitee` |
| ReceiveActivation | ✅ | ✅ | ✅ 外加 `sameStrings(roster)`、`Name` | ✅ |
| ReceiveDecline / ReceiveCancel | ✅ | ✅（`cancelFromInvite`） | ✅ | ✅ |
| CancelGroup | 本地 | ✅ `group.Creator==localNpub` | 不适用 | 不适用 |
| **MarkActivationQueued** | 本地 | ❌ **不检查 `group.Creator==localNpub`** | 不适用 | 只检查 invite 属于该 group |
| AcceptInvite / DeclineInvite | 本地 | 不适用 | 不适用 | ✅ `invite.invitee==currentIdentity` |
| storeMessage | ✅ sender 是 active 成员，本地也是 active 成员 | 不适用（消息信封不带 creator/hash，这是协议设计，见 `protocol.go:236`） | 同左 | 不适用 |

`MarkActivationQueued` 缺 creator 校验这一项：在被邀请者侧，`G == active` 只能经由 `ReceiveActivation` 达到，而那时 `I` 已经是 active，`eventID` 也必须等于已存的值，所以**目前没有能触发的利用路径**。实际试过（附录 A 的 `TestRepro_MarkActivationQueuedAsInvitee`）：以 Bob 身份对 Bob 本地的群调用，pending 时返回 `ErrInvalidTransition`，active 时传入外来 event ID 返回 `ErrProtocolMismatch`，没有复现越权迁移。即便如此，它不符合 S3，建议补一行 `if creator != localNpub { return ErrIdentityMismatch }`。这一项计入 R 级审视，但不单独编号。

## 事务边界 / fail-open 扫描结论

- 所有状态写入都在单个事务里完成，`defer tx.Rollback()` 齐全，**没有发现部分落库**。
- 吞掉的错误只有一处：`store.go:238` 的 `rosterJSON, _ := json.Marshal(e.Members)`，`[]string` 不可能 Marshal 失败，不构成 fail-open。
- `UPDATE` 没有检查 `RowsAffected` 的地方（例如 `store.go:299`）：前面的 Decode 已经保证 invitee ∈ members，所以不会出现 0 行更新，不构成 fail-open。
- 读-改-写都在事务里，但用的是 **deferred** 事务。这是 R1、R2 的根因，也是本 diff 里唯一系统性的事务问题。
- `cancelFromInvite` 的幂等分支在 `Commit` 之后才用 `s.db` 读取取消通知（`store.go:735`），读的是已提交的数据，结果正确。

---

## 测试实际输出（head `1d4ea36`，在 detached worktree 里跑，没有混入复现测试文件）

机器负载很高（复核期间 `load averages: 85 → 137`，26 个用户）。

### `go test ./... -count=1`

```
ok  	github.com/iDoris-ai/hyphae/cmd/hyphae	24.994s
ok  	github.com/iDoris-ai/hyphae/internal/audit	3.972s
ok  	github.com/iDoris-ai/hyphae/internal/common	5.054s
panic: CLI cold-start preflight exceeded 15s after 15.001361s: context deadline exceeded; output=""
FAIL	github.com/iDoris-ai/hyphae/internal/daemon	21.365s
ok  	github.com/iDoris-ai/hyphae/internal/group	5.147s
ok  	github.com/iDoris-ai/hyphae/internal/groupchat	8.704s
--- FAIL: TestCheckPasswordCLIReadOnlySuccessAndOutputModes (15.60s)
        check_password_cli_test.go:152: CLI timed out after 8.001555625s: context deadline exceeded
FAIL	github.com/iDoris-ai/hyphae/internal/identity	48.213s
--- FAIL: TestAgentMsgContentFileCLI_EncryptedQueue (8.81s)
    agent_content_file_test.go:107: CLI timed out: context deadline exceeded
FAIL	github.com/iDoris-ai/hyphae/internal/messaging	45.871s
ok  	github.com/iDoris-ai/hyphae/internal/nostr	5.133s
ok  	github.com/iDoris-ai/hyphae/internal/profile	25.199s
ok  	github.com/iDoris-ai/hyphae/internal/relay	6.174s
ok  	github.com/iDoris-ai/hyphae/internal/relayconfig	17.614s
ok  	github.com/iDoris-ai/hyphae/internal/relayquery	23.544s
ok  	github.com/iDoris-ai/hyphae/internal/storage	16.003s
ok  	github.com/iDoris-ai/hyphae/internal/tui	16.945s
ok  	github.com/iDoris-ai/hyphae/internal/wireevent	14.158s
ok  	github.com/iDoris-ai/hyphae/pkg/compress	11.970s
ok  	github.com/iDoris-ai/hyphae/pkg/crypto	9.250s
ok  	github.com/iDoris-ai/hyphae/pkg/types	7.751s
ok  	github.com/iDoris-ai/hyphae/tests/contracts	7.026s
FAIL   (exit=1)
```

### `go test -race ./... -count=1`

```
ok  	github.com/iDoris-ai/hyphae/cmd/hyphae	15.730s
ok  	github.com/iDoris-ai/hyphae/internal/audit	2.770s
ok  	github.com/iDoris-ai/hyphae/internal/common	2.841s
ok  	github.com/iDoris-ai/hyphae/internal/daemon	86.177s
ok  	github.com/iDoris-ai/hyphae/internal/group	9.934s
ok  	github.com/iDoris-ai/hyphae/internal/groupchat	17.594s
--- FAIL: TestCheckPasswordCLIReadOnlySuccessAndOutputModes (14.02s)
FAIL	github.com/iDoris-ai/hyphae/internal/identity	107.009s
--- FAIL: TestAgentMsgContentFileCLI_EncryptedQueue (12.62s)
FAIL	github.com/iDoris-ai/hyphae/internal/messaging	68.688s
ok  	github.com/iDoris-ai/hyphae/internal/nostr	10.773s
ok  	github.com/iDoris-ai/hyphae/internal/profile	32.279s
ok  	(relay, relayconfig, relayquery, storage, tui, wireevent, pkg/*, tests/contracts 全部 ok)
FAIL   (exit=1)；没有任何 WARNING: DATA RACE
```

### 失败包串行复跑：`go test -p 1 -count=1 ./internal/daemon ./internal/identity ./internal/messaging ./internal/profile`

```
ok  	github.com/iDoris-ai/hyphae/internal/daemon	17.040s
ok  	github.com/iDoris-ai/hyphae/internal/identity	16.705s
ok  	github.com/iDoris-ai/hyphae/internal/messaging	10.520s
ok  	github.com/iDoris-ai/hyphae/internal/profile	7.962s
exit=0   (load averages: 132.28 137.60 121.34)
```

判读：失败的都是**本 PR 没有改动的包**，原因都是 CLI 子进程冷启动超时（8s/15s 的 deadline），串行复跑全部通过。此前在 #138 原 worktree 里跑的一次 `-race`，运行期间 HEAD 从 `1d4ea36` 变成了 `bba37cd`（只差 gofmt，生产代码相同），那次 20 个包全部 ok。`internal/groupchat` 在所有运行里都 ok，且没有数据竞争。结论：失败是机器负载造成的环境性 flake，不是 #138 引入的回归。不过「`go test ./...` 全绿」在高负载机器上本身不可复现，这一点值得在 CI 门禁里注意。

---

## 规模与拆分（本轮不执行）

生产代码：`store.go` 1009 行加 `messages.go` 220 行，共 1229 行。下面的行号以 `1d4ea36` 为准。

**推荐：5 块，每块 ≤ 300 行，依赖链为线性**

| # | 切片 | 内容（行号） | 生产行数 | 配套测试 |
|---|---|---|---|---|
| S1 | schema 与只读 API | 类型、错误、`NewStore`/`migrate`（1-142），`GetGroup`/`ListGroups`（828-862），`insertInvite`/`loadInviteGroup`/`loadGroup`（864-920），`envelopeFrom`/`valid`（942-972）；同时删掉死代码 974-1000 | ≈ 265 | `TestOpaqueIncomingStateGate*`、zero-value 子测试 |
| S2 | 建群与邀请（pending 阶段） | `CreateGroup`、`ReceiveInvite`、`AcceptInvite`、`DeclineInvite`（144-408） | ≈ 265 | 邀请 / 接受 / 拒绝相关子测试 |
| S3 | 激活 | `ReceiveAcceptance`、`MarkActivationQueued`、`ReceiveActivation`、`activationEnvelopes`（410-601、922-940），加上 `LeaveGroup`（796-826） | ≈ 242 | `TestExplicitFixedRosterAcceptanceActivationAndRestart`、leave |
| S4 | 取消 | `ReceiveDecline`、`CancelGroup`、`ReceiveCancel`、`cancelFromInvite`、`cancellationEnvelopes`（603-794） | ≈ 192 | decline / cancel / 部分激活（**B1 的修法应在这一块落地**） |
| S5 | 消息 | `messages.go` 全部，加 `sortGroupMessages`（1002-1009） | ≈ 230 | `messages_test.go`、R1/R2 并发测试 |

为什么沿这些缝切：`store.go` 的函数之间只通过 S1 的 loader、常量和 `inviteRow` 互相耦合，S2 到 S5 之间没有函数级调用（`cancellationEnvelopes`/`activationEnvelopes` 各自只被本块调用）。所以每块都能单独编译、单独测试。S3 和 S4 可以交换顺序。B1 修法 1（activating 禁止取消）只改 S4 的守卫一行；修法 2 会同时碰 S3 和 S4，那就把 S3、S4 合成一块（约 434 行，走特殊预算）。

**备选：3 块（使用「特殊 500」预算）**：A = S1+S2（约 530 行，删掉死代码后约 500）；B = S3+S4（约 434）；C = S5（约 230）。

测试同样沿这些缝切（`store_test.go` 401 行里的 helper `newMember`/`newThreeMemberGroup`/`activate` 放进 S1 或 S2 的测试文件）。文档不计入规模。

---

## 附录 A：复现测试（`internal/groupchat/zz_adversarial_repro_test.go`，**不提交**）

放进 `1d4ea36` 的 worktree 后运行 `go test ./internal/groupchat -run TestRepro -count=1 -v`。设置环境变量 `REPRO_TXLOCK=immediate` 可以验证 R1/R2 的修法。断言写的是**当前的错误行为**（全部 PASS 就说明问题存在）；修复后，应把 F1/F4/F6 的断言改成期望的正确行为，F2/F3 改成 `errors == 0`，再并入正式测试。


```go
package groupchat

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/iDoris-ai/hyphae/internal/common"
	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/require"
)

// F1: activation handed out + durably queued, but MarkActivationQueued not yet
// recorded (crash / concurrent cancel window). CancelGroup still succeeds, and
// the invitee activates anyway -> creator=cancelled, invitee=active.
func TestRepro_F1_CancelInQueuedButUnmarkedWindow(t *testing.T) {
	g := newThreeMemberGroup(t)
	var batch []Envelope
	for _, m := range []testMember{g.bob, g.carol} {
		acc, err := m.store.AcceptInvite(g.invites[m.npub].InviteID, m.npub)
		require.NoError(t, err)
		b, err := g.alice.store.ReceiveAcceptance(buildIncoming(t, m, g.alice, acc))
		require.NoError(t, err)
		if len(b) > 0 {
			batch = b
		}
	}
	require.Len(t, batch, 2)
	var bobAct Envelope
	for _, a := range batch {
		if a.InviteeNpub == g.bob.npub {
			bobAct = a
		}
	}
	// caller signs + durably queues Bob's activation (outbox), then cancel races in
	ev := testAgentEvent(t, mustEncode(t, bobAct), g.alice.sk, g.bob.sk, true)
	cancels, err := g.alice.store.CancelGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(t, err, "cancel is NOT blocked: guard only sees marked activations")
	require.Len(t, cancels, 2)
	err = g.alice.store.MarkActivationQueued(g.alice.npub, g.draft.Group.ID, bobAct.InviteID, ev.ID.Hex(), true)
	require.ErrorIs(t, err, ErrInvalidTransition)
	// outbox still delivers activation to Bob
	applied, err := g.bob.store.ReceiveActivation(mustVerify(t, ev, g.bob.sk))
	require.NoError(t, err)
	require.True(t, applied)
	var bobCancel Envelope
	for _, c := range cancels {
		if c.InviteeNpub == g.bob.npub {
			bobCancel = c
		}
	}
	err = g.bob.store.ReceiveCancel(buildIncoming(t, g.alice, g.bob, bobCancel))
	require.ErrorIs(t, err, ErrInvalidTransition, "cancel cannot undo Bob's activation")
	bg, _ := g.bob.store.GetGroup(g.bob.npub, g.draft.Group.ID)
	ag, _ := g.alice.store.GetGroup(g.alice.npub, g.draft.Group.ID)
	t.Logf("DIVERGENCE creator=%s bob=%s", ag.State, bg.State)
	require.Equal(t, StateCancelled, ag.State)
	require.Equal(t, StateActive, bg.State)
}

// F4: replaying (or freshly re-signing) an accept after the group is active
// returns the full activation batch every time.
func TestRepro_F4_AcceptReplayReturnsFullActivationBatch(t *testing.T) {
	g := newThreeMemberGroup(t)
	acc, err := g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	in := buildIncoming(t, g.bob, g.alice, acc)
	g.activate()
	for i := 0; i < 3; i++ {
		b, err := g.alice.store.ReceiveAcceptance(in)
		require.NoError(t, err)
		require.Len(t, b, 2, "active group re-emits activations for ALL invitees")
	}
}

// F5: decline crossing a creator cancel is reported as an error, not idempotent.
func TestRepro_F5_DeclineCrossingCancelErrors(t *testing.T) {
	g := newThreeMemberGroup(t)
	dec, err := g.bob.store.DeclineInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	_, err = g.alice.store.CancelGroup(g.alice.npub, g.draft.Group.ID)
	require.NoError(t, err)
	_, err = g.alice.store.ReceiveDecline(buildIncoming(t, g.bob, g.alice, dec))
	t.Logf("decline after cancel: %v", err)
	require.ErrorIs(t, err, ErrInvalidTransition)
}

// F6: same key, two devices. Device A accepts, device B declines. Creator
// silently drops the decline; device B can never activate.
func TestRepro_F6_MultiDeviceAcceptThenDecline(t *testing.T) {
	g := newThreeMemberGroup(t)
	bob2 := newMember(t)
	bob2.sk, bob2.npub = g.bob.sk, g.bob.npub
	_, err := bob2.store.ReceiveInvite(buildIncoming(t, g.alice, g.bob, g.invites[g.bob.npub]))
	require.NoError(t, err)
	acc, err := g.bob.store.AcceptInvite(g.invites[g.bob.npub].InviteID, g.bob.npub)
	require.NoError(t, err)
	_, err = g.alice.store.ReceiveAcceptance(buildIncoming(t, g.bob, g.alice, acc))
	require.NoError(t, err)
	dec, err := bob2.store.DeclineInvite(g.invites[g.bob.npub].InviteID, bob2.npub)
	require.NoError(t, err)
	_, err = g.alice.store.ReceiveDecline(buildIncoming(t, g.bob, g.alice, dec))
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func newPoolMember(t *testing.T) testMember {
	t.Helper()
	sk := nostr.Generate()
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(t.TempDir(), "g.db"))}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)"); if os.Getenv("REPRO_TXLOCK") != "" { q.Add("_txlock", os.Getenv("REPRO_TXLOCK")) }
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA journal_mode = WAL")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	require.NoError(t, err)
	return testMember{sk: sk, npub: common.EncodeNpub(sk.Public()), db: db, store: store}
}

func poolGroup(t *testing.T) *threeMemberGroup {
	g := &threeMemberGroup{t: t, alice: newPoolMember(t), bob: newPoolMember(t), carol: newPoolMember(t)}
	draft, err := g.alice.store.CreateGroup("planning", g.alice.npub, []string{g.bob.npub, g.carol.npub})
	require.NoError(t, err)
	g.draft = draft
	g.invites = map[string]Envelope{}
	for _, inv := range draft.Invitations {
		g.invites[inv.InviteeNpub] = inv
		target := g.member(inv.InviteeNpub)
		_, err := target.store.ReceiveInvite(buildIncoming(t, g.alice, target, inv))
		require.NoError(t, err)
	}
	return g
}

// F2: production-like pool (WAL + busy_timeout, no SetMaxOpenConns(1)).
// Concurrent delivery of the same message must be idempotent.
func TestRepro_F2_ConcurrentSameMessage(t *testing.T) {
	fails := 0
	for round := 0; round < 30; round++ {
		g := poolGroup(t)
		g.activate()
		msg := Envelope{Type: EnvelopeMessage, Version: Version, GroupID: g.draft.Group.ID, LogicalID: mustOpaque(t), Body: "hi"}
		in := buildIncoming(t, g.alice, g.bob, msg)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := g.bob.store.ReceiveMessage(in); errs <- err }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				fails++
				t.Logf("round %d: %v", round, err)
			}
		}
	}
	t.Logf("F2 concurrent idempotent-delivery errors: %d / 240", fails)
}

// F3: concurrent final acceptances on the creator.
func TestRepro_F3_ConcurrentAcceptances(t *testing.T) {
	fails, noBatch := 0, 0
	for round := 0; round < 30; round++ {
		g := poolGroup(t)
		ins := []VerifiedIncoming{}
		for _, m := range []testMember{g.bob, g.carol} {
			acc, err := m.store.AcceptInvite(g.invites[m.npub].InviteID, m.npub)
			require.NoError(t, err)
			ins = append(ins, buildIncoming(t, m, g.alice, acc))
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		total := 0
		for _, in := range ins {
			wg.Add(1)
			go func(in VerifiedIncoming) {
				defer wg.Done()
				b, err := g.alice.store.ReceiveAcceptance(in)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					fails++
					t.Logf("round %d: %v", round, err)
				}
				total += len(b)
			}(in)
		}
		wg.Wait()
		if total == 0 {
			noBatch++
		}
	}
	t.Logf("F3 concurrent acceptance errors: %d / 60, rounds with no activation batch: %d / 30", fails, noBatch)
}

// R5-side probe: MarkActivationQueued has no creator check. Invoke it as the
// invitee on the invitee's own store.
func TestRepro_MarkActivationQueuedAsInvitee(t *testing.T) {
	g := newThreeMemberGroup(t)
	// pending invitee group: must not move
	ev := mustOpaque(t) + mustOpaque(t)
	err := g.bob.store.MarkActivationQueued(g.bob.npub, g.draft.Group.ID, g.invites[g.bob.npub].InviteID, ev, true)
	t.Logf("invitee pending: %v", err)
	g.activate()
	err = g.bob.store.MarkActivationQueued(g.bob.npub, g.draft.Group.ID, g.invites[g.bob.npub].InviteID, ev, true)
	t.Logf("invitee active, foreign event id: %v", err)
	require.ErrorIs(t, err, ErrProtocolMismatch)
}
```
