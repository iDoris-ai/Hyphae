# M1 群 fanout + durable send-intent 实现拆分方案（P0-E）

状态：2026-10-08 规划文档，**不含实现代码**。实现者按本文的切片逐个开 PR；每片的范围、行数、依赖与验收以本文为准。
契约依据：[M1 群消息 fanout 与 inbound 路由契约](m1-group-fanout-contract-20261008.md)（下称「契约」）。本文发现的契约问题集中登记在第 6 节，**实现前先改契约**，不允许在实现 PR 里默默偏离。

## 0. 基线核对（origin/main = `6c5e751`，2026-10-08 核对）

| 事实 | 核对结果 | 对本方案的影响 |
| --- | --- | --- |
| #138 群状态层 | 已合入：`internal/groupchat/{store,messages,protocol,verified}.go`；写事务统一走 `beginImmediate()` | 可直接在其上加表与 Tx 变体 |
| G1：`storeMessage` 自开事务 | 仍是：`messages.go` `storeMessage(...)` 内部 `s.beginImmediate()`，无 Tx 变体 | 需要切片 S2 |
| G2：`MarkActivationQueued(..., queued bool)` | 仍是布尔形状（`store.go:538`） | 需要切片 S10b |
| #145 `VerifyAgentMessage` / `AgentRouteReservedGroup` | **不在 main**；PR #145 仍 OPEN、REVIEW_REQUIRED，且基于 #138 合并前的旧栈 | 发送侧不依赖它；契约里「通过 `VerifyAgentMessage`」的单元验收改用 main 上已有的 `groupchat.VerifyIncoming`（见 D1） |
| 收件守卫 | 已合入：`messaging.RejectReservedGroupPayload`（`agent.go:680`），被 `inbox_watch.go`、`daemon.go processIncomingEvent`、`AgentInboxCmd` 调用 | P0-E 不碰收件路径 |
| 恰一次：`enqueue` 先于 history | 已合入（`agent.go sendQueuedAgentMessage`） | 群 fanout 沿用「先持久化、后发布」 |
| outbox 状态硬编码 | `recordAttemptFailure` 写死 `"failed"`、`inspectAttemptQueue` / `GetPendingOutbox` 认 `"pending"`、`CleanupOutbox` 只保留 `"pending"` 或近期条目 | 群状态值必须让这些函数感知，否则群条目会被清理或被改成 DM 状态（见 D5） |
| `publishToRelays` | 返回 `bool`，**第一个 relay 成功即返回** | 无法如实填契约的 `relay_acks/relay_count/Relays`（见 D2） |
| `hyphae group` 命令 | **已被旧的 `internal/group` 占用**（create/list/add-member/remove-member/leave/delete/chat，本地元数据语义） | 契约的 `hyphae group send|retry` 撞名（见 D8，需要拍板） |
| 新协议 CLI | main 上**没有**任何命令调用 `groupchat`（create/accept/decline/cancel 均无 CLI） | 三人真实 E2E 必须等 CLI/TUI 线与 P0-F（见第 5 节） |
| 并行中的 PR #151 | OPEN、CHANGES_REQUESTED；改 `outbox.go`（新增 `AttemptSendWithKeyStore`）、`tui/offline_outbox.go`、`agent.go sendQueuedAgentMessage` | 与 S1/S5b/S8b 有文本冲突，顺序见第 2 节 |
| `test.sh` | 不跑 `./internal/groupchat` | S3 补一行 |

## 1. 硬约束（用户 2026-10-08 定）

1. **生产代码每 PR ≤300 行**（`*.go` 且非 `_test.go`；测试、文档、脚本不计）；特殊情况 ≤500，**不得超 500**。本方案所有切片均按 ≤300 设计，无特殊片。
   计数口径（新增 + 删除，保守）：
   ```sh
   git diff --numstat origin/main...HEAD -- '*.go' ':(exclude)*_test.go' | awk '{s+=$1+$2} END {print s+0}'
   ```
   实际超过 300 时，**不得**合并为特殊片了事：先按本文「拆分备选」再切。
2. 提 PR 前必须跑 `bash ~/Dev/tools/PR-daemon/scripts/pre-pr-check.sh --base origin/main`，**0 block**。
3. **实现者 ≠ 评审者**；评审走外部 PR-Daemon（GitHub 账号 `clestons`）。
4. **APPROVED 后立刻合并，不在已批准的 PR 上追加 commit。** 因此每片都必须能独立合入并保持 main 可用：
   - 新能力先以「未被调用的新函数 / 新表」形式落地，接线放在后面的片里；
   - 后片只能**新增**函数、在已预留的扩展点**加 case**，不重写前片已合入的逻辑（扩展点在各片「边界」里写明）；
   - 每片合入后 `go test ./...` 全绿，DM 行为不变。
5. 每片 PR 描述必须包含：本片行数（上面命令的输出）、本片定向验收命令及其输出、「本片不做」清单原文。

## 2. 切片表

符号均为计划新增或修改的 Go 符号；「行」= 预估生产代码行（新增+删除）。所有验收命令在仓库根执行，期望输出均为 `ok` 行（`-count=1` 禁用缓存）。

| 片 | 主题 | 文件 · 主要符号 | 行 | 依赖（须先合入） | 定向验收（命令 → 期望输出） |
| --- | --- | --- | --- | --- | --- |
| **S1** | 签名 event 构造收拢 | 新 `internal/messaging/agent_event.go`：`BuildAgentMessageEvent(senderSK, recipientPK, plaintext, createdAt) (*nostr.Event, error)`（加密→`CompressText`→标签→`ValidateAgentMessageEvent`→`Sign`）；`agent.go` `AgentMsgCmd` 加密分支改调它；`tui/offline_outbox.go` `sendQueuedMessage` 改调它 | ~115 | 无（#151 若先合，rebase 即可） | `go test ./internal/messaging ./internal/tui -run 'TestBuildAgentMessageEvent\|TestAgentMsgCmd\|TestSendQueuedMessage' -count=1` → 两行 `ok  github.com/iDoris-ai/hyphae/internal/...` |
| **S2** | G1：消息写入 Tx 化 | `internal/groupchat/messages.go`：`storeMessage` 拆为 `storeMessageTx(tx queryExecer, ...)` + 原签名薄包装；新增 `storeLocalMessageTx(tx, localNpub, groupID, logicalID, body, createdAt)`。外部 API（`StoreLocalMessage` / `ReceiveMessage`）签名与行为不变 | ~50 | 无 | `go test ./internal/groupchat -run 'TestStoreMessageTx\|TestStoreLocalMessage\|TestReceiveMessage' -count=1` → `ok  github.com/iDoris-ai/hyphae/internal/groupchat` |
| **S3** | fanout 表 + 状态机 + 报告 | 新 `internal/groupchat/fanout.go`：`fanoutSchema`（契约 3.1 原样 DDL，`Store.migrate` 追加一行调用）、`RecipientDeliveryState` 四常量、`RecipientDelivery`、`FanoutReport`、`FanoutKey{LocalNpub, GroupID, EnvelopeType, SendKey}`、`fanoutRank`、`transitionFanoutTx(tx, key, recipient, from, to, fields)`（带 `WHERE state=?` 的单调更新）、`LoadFanoutReport(key)`、`deriveFanoutState`；`messaging/queued_agent.go` 新增 issue 常量 `queue_missing` / `queue_duplicate` / `route_handler_missing`；`test.sh` 增加 `./internal/groupchat` | ~210 | 无 | `go test ./internal/groupchat -run 'TestFanoutTransitionMatrix\|TestFanoutReport' -count=1` → `ok  github.com/iDoris-ai/hyphae/internal/groupchat`（含契约第 5 节 4×4 穷举） |
| **S4** | T1：`PrepareMessageFanout` | 新 `internal/groupchat/fanout_prepare.go`：`(s *Store) PrepareMessageFanout(senderSK nostr.SecretKey, groupID, logicalID, body string, createdAt int64) (FanoutReport, error)`；内部通用 `prepareFanoutTx(tx, key, []fanoutTarget, createdAt)`（S10b 复用）。一个 `beginImmediate` 事务内：读 active roster → `storeLocalMessageTx` → `Encode` → 逐收件人 `BuildAgentMessageEvent` → INSERT `state=prepared` | ~170 | S1、S2、S3 | `go test ./internal/groupchat -run 'TestPrepareMessageFanout' -count=1` → `ok  ...internal/groupchat`（覆盖契约第 1 节全部单元断言，`VerifyAgentMessage` 换成 `groupchat.VerifyIncoming`，见 D1） |
| **S5a** | outbox 群路由原语 | `pkg/types/types.go` `OutboxEntry.Route`；新 `internal/messaging/outbox_group.go`：常量 `OutboxRouteGroup` / `OutboxStatusGroupPending` / `OutboxStatusGroupFailed`、`EnqueueGroupOutboxEntry(eventJSON, recipientNpub string, relays []string, maxRetries int) (entry types.OutboxEntry, existed bool, err error)`（在 `UpdateOutbox` 锁内按 event ID 幂等）、`RequeueGroupOutboxEntry(eventJSON, ...)`（锁内删旧 `group_failed` + 加新 `group_pending`）、`GetPendingGroupOutbox`、`GroupOutboxEntriesByEventID`、`validGroupOutboxEntry`（Route⇔Status 不变量）；`outbox.go` 中 `recordAttemptFailure` / `inspectAttemptQueue` / `CleanupOutbox` 改为按 route 取 pending/failed 状态值；`outbox_commands.go` `isFailedOrStuck` 同步 | ~170 | 无（与 #151 有文本冲突，见第 3 节） | `go test ./internal/messaging -run 'TestGroupOutbox\|TestCleanupOutboxKeepsGroupPending\|TestLegacyBinaryIgnoresGroupEntries' -count=1` → `ok  github.com/iDoris-ai/hyphae/internal/messaging` |
| **S5b** | `AttemptSendRouted` | `internal/messaging/outbox.go`：`GroupOutboxHandler` 接口（契约 3.4 原样三方法）、`OutboxHandlers`、`AttemptSendRouted(ctx, ob, entry, defaultRelays, dialTimeout, handlers)`；`attemptSend` 增加 handlers 参数并按 `entry.Route` 分派；`AttemptSend` 改为 `AttemptSendRouted(..., OutboxHandlers{})`；`SendResult` 增加 `Issue AgentMessageDeliveryIssue`（D11） | ~130 | S5a；**#151 已合或已关**（D13） | `go test -race ./internal/messaging -run 'TestAttemptSendRouted' -count=1 && go test ./internal/messaging ./internal/daemon ./internal/tui -count=1` → 四行 `ok`（第一行 messaging 定向，后三行 DM 回归） |
| **S6** | O1/T2 + handler（T3/T4）+ 手动重排队 | 新 `internal/groupchat/fanout_queue.go`：`(s *Store) EnqueuePrepared(key FanoutKey) (FanoutReport, error)`（O1→T2）、`(s *Store) RequeueFailed(key FanoutKey, recipient string) (FanoutReport, error)`（`failed→queued`，同 event、新 QueueID）、`FanoutOutboxHandler{store}` 的 `BeforePublish` / `MarkRelayAccepted`（T3）/ `RecordAttemptFailure`（T4）、`NewFanoutOutboxHandler(store)`；**扩展点** `onFanoutQueuedTx(tx, key, row) error`（本片只处理 `envelope_type='message'`，为 no-op） | ~240 | S3、S5a | `go test ./internal/groupchat -run 'TestEnqueuePrepared\|TestFanoutOutboxHandler\|TestRequeueFailed' -count=1` → `ok  ...internal/groupchat` |
| **S7** | 对账 `ReconcileFanout` ⚠️ | 新 `internal/groupchat/fanout_reconcile.go`：`(s *Store) ReconcileFanout(localNpub string) (FanoutReconcileReport, error)`、`FanoutReconcileReport`、纯函数 `decideReconcile(row fanoutRow, entries []types.OutboxEntry) reconcileAction`、执行器 `applyReconcileAction`；测试注入点 `var fanoutFaultHook func(point string) error`（生产为 nil） | ~200 | S6 | `go test -race ./internal/groupchat -run 'TestReconcileFanout\|TestDecideReconcile\|TestFanoutCrashPoints\|TestFanoutConcurrentRetry' -count=20` → `ok  ...internal/groupchat` |
| **S8a** | daemon + `storage outbox retry` 接线 | `internal/messaging/outbox_group.go`：`GroupOutboxProvider` 类型与 `SetGroupOutboxProvider(p)`（D7）；`outbox_commands.go` retry 改调 `AttemptSendRouted`；`internal/daemon/daemon.go` `processOutboxWithLogger`：每轮先调 provider 的 reconcile，再遍历 `GetPendingOutbox` ∪ `GetPendingGroupOutbox`；新 `internal/groupchat/outbox_provider.go` `NewOutboxProvider(openDB)`；`cmd/hyphae/main.go` 注册一行 | ~140 | S5b、S7 | `go test ./internal/daemon ./internal/messaging -run 'TestProcessOutboxGroup\|TestOutboxRetryGroup\|TestGroupOutboxProvider' -count=1` → 两行 `ok` |
| **S8b** | TUI 重试接线 | `internal/tui/offline_outbox.go` `retryPendingOutbox`：启动/每轮先 reconcile，群条目走 `AttemptSendRouted`（provider 同 S8a）；`belongsToCurrentIdentity` 接受 `group_pending` | ~70 | S5b、S7、S8a（只依赖其 `SetGroupOutboxProvider`） | `go test ./internal/tui -run 'TestRetryPendingOutboxGroup' -count=1` → `ok  github.com/iDoris-ai/hyphae/internal/tui` |
| **S9** | CLI `send` / `retry` / `status` | 新包 `internal/groupchatcli`（避免 `groupchat` 依赖 cli）：`SendCmd`、`RetryCmd`、`StatusCmd`、`renderFanoutReport(w, report, contacts)`（昵称/npub 前 16 位，终端控制字符转义）、退出码按契约第 2 节；`send` 流程 = `ReconcileFanout` → `NewOpaqueID` → `PrepareMessageFanout` → `EnqueuePrepared` → 逐条 `AttemptSendRouted` → `LoadFanoutReport`；命令挂载点按 D8 决议 | ~220 | S4、S5b、S7；**D8 已拍板** | `go test ./internal/groupchatcli -count=1` → `ok  github.com/iDoris-ai/hyphae/internal/groupchatcli`（含 1/2 部分发送退出码非零、`--json` 字段齐全、文案无 delivered/已送达/已读） |
| **S10a** | 控制 envelope 的 Tx 变体（纯重构） | `internal/groupchat/store.go`：`createGroupTx` / `acceptInviteTx` / `declineInviteTx` / `receiveAcceptanceTx` / `cancelGroupTx`（参数加 `tx`，原公开方法变薄包装，行为不变） | ~150 | 无（与 S3 仅 `migrate` 一处相邻，冲突可忽略） | `go test ./internal/groupchat -count=1` → `ok  ...internal/groupchat`（既有 606 行 store_test 全绿 + 每个 Tx 变体回滚后无行的新测试） |
| **S10b** | 控制 envelope 进 fanout + G2 | 新 `internal/groupchat/fanout_control.go`：`CreateGroupWithFanout`、`AcceptInviteWithFanout`、`DeclineInviteWithFanout`、`ReceiveAcceptanceWithFanout`、`CancelGroupWithFanout`（状态迁移与 `prepareFanoutTx` 同一事务，D9）；在 S6 扩展点 `onFanoutQueuedTx` 增加 `activate` case：T2 同事务内完成原 `MarkActivationQueued` 的激活逻辑（证据 = 该行 `event_id` + `queue_id`）；`MarkActivationQueued` 删除 `queued bool` 参数并改为校验 fanout 行（G2） | ~200 | S10a、S4、S6 | `go test ./internal/groupchat -run 'TestControlFanout\|TestActivationRequiresFanoutEvidence' -count=1` → `ok  ...internal/groupchat` |
| **E2E-A** | 发送侧真实 relay E2E（0 生产行） | `test_groupchat_fanout_e2e.sh` + `internal/groupchatcli/fanout_e2e_test.go`（`//go:build e2e`），alice/bob/carol 三个隔离 HOME，`wss://relay.aastar.io` | 0 | S8a、S9 | `bash test_groupchat_fanout_e2e.sh` → 末行 `PASS: 3/3 groupchat fanout e2e` |
| **E2E-B** | 三人完整收发 E2E（0 生产行） | 同脚本追加用例：邀请→接受→激活→三人互发→重启 walk | 0 | E2E-A、S10b、P0-F、CLI/TUI 线的 create/accept 命令 | `bash test_groupchat_fanout_e2e.sh --full` → 末行 `PASS: 6/6 groupchat fanout e2e` |

合计生产代码 ≈ 2,065 行，13 个代码 PR + 2 个纯测试 PR，**每片均 ≤300**。最紧的是 S6（~240）。

### 拆分备选（仅在实际超 300 时启用，事先不拆）

- S6 超出 → 拆为 S6a `EnqueuePrepared` + `RequeueFailed`，S6b `FanoutOutboxHandler`；S6b 只依赖 S6a。
- S4 超出 → 把 `prepareFanoutTx` 单独作为 S4a（无调用方，只有单测），S4b 才是 `PrepareMessageFanout`。
- S9 超出 → `StatusCmd` + `renderFanoutReport` 先行（只读），`SendCmd`/`RetryCmd` 后行。
- S10b 超出 → 按 envelope 方向拆：创建者侧（create/receiveAcceptance/cancel/G2）与受邀者侧（accept/decline）。

## 3. 顺序与并行

```text
波次1（5 片互不依赖，可同时开 PR）:  S1   S2   S3   S5a   S10a
波次2:  S4   ◀── S1 + S2 + S3
        S5b  ◀── S5a (+ #151 落定)
        S6   ◀── S3 + S5a
波次3:  S7   ◀── S6
        S10b ◀── S10a + S4 + S6
波次4:  S8a  ◀── S5b + S7
        S9   ◀── S4 + S5b + S7 + D8 拍板
        S8b  ◀── S5b + S7 + S8a
波次5:  E2E-A ◀── S8a + S9
之后:   E2E-B ◀── E2E-A + S10b + P0-F + CLI 线 create/accept

关键路径: S3 → S6 → S7 → S9 → E2E-A
```

- **可并行**：波次 1 的 S1、S2、S3、S5a、S10a 五片互不依赖；波次 2 的 S4、S5b、S6 互不依赖；波次 3 的 S7 与 S10b 互不依赖；波次 4 的 S8a 与 S9 互不依赖。
- **必须串行**（关键路径）：S3 → S6 → S7 → S9 → E2E-A。S5a → S5b 也是串行。
- **外部顺序**：S5b 必须在 #151 合入或关闭之后开工（两者都改 `attemptSend` 签名区域）；S1、S5a、S8b 与 #151 只有文本冲突，后合者 rebase。
- **为什么 S6 不依赖 S5b**：S6 的 handler 只需要方法签名与契约 3.4 一致；`var _ messaging.GroupOutboxHandler = (*FanoutOutboxHandler)(nil)` 编译期断言放在 S8a（那时接口已在 main），S6 不需要等 S5b。
- **为什么 S8b 依赖 S8a**：provider 注册函数 `SetGroupOutboxProvider` 只在 S8a 定义一次，避免两片各写一份。
- 合入后的 main 状态：S1–S7、S10a/S10b 合入后，新代码均无生产调用方，DM 行为不变；S8a/S8b 合入后重试方能处理群条目，但没有任何命令会产生群条目；S9 合入后群发送才对用户可见。任何时刻停在中间都是安全的。

## 4. 每片的边界（「本片不做」，原文抄进 PR 描述）

| 片 | 本片不做 |
| --- | --- |
| S1 | 不改 `--encrypt=false` 明文分支（继续内联构造）；不改 `daemon.buildAutoReplyEvent`（它有可注入的加密器）；不改 `sendQueuedAgentMessage` / `SendQueuedAgentMessage` 的发送与入队逻辑；不碰任何群代码 |
| S2 | 不加任何新表/新列；不改 `StoreLocalMessage` / `ReceiveMessage` 的签名与错误值；不修 G3（暂态错误区分归 P0-F） |
| S3 | 不写任何会发送或签名的代码；不改 #138 已有表；不实现 T1–T4，只提供单调迁移原语与只读报告；不改 `AgentMessageDeliveryState` 三值枚举 |
| S4 | 不入 outbox、不发布、不处理控制 envelope；同 logical ID 重入时**不重新签名**，只返回既有行；不生成 logical ID（调用方负责） |
| S5a | 不改 DM 条目的任何行为（DM 仍是 `pending`/`failed`/`sent`）；不实现分派与 handler；不改 `publishToRelays`；不改 `storage outbox list/clear` 的输出格式（只修 `isFailedOrStuck` 判定） |
| S5b | 不改 DM 分支的可观察行为（现有 messaging/daemon/tui 测试不得修改断言）；不接线任何重试方；不改 `publishToRelays` 返回值（D2） |
| S6 | 不发布（不调用 `AttemptSendRouted`）；不对账；控制 envelope 的激活副作用只预留扩展点，不实现；`RequeueFailed` 只接受 `failed` 行 |
| S7 | 不发布、不签名、不重建 event；`queued`+0 条目只标 `queue_missing`，**不自动重新入队**；孤儿条目不删除 |
| S8a | 不改 DM 重试的退避与日志格式；不新增 CLI 命令；provider 为 nil 时群条目只得到 `route_handler_missing`，不发送 |
| S8b | 不做群 TUI 视图、不展示逐收件人状态（归 P0-F/TUI 线）；只让 TUI 后台重试能推进群条目 |
| S9 | 不实现 create/accept/decline/cancel 命令（归 CLI/TUI 线）；不改旧 `internal/group` 命令语义；不做 TUI；不显示 delivered/已送达/已读 |
| S10a | 纯重构：不改任何公开方法的行为与错误值，不加表 |
| S10b | 不新增 CLI；不实现收件路由（P0-F）；不改 envelope 编码 |
| E2E-A/B | 不增加生产代码；如需测试辅助，只放在 `_test.go` 或 `tests/` 下 |

## 5. 风险最高、最可能被评审打回的一片：S7 `ReconcileFanout`

**结论**：S7 最可能被 `clestons` 打回；S5b 次之。

**为什么 S7 风险最高**

1. 它是唯一同时读 outbox JSON（文件锁）和写 SQLite（`BEGIN IMMEDIATE`）、并且与**另一个进程的重试循环并发**运行的代码（daemon 与 TUI 可同时在线，`group send` 也会先调用它）。两把锁不在同一事务里，任何「先读 outbox、再写 SQLite」都是 TOCTOU 窗口，对抗式评审一定会构造交错。
2. 契约 3.5 的决策表本身**不完整**（D4：`failed` 行遇到 `group_pending` 条目的情形缺失），按契约原样实现会在 `group retry` 与 reconcile 并发时把刚重排队的行误判或漏判。
3. 它的错误代价不对称：误判 `queue_missing` 只是降级；但误入队会制造 `queue_duplicate`，误删会丢掉唯一的发送证据，而且对账要求幂等、可反复运行。
4. 契约第 3 节验收要求「每个崩溃点一个用例」，崩溃点散落在 S4/S6/S5b 里，S7 是第一个能把它们串起来测的片，前面任何一片的边界错误都会在 S7 暴露并被归到 S7 头上。

**降低打回概率的做法（S7 的额外验收）**

1. **先改契约再开工**：D4、D6 两条修正先以文档 PR 合入，S7 的决策表引用修正后的版本。
2. **决策与执行分离**：`decideReconcile(row, entries) reconcileAction` 为纯函数；生成式测试枚举 `state ∈ {prepared,queued,relay_accepted,failed}` × `条目数 ∈ {0,1,2}` × `条目状态 ∈ {group_pending,group_failed,损坏}` × `QueueID 相同/不同`，每个组合断言动作，PR 描述附「表格行 → 测试名」对照。
3. **执行器写 SQLite 一律带 WHERE 前置条件**（复用 S3 的 `transitionFanoutTx`），0 行受影响时读回现状而非报错；入队只用 S5a 的锁内幂等 `EnqueueGroupOutboxEntry`，从不直接 append。
4. **崩溃点注入**：`fanoutFaultHook` 在 T1 后、O1 后、T2 后、P 后、T3 后、O3 后六处返回错误模拟中断；每处一个用例，断言契约 3.5 的终态，并断言**第二次** `ReconcileFanout` 的报告与第一次相同。
5. **并发用例**：同一临时 HOME、同一 SQLite 文件开两个 `Store`，goroutine A 跑 `ReconcileFanout`、goroutine B 跑 handler 驱动的 `AttemptSendRouted`（假 publisher），`-race -count=20`；不变量：每个 event ID 至多 1 个 outbox 条目、所有 event ID 等于 T1 值、状态等级不倒退、`messages`（DM）表 0 行。
6. 评审前自查：在 PR 描述里逐条回答「reconcile 与 `group retry` / daemon 重试 / `storage outbox clear` 并发时各会怎样」。

**S5b 为什么次之**：它改的是所有 DM 重试共用的 `attemptSend`，又与 #151 冲突。缓解：等 #151 落定后开工；DM 分支零断言改动；PR 中附 `go test ./internal/messaging ./internal/daemon ./internal/tui` 的前后对比输出。

## 6. 契约偏离登记（实现前先改契约，不在实现 PR 里默默处理）

| ID | 契约位置 | 问题（已对 main 核对） | 修正建议 | 需在哪片之前修正 |
| --- | --- | --- | --- | --- |
| D1 | 依据与基线、§1 验收 | #138 已合入、收件守卫已合入为 `RejectReservedGroupPayload`，契约仍写「open / 未推送 commit `1cf7bb3`」；#145 未合入，`VerifyAgentMessage` 不在 main | 更新基线段；§1 单元验收改为「产出的 event 能通过 `groupchat.VerifyIncoming` 解出相同 envelope」，`Route()==AgentRouteReservedGroup` 断言延后到 #145 合入后由 P0-F 补 | S4 |
| D2 | §2 `RecipientDelivery.RelayAcks/RelayCount/Relays`、CLI 「1/1 relays」 | `publishToRelays` 返回 `bool` 且首个成功即停，无法得到「OK relay 数」；改它会动 DM 路径并与 #151 冲突 | M1 定义改为：`relay_acks ∈ {0,1}` 表示「至少一个 relay 接受」，`relay_count = len(targets)` 表示「配置的目标数」；删除 `Relays` 字段；CLI 文案改为 `relay accepted (≥1 of N)`。逐 relay 结果留到后续里程碑 | S3 |
| D3 | §2 E2E「Carol 离线 → 报告 1/2」 | 投递状态只取决于**发送方→relay**，与收件人是否在线无关；且所有收件人共用同一 relay 列表，Carol 离线不会产生 1/2 | E2E 改为：(a) 全部 relay 不可达发送 → 0/2 queued，换真实 relay 重试 → 2/2 且 event ID 不变；(b) 1/2 部分失败用 Go e2e 测试注入「对 Carol 的 event 首次发布失败」，Bob 走真实 relay | E2E-A |
| D4 | §3.5 对账表 | 缺 `failed` 行的情形：`group retry` 已完成 O1（新 `group_pending` 条目）但 T2' 未完成时，表里无对应动作；另外 `failed` + 遗留 `group_failed` 条目也未定义 | 增两行：`failed` + 恰 1 条 `group_pending` → T2' 采纳（`failed→queued`）；`failed` + 恰 1 条 `group_failed` → 无动作。对账扫描范围加上「仍有 outbox 条目的 `failed` 行」 | S7 |
| D5 | §3.2 只讨论旧二进制 | **新二进制**的 `CleanupOutbox` 也只保留 `Status=="pending"` 或近期条目：从未尝试过的 `group_pending`（`LastAttempt=0`）会在 daemon 第一次清理时被删；`recordAttemptFailure` 耗尽时写死 `"failed"`，会把群条目改成 DM 状态，破坏 Route⇔Status 不变量 | 契约 3.2 增加：`CleanupOutbox`、`recordAttemptFailure`、`inspectAttemptQueue`、`isFailedOrStuck` 必须按 route 使用对应状态值；列为 S5a 验收 | S5a |
| D6 | §2 手动重试、§3.5 「入队前先检查同 event ID」 | `group retry` 要用同一 event ID 换新 QueueID，而原 `group_failed` 条目仍在：先查重会直接返回旧条目；先追加会产生 2 条 → `queue_duplicate` | 新增原语 `RequeueGroupOutboxEntry`：在一次 `UpdateOutbox` 内删除同 ID 的 `group_failed` 条目并追加新 `group_pending` 条目；同 ID 有 `group_pending` 时返回既有条目 | S5a |
| D7 | §3.4「由 app 层传入 handler」 | `storage outbox retry` 的实现在 `internal/messaging` 包内，`messaging` 不能 import `groupchat`，app 层无处传参 | 增加注册点 `messaging.SetGroupOutboxProvider(p GroupOutboxProvider)`，由 `cmd/hyphae/main.go` 启动时注册一次；未注册时群条目一律 `route_handler_missing`（安全默认） | S8a |
| D8 | §2 `hyphae group send|retry` | `hyphae group` 已是旧 `internal/group` 命令（create/list/add-member/remove-member/leave/delete/chat，本地元数据语义，正是设计基线认定「不真实」的模型）；新协议也没有 create/accept 等 CLI | **需要拍板**。建议 M1 新协议使用独立命名空间 `hyphae groupchat {send,retry,status}`，旧 `group` 命令的去留由 CLI/TUI 线单独决定；在拍板前 S9 不开工 | S9 |
| D9 | §7 G4 | 只说控制 envelope「经 `PrepareFanout` 进入 T1」，没说必须与状态迁移**同一事务**。`AcceptInvite` 可重入，但 `CreateGroup` 每次生成新 group ID、`CancelGroup` 要求 `StatePending`，状态提交后崩溃就再也拿不回 envelope | G4 关闭条件改为「状态迁移与 fanout 行插入在同一事务」，实现经 S10a 的 Tx 变体 + S10b 的 `*WithFanout` | S10a |
| D10 | §1 构造函数「收拢为一处」 | 契约签名没有 encrypt 开关，`agent msg --encrypt=false` 无法用它；`daemon.buildAutoReplyEvent` 有可注入加密器 | 契约改为「加密路径收拢」：明文分支与 auto-reply 维持现状 | S1 |
| D11 | §3.4「返回 Attempted=false 和 issue route_handler_missing」 | `SendResult` 没有 issue 字段 | `SendResult` 增加 `Issue AgentMessageDeliveryIssue`（追加字段，DM 调用方不受影响），并提供哨兵错误 `ErrGroupRouteHandlerMissing` | S5b |
| D12 | §3.3 T1 | 未说明同 logical ID 重入 T1 时是否重新签名；未说明签名在事务内还是外 | 明确：签名在 T1 事务内完成（roster 与群状态在同一快照下读取，N 次 NIP-44 为毫秒级）；同 `(group, logical_id)` 已有 fanout 行时**直接返回既有行，不再签名** | S4 |
| D13 | §3.4 `AttemptSendRouted` 签名 | #151 正在给同一函数族加 `AttemptSendWithKeyStore(..., ks)`，两个签名会互相打架 | 建议把最后一个参数改为选项结构 `AttemptOptions{Handlers OutboxHandlers; KeyStore *types.KeyStore}`，等 #151 结论确定后在契约中定稿 | S5b |

D8 是唯一需要用户 / coordinator 拍板的产品决策；其余均为技术修正，建议合成一个「契约修订」文档 PR，在波次 1 开工前或与其并行合入。

## 7. 与里程碑测试要求的对应（AGENTS.md）

- 每片：新增公开函数都有单测，覆盖 nil/错误/空结果；全部使用临时 HOME，不触碰真实 `~/.hyphae/`。
- 真实 relay E2E（`wss://relay.aastar.io`、alice/bob/carol 真实身份）至少 3 条，落在 E2E-A：
  1. relay 抓包：两份 event 的 `Content` 不含正文、群名、roster 明文（契约 §1 E2E）；
  2. 全部 relay 不可达发送 → 0/2 queued，换真实 relay 后 daemon 重试 → 2/2，event ID 与首次一致（契约 §2/§3 E2E，按 D3 修正）；
  3. `send` 后在发布前中断（注入式），重启后对账 + 重试，Bob/Carol 各收到一次且 event ID 与中断前 SQLite 记录一致（契约 §3 E2E）。
- E2E-B 承接契约 §4/§6 的三人完整 E2E，依赖 P0-F 与 CLI 线，不在 P0-E 交付范围内。
- `test.sh` 在 S3 加入 `./internal/groupchat`，在 S9 加入 `./internal/groupchatcli`。
