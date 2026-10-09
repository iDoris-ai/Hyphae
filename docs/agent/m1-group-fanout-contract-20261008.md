# M1 群消息 fanout 与 inbound 路由契约（P0-E/F 前置）

状态：**rev7 设计评审 PASS（2026-10-09；仅文档/设计层）**。本文描述目标契约，不代表实现已存在或运行时验收已通过；S7 与受影响的 S8/S9 接线仍 **BLOCKED，直到 F1–F4 前置 follow-up 分别评审/合入并通过其验收门槛**。不得修改已批准的 #165/#166 来追补本设计。

设计历史：rev7 在源码审计与 rev6 复核基础上，选择有条件的受控部署边界并明确 legacy null 语义；本文与[实现计划](m1-group-fanout-impl-plan-20261008.md)自包含记录七项设计决策，不依赖临时档案。

### 实现状态与精确基线

本工作树/PR base 固定为 `699b36f74c6b3802b93768d670229d7b2d3b6cff`。下表来自本地 `git show <exact-object>:<path>`；未把远端 PR 的批准状态当成 base 已包含它们。旧 rev1–3 的 `6c5e751` / `254b5412` 只属历史。

| 精确对象 | 已检查的源/API | 事实与缺口 |
| --- | --- | --- |
| base `699b36f74c6b3802b93768d670229d7b2d3b6cff` | `internal/groupchat/store.go:beginImmediate`、[fanout.go](../../internal/groupchat/fanout.go)、`messages.go:storeMessageTx` | fanout 是 SQLite；S2/S3/S10a 已在 base。无 attempt_generation、row_revision、recovery witness；transitionFanoutTx 以 state CAS，部分字段 MAX 更新，不等于 rev7 fence |
| 同一 base | [outbox.go](../../internal/messaging/outbox.go) 的 readOutbox/UpdateOutbox/writeOutbox/currentOutboxAttempt；`outbox_lock_unix.go:withOutboxLock`；`pkg/types/types.go:OutboxEntry` | outbox 是独立 JSON 文件，排他 flock + temp fsync/rename/目录 fsync，**不与 fanout 共用 SQLite DB/事务**；typed unmarshal 后 normalize，缺 QueueID 可被自动分配 |
| 同一 base | [outbox_group.go](../../internal/messaging/outbox_group.go) 的 normalizeOutboxEntryRoute / RequeueGroupOutboxEntry / GetPendingGroupOutbox / GroupOutboxEntriesByEventID | S5a 已有；空 Route 可被修成 group；requeue 能替换 exhausted pending，GetPending 排除 exhausted；同事件 helper 只收 group-like，不能用作全 route 证据枚举；没有 D6 来源见证 |
| approved #165 `c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b` | `internal/groupchat/fanout_queue.go:queueTargets / BeforePublish / MarkRelayAccepted / RecordAttemptFailure` | 先 JSON 入队再 SQLite state CAS；BeforePublish 只看 accepted；失败回调增加 attempts。**没有 generation reservation/token，也没有恢复见证** |
| approved #166 `7861d029b5ccc2959b80a184a390ec132ddd025f` | `internal/messaging/outbox.go:GroupOutboxHandler / attemptSendGroup / recordGroupAttemptFailure / recordAttemptFailure` | 三个 handler 签名均无 generation；O3 先改 JSON RetryCount/status，再调用 T4；给 T4 加 fence 仍阻止不了 stale O3。精确 QueueID 删除已有基础，但 raw 全量/ACK 证据门槛仍需 follow-up |

两个 approved head 上的 tracked 契约仍为 rev3，不能把 rev5 的设计接口归到它们。#145 的 verified 路由、#151 的 `AttemptSendWithKeyStore` 已在 base；D1 的历史分工、D13 的 keystore 语义保留，无需再等这些已合入 PR。

参考：[M1 收尾计划](m1-release-plan-20261007.md)、[群聊最小设计](m1-group-chat-design-20261007.md)。不引入新的 relay 订阅/解密实现；现有 reserved guard 只提供不回落 DM 的保护，完整 inbound 仍见 §4。

### 七项评审发现的闭合索引（设计层）

| 发现 | 本次明确决策 | 契约位置 | 实施门槛 |
| --- | --- | --- | --- |
| 1 generation 不在 approved heads | 单独 F1–F4，不 retrofit #165/#166 | §3.1、§3.3、§3.6 | 全部合入后才 S7/接线 |
| 2 stale O3 | 持锁检查当前 token 后才写 O3；结果日志与 retry/status 原子提交，恢复屏障先 T4 后新 R | §3.3、V2/V3 | F2/F3 |
| 3 recovery provenance | D6 原子写 witness；missing 证明当前缺失，不要求不存在的旧记录 | §3.4、V4 | F4 |
| 4 generation/预算 | generation、publish starts、当前 QueueID failure budget 三个计数分开 | §3.1、V1 | F1/F3、S9 |
| 5 exhausted pending | `<` 与 `>=` 分开；D14 QueueID 冲突先于 exhaustion/recovery | §3.2、§3.4–3.5 | F2/F4、S7 |
| 6 诊断重叠 | first-match primary + details；orphan 仅无 row；诊断不覆写 Issue | §3.5、V5 | F4、S7 |
| 7 raw evidence | normalization/自动 QueueID 前验证；全 route 枚举；字段级 legacy 例外（DM 身份路由、DM/group relays:null） | §3.2、V6 | F2 |

**D1**（约束 S4；延后断言归 P0-F）：
- 决定（设计时）：基线以 `origin/main = 6c5e751` 为准（上列）。第 1 节单元验收中「通过 `VerifyAgentMessage`、`Route()==AgentRouteReservedGroup`」改为「产出的 event 能通过 main 上已有的 `groupchat.VerifyIncoming` 解出逐字节相同的 envelope」；`Route()==AgentRouteReservedGroup` 断言留给 P0-F 集成验收（当时 P0-F 开工前提是 #145 合入，见 G5）。
- 理由（设计时）：契约写作时 #138 未合、守卫未推送；D1 据当时基线将 `Route()` 断言留给 P0-F。当前基线已包含 #145；保留该历史切分的原因是 S4 验收不应依赖入站路由集成。
- 为什么不是另一个方案（设计时）：「S4 等 #145 合入」会把发送侧关键路径绑到收件侧 PR 上；当时 #145 还需按 #138 合并后的主干重做。「S4 自带一份 `VerifyAgentMessage`」会与 #145 产生两份解密边界，违反 I2。`groupchat.VerifyIncoming` 当时已在 main、覆盖 kind、event ID 与签名、`ValidateAgentMessageEvent`、恰一个且匹配的 `p`、NIP-44 解密并拒绝明文，再经 `groupchat.Decode` 即可比对 envelope，足以证明 event 可被收件侧接受；只缺「按前缀分类为 reserved」这一项，正是延后给 P0-F 的断言。

不变量（全文共用）：

- **I1** 不引入共享群密钥；每条逻辑消息逐收件人 NIP-44 加密。
- **I2** 不新增 relay 订阅、不新增解密实现；入站只经 `messaging.VerifyAgentMessage` 解密一次。
- **I3** 群 envelope（`hyphae.group/` 前缀，含未知/损坏版本）永不写入 DM 表 `messages`，永不进入 auto-reply、DM 通知或 DM 展示。
- **I4** 已签名 event 一旦持久化即冻结：重试只重发同一 event（同 event ID / 同 `d` / 同密文 / 同签名），绝不重新加密或重新签名。
- **I5** 状态单调：任何重放、重启、并发重试都不能让收件人投递状态或群状态倒退，也不能让同一逻辑消息显示两次。

### 决策索引（D1–D14；rev7 更新恢复与证据规则）

| ID | 位置 | 一句话决定 | 约束的切片 |
| --- | --- | --- | --- |
| D1 | 依据与基线、§1 验收 | 基线更新；§1 验收改用 `groupchat.VerifyIncoming`，`Route()` 断言延后到 P0-F | S4（P0-F 补断言） |
| D2 | §2 `RecipientDelivery`、CLI、§5 | `relay_acks ∈ {0,1}`、`relay_count = len(targets)`、删 `Relays`、文案 `relay accepted (≥1 of N)` | S3、S6、S9 |
| D3 | §2 验收 E2E | 投递状态只取决于发送方→relay；E2E 改为全 relay 不可达 0/2→2/2 + Go e2e 注入 1/2 | E2E-A |
| D4 | §3.4–3.5 | failed 恢复须 durable witness；全状态/全 route 扫描、顺序诊断 | F4、S7 |
| D5 | §3.2 | `CleanupOutbox`/`recordAttemptFailure`/`inspectAttemptQueue`/`isFailedOrStuck` 按 route 取状态值 | S5a |
| D6 | §2、§3.4–3.5 | 协调恢复 API：新 QueueID + 原子 witness；覆盖 missing 与两种 exhausted | F4、S7、S9 |
| D7 | §3.6 | `messaging.SetGroupOutboxProvider(p)`，`main.go` 注册一次；未注册→`route_handler_missing` | S8a（S8b 使用） |
| D8 | §2 CLI、全文命令名 | 新协议命令为 `hyphae groupchat {send,retry,status}`；不碰旧 `hyphae group` | S9 |
| D9 | §7 G4 | G4 关闭条件 = 状态迁移与 fanout 行插入同一事务（S10a Tx 变体 + S10b `*WithFanout`） | S10a、S10b |
| D10 | §1 构造函数 | 只收拢**加密路径**；明文分支与 daemon auto-reply 维持现状 | S1 |
| D11 | §3.6 | `SendResult` 追加 `Issue`；哨兵错误 `ErrGroupRouteHandlerMissing` | S5b |
| D12 | §3.3 | 签名在 T1 事务内；同 `(group, logical_id)` 已有 fanout 行时返回既有行、不再签名 | S4 |
| D13 | §3.6 | `AttemptOptions{Handlers; KeyStore}` 冻结；KeyStore 语义对齐已合入 #151 的 `AttemptSendWithKeyStore` | S5b |
| D14 | §2、§3.1、§3.4、§3.5、§5 | 状态迁移保留历史证据；queued QueueID 碰撞只 hold；独立 generation、O3 fence、证据恢复与精确清理 | F1–F4、S7–S9 |

---

## 1. fanout 语义

### 决定

一条群消息 = 一个逻辑消息 + N 个收件人 event（N = active roster 去掉本机身份）。

| 项 | 规则 |
| --- | --- |
| logical ID | 用户每触发一次「发送」，调用一次 `groupchat.NewOpaqueID()`（16 字节 `crypto/rand`，32 位小写 hex，满足 `validOpaqueID`）。同一逻辑消息的全部 N 个 envelope 共用它。 |
| envelope | `groupchat.Encode(Envelope{Type: EnvelopeMessage, Version: Version, GroupID, LogicalID, Body})`，N 个收件人的明文**逐字节相同**；不携带 roster。 |
| 加密 | 对每个收件人分别 `crypto.EncryptMessage(envelope, senderSK, recipientPK)`（NIP-44），再 `messaging.CompressText`。N 份密文互不相同（NIP-44 随机 nonce + 不同会话密钥）。 |
| 标签 | 每个 event 恰好一个 `p`（该收件人 hex 公钥），`c`=`AgentTag`、`z`=`CompressTag`、`v`=`AgentVersion`、`enc`=`nip44`，`d` 由 `messaging.NewAgentMessageDTag(compressed, createdAt)` 逐 event 随机生成。kind = `AgentKind`（30078）。 |
| created_at | N 个 event 使用**同一个** `createdAt`，且等于本机历史行的 `created_at`，使各端按同一时间排序。 |
| 签名 | 每个 event 由发送身份单独 `event.Sign(senderSK)`，得到 N 个独立 event ID。 |
| 构造函数 | P0-E 在 `internal/messaging` 新增 `BuildAgentMessageEvent(senderSK nostr.SecretKey, recipientPK nostr.PubKey, plaintext string, createdAt nostr.Timestamp) (*nostr.Event, error)`，把 `agent.go` `AgentMsgCmd` 加密分支与 `tui/offline_outbox.go` `sendQueuedMessage` 里重复的加密→压缩→打标签→`ValidateAgentMessageEvent`→签名序列，即**加密路径**，收拢为一处（**D10**）；群 fanout 只调用它。 |
| 本机副本 | 发送者不给自己发 event；本机历史只有一行逻辑消息（见第 3 节 T1），`event_id` 为 NULL。 |

**D10**（约束 S1）：
- 决定：「收拢为一处」的范围限定为**加密路径**。`agent msg --encrypt=false` 的明文分支继续内联构造 event；`daemon.buildAutoReplyEvent` 维持现状（保留其可注入的加密器）。S1 只改 `AgentMsgCmd` 加密分支与 `sendQueuedMessage`。
- 理由：契约签名 `BuildAgentMessageEvent(senderSK, recipientPK, plaintext, createdAt)` 没有 encrypt 开关，明文分支无法调用它；auto-reply 的加密器是测试注入点，强行收拢会丢掉该注入能力。群 fanout 永远加密（I1），只需要加密路径。
- 为什么不是另一个方案：给函数加 `encrypt bool` 参数会让群代码可以构造明文 event，与 I1 相悖，且扩大 S1 改动面；把 auto-reply 一起收拢需要给 `BuildAgentMessageEvent` 加加密器参数，对唯一真实调用方（群 fanout）没有价值，只增加 S1 在 DM 路径上的回归风险。

**logical ID 与 `StoreLocalMessage` 幂等键的对应**：#138 的 `groupchat_messages` 主键是 `(local_npub, group_id, logical_id)`。

- 发送端：`StoreLocalMessage(localNpub, groupID, logicalID, body, createdAt)` 以该主键写一行；同 logical ID + 同 sender + 同 body 重复写是 no-op，不同 body 返回 `ErrLogicalIDConflict`。
- 接收端：`ReceiveMessage` 以**同一个主键**（本地身份、group ID、logical ID）去重，并额外受 `UNIQUE(event_id)` 约束。所以「接收端恰一次」的键就是发送端生成的 logical ID，与收件人拿到哪个 event ID 无关。
- 进程内重试、恢复、`groupchat retry` **全部复用已持久化的 logical ID**；只有用户再次输入并发送才生成新 logical ID。CLI 不得在命令内部失败后自行重新生成。

### 为什么不是另一个方案

- **单 event 多 `p` 标签**：NIP-44 是两方会话密钥，一份密文无法同时给多人解；多 `p` 还会把 roster 公开给 relay；#136 `VerifyIncoming` / #145 `VerifyAgentMessage` 要求 `pCount == 1`，`StoreIncomingMessageOnce` 也要求恰好一个 `p`。
- **共享群密钥 / MLS**：既定决策排除（I1）；M1 不做密钥轮换、远端撤权。
- **NIP-17 gift wrap（kind 1059）**：需要新的 kind、过滤器与解包路径，违反 I2。
- **logical ID = hash(group, body, time)**：同一秒发两条相同内容会撞键被当作重复吞掉；且确定性派生让同内容可被关联。随机 ID 没有这两个问题。
- **logical ID = 第一个收件人的 event ID**：依赖收件人顺序，且 event ID 在 relay 上公开，会把各收件人 event 关联起来。
- **`d` 由 `hash(logical_id, recipient)` 派生**：event 在发布前已持久化（I4），不存在需要「同坐标覆盖」的重签场景；沿用现有 `NewAgentMessageDTag` 可保持与 DM 一致，不引入新的标识策略。

### 验收

- 单元：Alice→{Bob, Carol} fanout 后断言 2 个 event：`p` 各 1 个且分别为 Bob/Carol；event ID、`d`、`Content` 两两不同；`created_at` 相同；Bob、Carol 各自用私钥解出的明文逐字节相等且 `Decode` 后 `LogicalID` 相同；用 Bob 私钥解 Carol 的 event 失败。
- 单元（**D1**，S4）：`BuildAgentMessageEvent` / `PrepareMessageFanout` 产出的每个 event 用对应收件人私钥经 `groupchat.VerifyIncoming` 解出的 envelope 与发送前 `groupchat.Encode` 的结果逐字节相同。
- 单元（**D1**，P0-F，#145 已合入）：同一 event 通过 `messaging.VerifyAgentMessage`，`Route()` 为 `AgentRouteReservedGroup`。该断言不属于 S4 的合并条件。
- 单元（**D12**，S4）：同 `(group, logical_id)` 第二次进入 T1 时签名函数调用次数为 0，返回的 event ID 与第一次相同。
- 单元：同一 `(group, logical_id)` 两次进入 T1 只得一行本机历史；同 logical ID 不同 body 返回 `ErrLogicalIDConflict`，并且不新增任何 fanout 行。
- E2E（真实 relay 抓包）：relay 上两份 event 的 `Content` 都不含正文、群名或 roster 的明文。

---

## 2. 部分发送

### 决定

投递状态**逐收件人**记录、逐收件人报告；整体状态只是派生摘要，不单独落盘。

目标报告结构（在 base 的 `internal/groupchat/fanout.go` 基础上由 F1 扩展；诊断 overlay 见 §3.5）：

```go
type RecipientDeliveryState string

const (
    RecipientPrepared      RecipientDeliveryState = "prepared"       // event 已签名并写入 SQLite，尚无 outbox 证据
    RecipientQueued        RecipientDeliveryState = "queued"         // outbox 已提交且 QueueID 已回写 SQLite
    RecipientRelayAccepted RecipientDeliveryState = "relay_accepted" // 至少一个 relay 返回 OK，并已在 SQLite 落盘
    RecipientFailed        RecipientDeliveryState = "failed"         // 重试耗尽 / 队列条目丢失 / 不可发送
)

type RecipientDelivery struct {
    RecipientNpub string                               `json:"recipient_npub"`
    EventID       string                               `json:"event_id"`  // 冻结的签名 event ID
    QueueID       string                               `json:"queue_id"`  // 当前 outbox 条目；prepared 时为空
    State         RecipientDeliveryState               `json:"state"`
    Issue         messaging.AgentMessageDeliveryIssue  `json:"issue,omitempty"`
    RelayAcks     int                                  `json:"relay_acks"`  // D2：∈ {0,1}；1 = T3 durable ACK
    RelayCount    int                                  `json:"relay_count"` // D2：最近一次 publish attempt 的目标 relay 数 = len(targets)，不是 ACK 数
    Attempts      int                                  `json:"attempts"` // §3.1 durable publish starts（legacy 有标签）
    RetryCount    *int                                 `json:"retry_count"` // nil = unknown，不伪造 0
    RetryQueueID  string                               `json:"retry_queue_id"`
    AttemptPhase  string                               `json:"attempt_phase"`
    AccountingOrigin string                            `json:"accounting_origin"`
    LegacyAttemptsBase int                             `json:"legacy_attempts_base"`
    MaxRetries    int                                  `json:"max_retries"`
    LastAttemptAt int64                                `json:"last_attempt_at"`
    AcceptedAt    int64                                `json:"accepted_at"` // relay_accepted 时刻；否则 0
}

type FanoutReport struct {
    GroupID    string                              `json:"group_id"`
    LogicalID  string                              `json:"logical_id"`
    CreatedAt  int64                               `json:"created_at"` // D14：该逻辑消息全部收件人行的共同 created_at
    State      messaging.AgentMessageDeliveryState `json:"state"` // 派生，规则见下
    Accepted   int                                 `json:"accepted"`
    Queued     int                                 `json:"queued"`   // prepared 计入 queued
    Failed     int                                 `json:"failed"`
    Recipients []RecipientDelivery                 `json:"recipients"` // 按 RecipientNpub 升序，稳定输出
}
```

**D2**（约束 S3 的字段定义、S6 的 T3 写入、S9 的 CLI 文案）：
- 决定：M1 中 `relay_acks ∈ {0,1}`，含义是「最近一次尝试至少一个 relay 接受」，**不是** OK relay 的个数；`relay_count = len(targets)`，即该次尝试配置的目标 relay 数；**删除 `Relays` 字段**（上面结构已删）。CLI / TUI 文案统一为 `relay accepted (≥1 of N)`，不显示 `k/N relays` 形式的比例。逐 relay 结果留给后续里程碑（届时需要改 `publishToRelays` 的返回值，并作为新字段追加）。
- 理由（设计时）：main 上 `publishToRelays` 返回 `bool`，且**第一个 relay 成功即返回**，后续 relay 根本没有被尝试；按原契约填「OK relay 数」只能伪造数据。改 `publishToRelays` 会改动所有 DM 发送路径并与当时并行的 #151 正面冲突。
- 为什么不是另一个方案（设计时）：「在 P0-E 改 `publishToRelays` 为逐 relay 发布并返回结果」会改变 DM 发布的时延与行为（原本首个成功即停），超出 S5b「DM 分支零可观察变化」的边界；当时也会与 #151 的发送函数改动交叠。「保留 `Relays` 字段但留空」会让 `--json` 消费方误以为数据存在。`relay_accepted` 的判定（≥1 个 OK）不受影响，与第 5 节一致。

Issue 与 operation diagnostic 分离：持久 `queue_missing` / `retry_exhausted` / `send_failed` 只由 §3 的受保护迁移写入；`queue_duplicate` 等对账诊断不写 RecipientDelivery.Issue，缺 handler 仅返回 route_handler_missing。枚举已存在不代表允许在任意 row 上持久化。

派生整体状态，复用现有三值 `AgentMessageDeliveryState`：任一收件人 `failed` → `failed`；否则任一 `prepared`/`queued` → `queued`；否则 → `relay_accepted`。「部分」由计数表达（例如 `queued`，`accepted=1/2`），不新造第四个整体状态。

CLI `hyphae groupchat send`（命令命名空间见 **D8**）：

- 文本输出逐收件人一行，收件人用联系人昵称（无则 npub 前 16 位），且经终端控制字符转义（relay 文案按 **D2**）：

  ```text
  📤 group <name> · logical 3f2a…  accepted 1/2
     bob    ✓ relay accepted (≥1 of 2)  event 9ab1…
     carol  ⏳ queued                    publish starts 1; failures 1/10  event 77cd…
  ```

- `--json` 输出完整 `FanoutReport`。
- 退出码：全部 `relay_accepted` → 0；否则返回 `common.NewExitErrorWithData(common.ErrCodeOther, err, report)`。这与现有 `agent msg` 「已入队待重试仍返回非零」一致，脚本才能区分「全员 relay 已收」和「部分在路上」。
- 文案只说 “relay accepted / 已提交 relay”，不得出现 “delivered / 已送达 / 已读”。

TUI（P0-F 的群模型）：本机发出的每条消息尾部显示派生摘要，例如 `✓ 2/2`、`⏳ 1/2`、`✗ 1/2`；选中该消息展开逐收件人行（同 CLI 字段）。状态更新来自 SQLite 重读及 §3.5 只读 evidence overlay，而不是 worker 内存，因此重开 TUI 后显示一致。

重试：

- 自动重试：同一收件人沿用原 outbox 条目（同 QueueID、同 event），由现有重试循环（daemon、TUI outbox worker、`storage outbox retry`）经升级后的 routed 协调 API 发送，见第 3 节分派。
- 手动重试：`hyphae groupchat retry <group-id> <logical-id> [--recipient <npub>]` 只作用于 `failed` 行；它经 **D6** 的协调恢复 API 与耐久 witness 把 SQLite 中**原 `event_json`** 重新入 outbox（新 QueueID、**同 event ID**），行转回 `queued`；queued 缺队列/同 QueueID exhausted 可先对账成 failed，不同 QueueID 一律 hold（§3.4）。`relay_accepted` 的收件人永远不重发。
- 只读查询：`hyphae groupchat status <group-id> <logical-id> [--json]` 输出 `LoadFanoutReport` 加 §3.5 的只读 evidence diagnostics overlay（同 `send` 格式），不发布、不入队、不重放持久写入。

**D8**（约束 S9；协调者已拍板）：
- 决定：新群协议的 CLI 使用独立命名空间 **`hyphae groupchat {send,retry,status}`**。**不改动现有 `hyphae group`**：旧 `internal/group` 的 create/list/add-member/remove-member/leave/delete/chat 保持原样，其去留由 CLI/TUI 线单独决定。本文其余位置凡指新协议命令，均为 `hyphae groupchat …`。
- 理由：main 上 `hyphae group` 已被旧 `internal/group` 占用，它是本地元数据语义，正是群聊设计基线认定「不真实」的模型；新协议没有 create/accept 等 CLI，挂进旧命名空间会让同一前缀下两套语义并存。
- 为什么不是另一个方案：「在 `hyphae group` 下追加 `send/retry` 子命令」会让 `group create` 建的旧群与 `group send` 发的新协议群互不相通，用户无法从命令名判断；「直接替换旧 `group` 命令」会删除现有用户可见功能，属于 CLI/TUI 线的产品决定，不应夹带在 P0-E 的 S9 中。

### 为什么不是另一个方案

- **只报告整体成功/失败**：3 人群里 Carol 失败、Bob 成功时，用户无法知道该对谁重试，也无法证明 Bob 的副本已提交 relay。
- **失败后对该收件人重新加密/重新签名**：产生第二个 event ID，接收端只能靠 logical ID 兜底去重，且 relay 上留下两份可关联密文；违反 I4 和发布计划「重试复用同一个收件人的原 event」。
- **失败后整条消息重发给所有人**：已 ACK 的收件人会收到重复 event，并且重新生成 logical ID 会导致重复显示。
- **新增第四个整体状态 `partial`**：现有 CLI/TUI/JSON 消费方已按三值 `AgentMessageDeliveryState` 处理；计数足以表达部分，不扩大状态枚举。

### 验收

- 单元：注入 publisher 让 Bob 成功、Carol 失败 → 报告 `State=queued, Accepted=1, Queued=1`，Bob 行 `relay_accepted`、Carol 行 `queued` 且 `Attempts=1`；CLI 退出码非零，`--json` 字段齐全。
- 单元：Carol 重试耗尽 → Carol 行 `failed`、issue `retry_exhausted`、整体 `failed`；`groupchat retry` 后 Carol 行 `queued`，outbox 新条目的 `ID` 等于原 event ID，`EventJSON` 与 SQLite 中 `event_json` 逐字节相同。
- 单元：对 `relay_accepted` 收件人调用 `groupchat retry --recipient` 返回错误且不入队。
- 单元（**D2**）：`relay_acks` 只取 0 或 1；`relay_count` 等于该次尝试的 `len(targets)`；`--json` 输出中不存在 `relays` 键；文本输出含 `relay accepted (≥1 of N)` 且不含 `k/N relays` 形式。
- E2E（**D3**，E2E-A）：
  - (a) Alice 以「全部 relay 不可达」的配置执行 `groupchat send` → 报告 `accepted 0/2`、两行均为 `queued`；把配置换回真实 relay `wss://relay.aastar.io` 后由重试循环（daemon 或 `storage outbox retry`）推进 → `accepted 2/2`，Bob、Carol 实际收到的 event ID 与第一次尝试前 SQLite 中记录的 event ID **相同**，各自只显示一次。
  - (b) 1/2 部分失败用 Go e2e 测试（`//go:build e2e`）注入「对 Carol 的 event 首次发布失败」，Bob 走真实 relay → 报告 `accepted 1/2`，退出码非零；下一轮重试后 2/2，Carol 的 event ID 不变。

**D3**（约束 E2E-A）：
- 决定：E2E 以「发送方→relay」的可达性制造部分发送，不以「收件人离线」制造；具体用例为上面 (a)(b)。
- 理由：投递状态（`relay_accepted`）只取决于发送方到 relay 的发布结果，与收件人是否在线无关；且所有收件人共用同一 relay 列表，「Carol 离线」不会让 Alice 侧出现 1/2，原用例无法通过也无法证伪任何东西。
- 为什么不是另一个方案：「给 Carol 配不同的 relay 列表」需要按收件人选 relay，M1 没有该功能，引入它只为造测试场景；「在 shell E2E 里断网」不可在 CI 中稳定复现，且会同时影响 Bob。(b) 用 Go e2e 注入只影响 Carol 的首次发布，Bob 仍走真实 relay，满足 AGENTS.md 的真实 relay 要求。

---

## 3. outbox 与 SQLite 不共事务：durable send-intent

以下均为 **rev7 提案**；F1–F4 独立 follow-up 尚未实现/评审/合入。SQLite 保存权威 intent/投递状态，JSON 保存工作队列及跨存储提交凭据。不声称两者具有单事务原子性；选择「统一跨进程锁 + SQLite CAS + JSON 原子结果日志 + 强制恢复屏障」协议。仅把 generation 加进 T3/T4 回调不能满足本契约。

#### 3.1 数据模型与计数（D14；发现 1、4）

保留基线 `groupchat_fanout` 的主键 `(local_npub, group_id, envelope_type, send_key, recipient_npub)`、`UNIQUE(local_npub,event_id)`、冻结的 `event_json`/`event_id`、`max_retries` 与同 intent 共同 `created_at`。控制 envelope 的 `send_key=invite_id`，message 的 `send_key=logical_id`。F1 增加以下**目标 schema**（不是现有列）：

| 存储 | 字段/约束 | 语义 |
| --- | --- | --- |
| fanout SQLite | `row_revision` 非负 64 位整数 | 每次实际 row 更新递增；所有跨存储操作 CAS 的版本，阻止同态/ABA 覆盖 |
| fanout SQLite | `attempt_generation` 非负 64 位整数 | 每次 R 预留严格 +1；从不因重排、进程重启或失败重置，溢出 fail closed |
| fanout SQLite | `attempt_phase` = idle/reserved/started/failed/accepted | 当前 generation 的阶段；started 后无结果表示结果未知 |
| fanout SQLite | `last_result_generation`、`last_failure_receipt`（token+凭据摘要）、`last_recovery_id`/`last_recovery_digest` | 幂等投影水位与已消费精确凭据；只能消费匹配的 O3 结果或 D6 witness |
| fanout SQLite | nullable `retry_count`、`retry_queue_id` | 当前 queue epoch 的预算快照；由 O3→T4 或 D6→T2' 投影，读报告可识别尚待投影，不能用 attempts 代替 |
| fanout SQLite | `accounting_origin` = native/legacy、`legacy_attempts_base` | 升级时旧 attempts 只能保留为 legacy 基数，不能伪称是历史发布次数 |
| outbox JSON group entry | `fanout_protocol=1`、可选 `failure_result`、可选 `recovery_witness` | F2 严格版本化 schema；O3 与 failure_result 同次 JSON 提交；D6 替换与 witness 同次 JSON 提交 |

`AttemptToken = (完整 fanout 主键, event_id, queue_id, attempt_generation)`；QueueID 非空且全队列唯一，generation 独立于 attempts。主键与 sender 身份必须同时匹配，不能仅凭 event ID 跨身份回调。`row_revision` 在每个短事务内现读并 CAS；token 在 R/S/结果之间不因正常 revision 变化失效。

- **R reservation**：只增加 `attempt_generation`，phase=reserved；`attempts`、`RetryCount`、`last_attempt_at`、`relay_count`、`Issue` 均不动。崩溃只消耗 generation，未消耗发布计数或 retry budget。
- **S dispatch start**：R 后独立的 SQLite 提交，精确 token、phase=reserved CAS；`attempts += 1`，phase=started，记录 `last_attempt_at=max(旧值,now)` 与 `relay_count=len(targets)`，清除旧发送失败 Issue。S 成功返回后才调用 P，重试 S 不可再次加一；已有 started 的重复 S 不授予第二次 P，进程恢复以新 R 开始。`attempts` 的严格定义为「已持久化授权的发布启动次数」，**不声称能证明网络调用发生**：S 后 P 前崩溃也计 1，UI 标记结果未知。不能在无事务的网络 P 与计数之间声称恰一次。
- **O3 failure**：只有当前 token、phase=started、尚无该 generation 结果的有效 P 失败才使 `RetryCount += 1`；达到 `MaxRetries` 写 `group_failed`，否则 `group_pending`。成功、过期结果、校验失败、R/S 崩溃不增加 RetryCount。RetryCount 是本 QueueID 已提交的发布失败数，不是所有 P 调用数；自动调度只允许 `< MaxRetries`。
- **T3/T4** 不再增加 attempts。T4 从 O3 凭据投影 retry_count/status/issue、phase=failed、last_result_generation、last_failure_receipt；T3 固化 ACK，phase=accepted 并推进 last_result_generation。`Outbox.LastAttempt` 是最近已提交失败的 S 时间，不是成功时间；SQLite `last_attempt_at` 是最近 S 时间。两个时间不必相等。
- **D6** 创建新 QueueID，并重置该 epoch 的 RetryCount=0 / LastAttempt=0；`MaxRetries` 必须等于冻结的 row.max_retries。SQLite attempts、attempt_generation、last_attempt_at、relay_count 与历史水位保留；T2' 清 Issue、phase=idle、投影新 retry_queue_id/count=0。下一 R 才再增加 generation。旧 epoch 结果永远不能回写。
- `relay_count` 是最近 S 的目标数，可以从 3 减到 1，**不能用 MAX 聚合**；`relay_acks ∈ {0,1}`，1 仅在当前 token 的 T3 写入。`accepted_at=max(now,last_attempt_at)` 且 >0，终态冻结；updated_at 单调，只在实际写入时更新。只读 hold 不刷新时间。
- 报告扩展 nullable `RetryCount`、`RetryQueueID`、`AttemptPhase`、`AccountingOrigin`、`LegacyAttemptsBase`；generation 可在诊断中显示，不能充当 `attempt x/MaxRetries`。CLI/TUI 顶层格式为 `publish starts N; failures r/M (current queue)`；native 的 N=attempts，legacy 的 N=attempts-legacy_attempts_base，并另显示历史基数，started 无结果显示 `outcome unknown`。预算投影待恢复时显示 `pending bookkeeping`，不拼接旧 QueueID 的预算。只读 status 不做恢复写入。

F1 迁移保留旧 attempts 原值，另存相同的 immutable legacy_attempts_base，标记 legacy；以该非负值初始化 generation 水位但**不构造历史 token**，phase=idle（accepted 行保留终态并置 phase=accepted）。迁移只在 §3.6.1 受控安装边界成立、旧 writer 已退出且所有入口关闭后进行；旧在途回调没有新 token，一律不能提交。不能从现有 attempts 推导历史 RetryCount，须用经严格验证的当前队列初始化预算快照，否则报告未知。新行两计数与 legacy_attempts_base 均为 0。F1 需同时更新迁移原语/allowlist/报告，不能沿用基线 state-only CAS 或 relay_count 的 MAX 行为。

#### 3.2 原始证据边界（D5；发现 5、7）

**先读原始字节，再校验，再分类，最后才能正规化 DM 或调用任何可写 API。** 现有 `LoadOutbox/readOutbox` 和 `currentOutboxAttempt` 不能当作群证据入口（前者会归一 Route，后者会补 QueueID）。F2 提供 `ReadRawOutboxEvidenceLocked` 与保持未改条目原始字节的 writer；普通 struct unmarshal 后再 marshal 不够。

1. 在稳定 outbox sibling 锁内读取一个完整 JSON snapshot。严格 tokenizer 在 outer document、entry、嵌套 witness/result、`event_json` 内的 signed event 每一层检测重复 key（包括转义后同名）、未知字段、字段级 null/缺失/类型错误（见下表）、尾随 token、整数溢出/负值。schema 字段名大小写精确；signed event 使用受支持的 Nostr 字段 allowlist，验证 ID/签名、kind、单一 p、加密标签、sender/recipient 与条目自身声明一致；与冻结 SQLite event 的逐字节比较在 §3.5 优先级 5 完成。`event_json` 字符串不得经重编码替换。未知 metadata 版本 hold，不能丢弃后继续。
2. 群条目的原始 `route` 必须显式等于 `group`，status 仅 `group_pending/group_failed`；空/缺失/null route + group status 均为 `queue_corrupt`，**无 legacy group Route 修复例外**。反向不匹配、未知 route、`sent` 群条目也不可当 ACK。group queue_id 空/缺失/null 先报损坏，绝不让 `currentOutboxAttempt` 自动分配。`RetryCount >= 0`、`MaxRetries > 0`、时间非负、身份/固定字段匹配；RetryCount 超过上限是 exhausted，不单因 `>` 判损坏；group_failed 却 RetryCount<MaxRetries 是矛盾证据，归 queue_corrupt。
3. 先枚举 **所有 route/status** 共享外层 ID 或可严格读出 signed EventID 的条目，包括 DM、未知 route、坏 group、group_failed；随后才选择/adopt。两个 ID 不符是损坏，不能把碰撞藏在另一 ID 下。条目不能可靠提取身份/EventID、outer JSON 损坏、或未识别字段可能影响枚举时，整份 outbox `queue_corrupt(scope=file)`，冻结该文件群操作。多个条目同 EventID 一律 duplicate，不能选最新。重复 QueueID 跨 EventID 也不可用。
4. Route/QueueID 的唯一兼容例外：无 group status、也不与任何 fanout EventID 碰撞的普通 DM，可沿用旧的空/缺失 route 与空/缺失 QueueID；只有隔离的 DM 路径可补 QueueID。DM 的 route/queue_id 显式 null 仍拒绝；DM 不因此成为群候选。另有下表规定的 DM/group `relays:null` 兼容，不授权身份修复。缺少新增 protocol metadata 的**严格合法旧群条目**可在升级审计中补 `fanout_protocol=1`，不能补 Route/QueueID、生成 recovery witness 或假造结果。升级审计仅对匹配 queued 行的旧条目自动初始化预算；prepared+active pending 在 §3.5 的 T2 正式采纳后才投影该条目的预算，不能由 migration 提前假定 queued；failed+pending 仍无恢复授权。
5. `GetPendingGroupOutbox` 必须只返回严格有效的 `group_pending && RetryCount < MaxRetries`；`GetPendingOutbox` 只返回合格 DM。`group_pending && RetryCount >= MaxRetries` 与 `group_failed` 均不可发布，交 §3.5/显式 D6；全量证据扫描不能使用 GetPending 作为输入。

**字段级 raw JSON schema（B2）**：缺失、null、空值分别判定，不能用 Go 零值或指针反推原文是否存在。表中例外只适用于指定字段；每层 duplicate-key（包括转义同名）、unknown-key、类型与范围检查始终有效。

| 字段/位置 | 缺失、null 与空值规则 |
| --- | --- |
| outer document / entry / signed event | 必须为对象；`entries` 必须为数组，每项为对象，均不得缺失/null。signed event 必需 id/pubkey/sig/content/tags/kind/created_at 按其 schema 校验，`tags` 及元素不得 null；不因 relays 例外放宽 event |
| entry `id`、`event_json`、`recipient_npub`、`status`；群 `route`、`queue_id` | required，拒绝缺失/null/错误类型；身份与群 Route/QueueID 必须有效非空。`event_json` 为原始签名 event 的 JSON 字符串，先验证后比较，绝不先重编码或发明标识 |
| `retry_count`、`max_retries`、`last_attempt`、`created_at` | required 非 null 整数，范围按本节；缺失不能默认为 0。counter/时间为 0 是否合法依字段语义判断 |
| DM/group entry `relays` | required；允许字符串数组（空数组表示 default-relay selection）或 **legacy null**（同一默认 relay 哨兵）；缺失、标量、对象、数组中的 null/非字符串拒绝。新建条目必须写 `relays:[]` 表示默认选择；对已有 legacy 条目的采纳、迁移、O3、恢复投影及其他非替换更新须逐字节保留其 `relays` 原始片段，包括 null 与周围原有空白，不将 null 改写为 [] |
| `fanout_protocol`、`failure_result`、`recovery_witness` | protocol 仅旧群条目可缺；出现时须为受支持正整数，null 拒绝。result/witness 是可省略对象，省略表示不存在；显式 null 拒绝。出现则内部必需 version/token/key/ID/revision/generation/计数/时间等字段完整且非 null，不能用空对象或缺失成员代表未发生 |
| witness 的条件字段 | `old_queue_id` 必须存在且为字符串，仅 §3.4 的 legacy failed 无旧 QueueID 可为空，不可 null。reason=missing 必须 `observed_absent=true`，旧条目摘要/status/RetryCount 省略；exhausted 两种 reason 必须旧摘要/status/RetryCount 且 `observed_absent` 省略；互斥字段或 null 均拒绝 |
| optional / pointer 输出 | 无其他通用 null 豁免。可选持久字段缺失与显式 null 不等价；新字段须列入已评审 schema。§2 报告 `RetryCount *int` 与 SQLite nullable retry_count 可用 null 表示 unknown，**不代表** outbox retry_count、token 或 witness 允许 null；可选 Issue 省略表示无 issue，出现须为合法字符串 |

上述片段保留是未来 F2 writer 的要求，不是 approved typed writer 的现有能力；授权更新计数/metadata 时不承诺整份 outer JSON envelope 字节不变。legacy `relays:null` 在补 `fanout_protocol=1` 后仍合法，不能因升级 metadata 就失去兼容性。仅解释为运行时选用 `defaultRelays`，不把默认 relay 地址写回条目；defaultRelays 为空时沿用既有发送结果，不额外发明成功语义。D6 创建新 QueueID 属于新条目，默认选择写 []，从旧 null 得到同一选择语义；此处是显式授权替换，不是旧条目的归一化。D6 witness 必须对替换前**包含原 null 的旧原始条目**取摘要；所有 event 字节始终保留。已消费的 result/witness 即使在同条目后续 O3/R/S/T3 等更新时也须保留原始嵌套片段，直到 §3.3/§3.4 授权替换/删除，不能重排或重新格式化破坏 receipt digest。

**源路径复制 fixture 规格（B2，F2/V6 必交）**：以下是应复制落盘 bytes 的两个具名输入，不是手写有效签名或本次已运行的测试。F2 要在隔离 HOME 从指定精确对象运行源路径后直接读取 `outbox.json`，连同对应 SQLite row、event bytes 和 SHA-256 固定为原始 fixture；不得以新版 struct round-trip 生成或“修复”。完整签名 event 由该路径生成后冻结，文档不以占位 event 冒充可执行 fixture。

| fixture | 必须复制的精确来源/原始特征 | 预期结果与派生用例 |
| --- | --- | --- |
| `legacy-165-group-nil-relays` | #165 `c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b` 的 `fanout_queue.go:queueTargets` 调用 `enqueue(target.eventJSON, target.recipient, nil, target.maxRetries)`；其 `outbox_group.go` 用 `Relays: append([]string(nil), relays...)`，`OutboxEntry` relays 无 omitempty，writeOutbox MarshalIndent 产生 `"relays": null`、`"retry_count": 0`、`"last_attempt": 0`、`"status": "group_pending"`，并保留源分配的非空 queue_id、route=group、真实 event_json | matching queued row 可补 protocol 并初始化当前 QueueID 预算 0/M，不能改 relays/event bytes；捕获 O1 后 T2 前另一个 snapshot，prepared 经 T2 采纳原 QueueID、预算 0/M；有效 R/S/P 失败后 O3 计 1、T4 前 kill 重放只投影一次，null/event 不变；同原 QueueID 耗尽派生 fixture 经显式 D6 新条目 [] + witness，再 T2' kill/replay 幂等；failed+active pending 无 witness 仍 hold |
| `legacy-dm-nil-relays` | 同一 #165 精确树的 `outbox.go:enqueueOutboxEntry`（与 base 路径相同）调用 nil relays，`Relays: relays`；落盘 `"relays": null`、`"retry_count": 0`、`"max_retries": 10`、`"last_attempt": 0`、`"status": "pending"`；route 由 omitempty 省略，queue_id 保留源分配值 | 严格 raw 校验后进入独立 DM 兼容路径，不增加 group protocol/witness、不写 fanout、不初始化虚假群预算；DM 发送仍选 defaultRelays，失败重试预算沿用 DM 规则且保留 null/event bytes。另以源 raw bytes 定向删除 queue_id 测 legacy DM 例外，不能把该例外移植到 group |

#166 `7861d029b5ccc2959b80a184a390ec132ddd025f` 的 `attemptSendGroup` 明确以 `len(targets)==0` 选 defaultRelays；上述 null 不是损坏或新的投递语义。两个 exact approved head 均无本节未来 strict raw 验证能力。V6 还须在这两个真实 fixture 上定向改出缺失 relays、`relays:[null]`、计数 null、group Route/QueueID null、result/witness null、嵌套缺字段及重复/未知 key，并验证拒绝前后原 bytes 不变。

异常快照禁止整文件类型化重写（否则会抹掉别的坏条目）。F2 将所有 writer 路径纳入原始证据保护，确保 hold 条目的原始 bytes 保留；无法无损保存时整个写请求失败。`CleanupOutbox`/clear 不得删未投影的结果/witness，不得自动删除群 failed 证据；精确终态清理由 §3.5 授权。D5 的 route 状态分离继续有效。

#### 3.3 并发、顺序和实际 O3 fence（D12、D14；发现 1、2）

**F3 新 API 边界提案**：`WithGroupOutboxEvidence` 管理 outbox 锁、严格 snapshot、SQLite `BEGIN IMMEDIATE` 与恢复屏障；`ReserveGroupAttempt` 返回 token；`StartGroupPublish(token,targets)` 提交 S；`CommitGroupFailure(token,result)` 包住 **O3 本身和 T4**；`CommitGroupAccepted(token,ack)` 包住 T3；`CleanupAcceptedGroup` 仅做授权 O2。方法名为设计名，接口需在 F3 独立评审冻结。不能继续使用 #166 的 `recordAttemptFailure` 先写、handler 后验流程；也不能只改三个 handler 的参数。

全局锁顺序：**outbox 稳定 sibling 排他锁 → SQLite BEGIN IMMEDIATE → 完成短操作 → 结束 SQLite → 释放 outbox 锁**。T1 是纯 SQLite，不获取 outbox 锁；不得持 SQLite 再等 outbox。所有群 row 状态/queue/generation/revision writer（含 requeue、reconcile、clear、迁移、generic transition 的调用方）必须遵循该边界；DM outbox writer 使用同一文件锁并保留群凭据。不支持跨进程锁的平台禁止启用群发送。P 在两把锁均释放后执行。此锁只约束合作的新 writer；旧二进制即使取得同一 flock 仍可删字段/清队列。§3.6.1 的部署前提独立成立，不能由 outbox.lock、daemon.lock 或协议 marker 代替。

```text
T1: SQLite 原子写本机历史 + 冻结 fanout prepared（D12）
O1: 锁内只从冻结 event 入 JSON；持久成功后 T2: prepared→queued
R:  锁 + 恢复屏障 + 当前唯一队列/row/token 校验；SQLite 预留 generation
S:  再取锁 + 恢复屏障；CAS 当前 reserved token，记录 publish start
P:  解锁后调用 publisher（只用原 event；可与另一 generation 的 P 重叠）
成功: 锁内校验当前 started token；T3 SQLite ACK 提交；随后精确 O2
失败: 锁内校验当前 started token；O3 JSON failure_result + retry/status 同次提交；T4 SQLite 投影
```

R/S 每次都重新验证 §3.2/§3.5，包括 D14 queued QueueID；R 可以 supersede 尚未完成的旧 generation，旧 P 的网络调用无法撤回，但其结果不能改变持久化状态。S 后 P 前进程挂起也可能产生迟到网络调用；本方案承诺持久化 fence 与 event 去重，不声称阻止所有过期网络发送。发现 hold 后不授权新的 P。

回调先在锁内只读核对 SQLite token/phase；过期或已完成则直接 stale_attempt，**不触发恢复屏障写入**，即使另一个 generation 有待投影结果也由下一次正常操作恢复。这保证 A 晚失败的调用本身不能改任一 durable record。当前 token 的回调、新 R/S、D6/对账才进入屏障。

**O3 内的不可分割校验与写入**：同一 outbox 锁内开始 SQLite 写事务，检查完整 token、state=queued、phase=started、精确 event/QueueID、row_revision、RetryCount 旧值、无结果水位；然后单次 JSON fsync/rename/dir-fsync 写 RetryCount/status/LastAttempt **及** `failure_result`，最后 T4 CAS/commit。SQLite 写锁和 outbox 锁直到 T4 成功/失败处理结束一直持有；B 不可在 A 校验后、O3 前预留。`failure_result` 至少包含 version、token、S 的 attempts/时间/targets 数、expected row_revision、prior/result RetryCount、result status、issue、result_id（token 唯一）。它不是可丢弃的日志；是重放 T4 的唯一授权。重入同 result_id 不再递增 RetryCount。

**崩溃 O3 后 T4 前**：SQLite 事务回滚，JSON 已含新 RetryCount 和当前 token 的结果。任何后续 R/S/D6/O2/row 修改前，恢复屏障验证凭据与 row 的 token/revision/started 记录及 JSON 结果完全匹配，再执行一次 T4；若 `last_failure_receipt` 的 token/摘要精确对应该 JSON 凭据则已经消费，是幂等 no-op；凭据摘要取严格校验后存储的原始结果 bytes，不拿后续 S 已变化的 attempts/targets/revision 再作旧结果比较。结果水位未知、冲突或损坏时 hold，不能再预留 generation，也不能重加 RetryCount。T4 已提交、JSON 凭据尚未压缩时重复屏障同样 no-op。已消费凭据按 last_failure_receipt 或 last_recovery_id+last_recovery_digest 判定，不再要求旧 expected revision 等于当前 revision；后续 R/S 更新 revision/generation 或 T3 接受不能把已消费凭据误判为冲突；R/S/T3 不改 last_failure_receipt，新 T4 才替换它。T3 推进结果水位也不能使旧 O3 凭据变成“未消费”。只在水位已证实消费后，下次 O3/D6 可替换凭据；历史计数留在 SQLite。未恢复前只读 status 可以报告 bookkeeping pending。

O3 写返回 commit-uncertain 时，在仍持锁时严格重读并确认目录持久性；不能确认就返回 pending-bookkeeping，不执行第二次 O3、不调用 P、不放行新 generation。重启先取得锁、确认完整 snapshot 与目录持久性并恢复；若实际只留下旧 snapshot 而无结果，则此次 P 结果没有 durable 证据，按 outcome unknown 处理，不推测 RetryCount，应在确认存储正常后才允许新 R。跨文件原子性仍不存在，这个可恢复提交边界是 **F2/F3 前置工作**；现有 UpdateOutbox 回调与 state-only transition 不能直接提供该保证。

**必测 A/B 交错**：同 EventID、同 QueueID，A 预留 g=7 并开始 P；B 预留 g=8/开始 P；A 晚失败。A 在任何 JSON 写前发现 g 不匹配，返回 `stale_attempt`：两 durable records 均逐字段不变，RetryCount/status/Issue 不动。B 已 T3/O2、B 未返回、B 失败已 O3/T4 三种结局均须通过。反向顺序：A 已 O3 尚未 T4 时崩溃，B 的 R 必须先重放 A 的 T4；若已耗尽则 B 不可预留。既不丢 A 的有效失败，也不让 stale A 破坏 B。

T3 仅接受当前 started token 的 relay OK；T3 成功后终态冻结，O2 失败只诊断待清理。P 成功但 T3 前崩溃没有 durable ACK，允许同 event 后续重发，不能猜成功。迟到失败或 ACK 在新 generation、D6 新 QueueID、failed 或 accepted 状态后一律不写。失败结果已完成也不能被同 token 的另一回调改成 ACK。

T1 的 D12 保持：事务内读 roster/群状态、签名、写本机行与 fanout；全部回滚或全部提交。同 intent 重入返回既有 event，不签名；同 logical ID 不同正文冲突。T1 提交前不得 P。

#### 3.4 显式恢复授权（D6、D4；发现 3、5）

**F4 新协调 API `RecoverFailedGroup`** 取代生产调用裸 `RequeueGroupOutboxEntry`。旧 API 存在不等于有恢复授权。先严格全量扫描并应用 §3.5 前置优先级，执行恢复屏障，再判断资格。用户显式 retry 只授权目标 failed 收件人；若起始 queued+缺队列/同 QueueID exhausted，先按 §3.5 提交 failed，再继续。queued+不同 QueueID **第一时间 hold**，即使该条目 exhausted 也不准转 failed 以绕过 D14。

| 起始证据（通过全部优先级） | 显式 D6 动作 |
| --- | --- |
| failed + 0 条；包括 queue_missing，或 legacy failed 行从未有 QueueID | 允许从冻结 event 建新 queue；reason=missing，见证持锁扫描为 0，不要求不存在的旧 failed 条目历史 |
| failed + 唯一同旧 QueueID `group_pending` 且 RetryCount>=MaxRetries | 允许替换，新 QueueID；reason=exhausted_pending，记录真实 status/counters |
| failed + 唯一同旧 QueueID `group_failed`（RetryCount>=MaxRetries） | 允许替换，新 QueueID；reason=exhausted_failed |
| failed + 同/不同 QueueID active pending，且无有效 witness | hold `recovery_unauthorized`；pending 本身不能证明 D6，不能直接 T2' |
| failed + 不同 QueueID exhausted pending/group_failed，且无有效 witness | hold `queue_conflict`；不能替换并掩盖冲突 |
| failed + 有效 witness 的唯一新 QueueID active pending | 仅重放原 D6 的 T2'，不再次清预算/创建 queue |
| queued + 同新 QueueID 且 row.last_recovery_id 已等于 witness ID | 已完成的 D6，幂等 no-op；不再次 reset RetryCount |

`group_failed` 且 below-limit 是矛盾证据，hold；未耗尽的失败原因不能冒充 exhaustion。每次真正 D6 必须新 QueueID，**不存在 failed+同旧 QueueID pending 的授权恢复例外**。若目标 QueueID 与旧 ID 相同或与全队列任一 ID 碰撞，拒绝，不执行替换。已有新 QueueID 只可凭 witness 完成，不能用“看起来刚重排”推断。

在锁内、SQLite 已提交的 failed row 上创建 `recovery_witness`：version、随机 recovery_id、完整 fanout key、event_id、冻结 event_json 的摘要、recipient、max_retries、expected failed row_revision/generation、old_queue_id（可空）、new_queue_id、reason、旧条目精确摘要/status/RetryCount 或 `observed_absent=true`、授权时间。调用者显式意图由该协调 API 落盘，**不是**从现有 pending 逆推。先验证所有字段和资格；保持锁，单次 JSON 提交「移除唯一旧条目（如有）+ 新条目 RetryCount=0 + witness」，绝不删除全部同 ID failed 条目来掩盖 duplicate。

这是 **D6/O1 的原子边界**：新 pending 与 witness 同生同灭，不可能只写 queue 再补 witness。随后 T2' 在同锁内以 failed + expected revision/generation + old QueueID CAS，采纳新 QueueID、记录 last_recovery_id 与 witness 原始 bytes 摘要 last_recovery_digest、清 Issue、投影新预算；控制 activation 的 `onFanoutQueuedTx` 仍与 T2/T2' 同 SQLite 事务。

D6 后 T2' 前崩溃时，旧 failed row 未变，新条目携带完整授权，屏障验证并重放 T2'；不要求旧条目仍存在或查询未持久化的历史。witness 的 old evidence 是持锁验证结果的耐久记录，信任边界与本地 SQLite/JSON 相同，不能证明恶意本地篡改。row 已 queued（或后来 accepted/failed）且同新 QueueID/last_recovery_id/摘要说明已消费；任何其他 row/version/QueueID/签名不匹配 hold，不覆盖。未经消费的 witness 禁止被 publish、clear、cleanup 或其他 D6 覆盖。消费后保留到下一次经验证 D6 或终态 O2；已消费 witness 不再要求当前 RetryCount=0，否则正常后续 O3 会被误判。

#### 3.5 确定性对账与精确清理（D4、D14；发现 5、6）

`FanoutReconcileReport` 每项提供 fanout key/EventID、primary、稳定排序 details、held、action（none/T2/T2prime/T4/O1/O2）、是否修改与 cleanup outcome；没有匹配 row 的项不伪造 key。`LoadFanoutReport` 仍只读 SQLite；S9/P0-F 的只读展示使用 F3 的 `InspectFanoutEvidence` 在相同锁顺序下取一致 snapshot，合并 diagnostics/预算已知性 overlay，**不重放屏障、不修改 Issue**。无法读 outbox 时预算展示 unknown，而非把旧快照当当前值。F1 提供可空预算/report 数据形状，F3 提供只读证据 API，S9 负责展示。

`ReconcileFanout` 不发布、不签名。扫描本身份所有 fanout 行（包括 failed/accepted）及整份 outbox 的所有 route；通过已验证 signed event 的 sender 归属身份，其他身份有匹配 row 的正常条目不算本身份 orphan，但仍参与 EventID/QueueID 碰撞枚举。宣告 orphan 前须查询完整 sender 身份/EventID 对应 row 确认确实不存在，不能把当前分页或本身份过滤漏出的 row 当作不存在；纯决策输入是严格 raw evidence，不是仅 `[]types.OutboxEntry`。报告每个 EventID **一个 primary diagnostic**，其余证据放排序稳定的 details。以下 first-match 顺序互斥；正常状态表仅在前置检查全部通过后运行：

| 优先级 | 精确谓词 | primary / 动作 |
| --- | --- | --- |
| 0 | 无法可靠解析/枚举 raw snapshot | `queue_corrupt(scope=file)`；该文件群操作全部 hold |
| 1 | SQLite 同键/同身份 EventID 重复，类型/固定字段/ACK 不变量坏 | `fanout_corrupt`；hold |
| 2 | 同 EventID 全 route 条目数 >1 | `queue_duplicate`；即使其中有坏条目/不同 QueueID也以 duplicate 为 primary |
| 3 | 唯一候选 raw/schema/签名/身份/Route/QueueID/计数不合法，或其 QueueID 在别的 EventID 重复 | `queue_corrupt`；hold |
| 4 | **没有匹配 SQLite row**，且剩余恰一有效 group 条目 | `orphan`；hold。普通无 fanout 的 DM 不产生群报告；有 row 时绝不叫 orphan |
| 5 | 有 row，唯一条目合法但与冻结 intent 字段不符；或 queued/accepted 的 QueueID 不同 | `queue_conflict`；D14：保留双方，零 mutation、零 publish、零 delete；绝不 queued→failed→queued |
| 6 | 未消费 result/witness 与其 token、revision、stage 或已消费水位矛盾 | `bookkeeping_conflict`；hold；不能跳过凭据进行下一阶段 |
| 7 | 有匹配的未消费 O3 result 或 D6 witness | 按 §3.3/§3.4 投影一次，再重新判定正常状态；存储失败 `bookkeeping_pending`，停止本项 |
| 8 | 已通过以上检查 | 下表；不再从 held 状态兜底推成 orphan |

优先级 3 的身份/字段校验指结构与自身 signed event 的一致性；与存在的冻结 row 比较归优先级 5。有合法 D6 witness 的 **failed** 行允许在优先级 5 暂不拒绝新 QueueID，留给 6/7 验证；此例外绝不适用于 queued 或 accepted。raw 字段错误可归属 EventID 时走 2/3；不可归属时走 0，规则不依赖遍历顺序。

| row / 唯一有效证据（已过上述优先级） | 动作 | 正常诊断/结果 |
| --- | --- | --- |
| prepared / 0 条 | O1→T2，只用冻结 event；未发布 | `queued_repaired`（后续稳定为 ready） |
| prepared / active group_pending，尚无 result/witness | T2 采纳其 QueueID，计数不增加 | `queued_repaired` |
| prepared / exhausted pending 或 group_failed | 保留双方，零写入/发布/删除 | `prepared_held` |
| queued / 同 QueueID active pending | 无写入；只有 R/S 成功后重试循环才可 P | `ready` |
| queued / 0 条 | CAS failed/queue_missing，保留原 QueueID/计数；不得自动 O1 | `queue_missing` |
| queued / 同 QueueID exhausted pending 或 group_failed | CAS failed/retry_exhausted，投影真实预算但不增加 attempts/generation；保持队列原貌 | `retry_exhausted` |
| failed / 0 条或同 QueueID exhausted pending/group_failed | 只读，等待显式 D6 | `failed_held` |
| failed / active pending（同/不同 QueueID），无可消费 witness | 零写入/发布/删除 | `recovery_unauthorized` |
| failed / 不同 QueueID exhausted pending/group_failed，无 witness | 零写入/发布/删除 | `queue_conflict` |
| relay_accepted / 0 条 | 不写；ACK 终态稳定 | `accepted_clean` |
| relay_accepted / 同 QueueID 唯一有效条目 | 仅精确 O2；绝不 P | 已确认删除=`accepted_clean`；删除失败或持久性未知=`cleanup_pending` |

其余组合（例如 prepared 带不可解释的 witness/result）以优先级 6 `bookkeeping_conflict` hold，不是默认采纳。报告里 `held=true` 与实际动作独立于 row.State；ready/accepted_clean 为无故障结果。首次修复可报告 action=queued_repaired，重复运行不必有相同 action，但 durable state 与稳定诊断必须幂等。

`stale_attempt` 是回调结果诊断：通过结构校验后 token 非当前或已完成，零写入；不覆盖 snapshot 的 primary。每轮输出按 fanout key/EventID 排序。`queue_duplicate/queue_corrupt/fanout_corrupt/queue_conflict/orphan/prepared_held/failed_held/recovery_unauthorized/bookkeeping_conflict/bookkeeping_pending/cleanup_pending/stale_attempt` **仅为 reconciliation/operation diagnostics，不写 RecipientDelivery.Issue**。持久 Issue 仅在获授权的状态变更写入：queue_missing、retry_exhausted、当前 O3 的 send_failed；S/T2/T2'/T3 清除相应旧 Issue，R 不清。缺 handler 返回 `SendResult.Issue=route_handler_missing`，不修改行；I/O 错误作为 operation diagnostic，不能假写投递失败。

**O2 证据条件**：重新取锁和 SQLite 写锁/快照；通过全部 raw/duplicate/冲突检查；row 已 relay_accepted、持久 queue_id 非空、relay_acks=1、accepted_at>0；候选唯一、Route/status 合法、EventJSON/recipient/max_retries 全匹配且 QueueID 精确相同；无未消费凭据。只删除这一 entry。0 条是幂等成功；任何 mismatch/duplicate/corrupt 优先级高于 cleanup_pending，保留全部条目。删除后 fsync/rename 不确定只报告 cleanup_pending，不清 ACK、不猜删除成功；下轮重读，缺条目才 accepted_clean。不能凭 `BeforePublish skip=true` 或任意成功回调删除整个 EventID 集合。

#### 3.6 接线与启用门槛（D7、D11、D13）

保留 `AttemptOptions{Handlers; KeyStore}` 的 keystore 兼容语义和 `SendResult.Issue`；DM 发布行为不变。群接口升级为 F3 协调 API/token 与只读证据 InspectFanoutEvidence，不沿用 approved #166 的无 token handler 作为安全边界。`messaging` 定义接口、`groupchat` 实现，app 层 `SetGroupOutboxProvider` 注册一次，避免循环 import。nil provider/handler 一律 route_handler_missing，不发布、不删队列、不写 DM。

S8a 的 daemon/CLI outbox retry、S8b 的 TUI、S9 的 send/retry 都必须先恢复屏障/对账，再用严格待发集合；不能绕过 R/S/O3 协调 API。F2–F4 涉及的 clear/cleanup/通用 writer 必须一并审计。旧 DM 数据兼容不等于旧进程可与新群 writer 共存；DM 也能整文件覆盖群凭据，因此启用 gate 覆盖整份共享 stores 的全部 writer。

##### 3.6.1 B1：有条件的受控部署边界（F3/V7，S7 前置）

**明确选择方案 2：外部受控安装的部署前提，不声称 OS 隔离任意同 UID 程序。** 指定一个 installation owner（部署管理员，开发单用户部署则是 HOME 所有者本人）独占管理该 HOME、安装目录、所有启动定义和升级权。受支持操作只经其登记的入口；参与者承诺不直接执行未登记二进制/解释器脚本来访问此 HOME。HOME 与数据目录不得多人/网络共享，不得有无法盘点的同 UID 自动化。管理员/同 UID 任意复制旧 binary 后直接指向 live HOME、手动 SQL/文件编辑、恶意绕过 launcher **明确在支持/威胁模型之外**；Unix 同 UID 文件权限不能阻止它们，本文也没有新存储隔离来阻止它们。普通无法维持此约束的个人 HOME 不可激活新协议；S7 在前提未满足的部署持续 BLOCKED。

**持续边界的具体载体**：installation owner 管理固定绝对路径的 launcher、关闭/启用状态和安装清单；清单绑定 canonical HOME、outbox/SQLite 的 canonical 路径（含 SQLite WAL/SHM、outbox sibling/temp 路径）、唯一允许的新 binary 绝对版本路径与内容 hash，以及每一个入口。所有 managed 入口只能调用该 launcher，launcher 在执行前校验该 tuple/owner/路径，closed、缺项、hash 不符、legacy 版本请求或 HOME override 不符均在打开 stores 前拒绝。通过检查则 exec 固定版本路径，不经 PATH 查找或用户提供的 executable 参数。安装树/启动定义/清单的改动只能经 owner 的停机流程，激活时冻结写权限与版本链接；这属于受信任 owner 的操作约束，不是防同 UID 攻击。不能只给旧 binary 增加不会被它读取的 marker。

| 必须登记的 surface | 指向新版本与持续控制 |
| --- | --- |
| CLI（含一次性 agent msg、storage outbox retry/clear、groupchat/control） | shell PATH、alias/function、命令缓存及登记绝对路径全部核查；清缓存/重开 shell；旧安装路径移除或换成仅调用固定 launcher 的 stub，旧版本参数拒绝 |
| TUI（含桌面/IDE 启动、内部 outbox worker） | 登记启动命令及其子进程；旧会话完全退出；桌面入口/IDE task 也只能调 launcher，不能继续持有旧进程 |
| daemon 与服务管理器 | launchd/systemd/其他 manager 的 executable、环境 HOME、自动重启策略逐项登记；升级前卸载/停止自动拉起，重启只调用 launcher |
| cleanup、脚本/automation | 内置 cleanup 随新 binary；cron/timer、shell 脚本、CI/IDE task、备份恢复/维护脚本和自建 helper 逐项登记，包括 DM enqueue/send 后清理；禁止直接写 stores。持久化库的自建调用程序也是 executable，须迁到已评审新版本或退役 |

**writer/路径盘点交付物**：F3 从目标合并树列出 `SaveOutbox`/writeOutbox 整体替换、`UpdateOutbox` **所有调用方**、`currentOutboxAttempt` 自动 ID 路径、clear、CleanupOutbox、UpdateOutboxStatus/IncrementOutboxRetry、删除/成功清理、DM enqueue/发送失败、group enqueue/requeue/handler，以及所有 SQLite/group schema migration、transitionFanoutTx、T1/T2/T3/T4、R/S、D6/T2'、reconcile/control activation 路径。每项映射源码符号→可达 executable→launcher→实际 HOME/JSON/DB 路径；包括直接 SQL/文件工具和恢复脚本，不能只列 grep 命中的公有函数或 GetPending。记录目录 owner/权限、软硬链接/其他路径别名与挂载，拒绝未受控别名。盘点必须在 F3 评审关闭且每次安装变更更新；本文列表不是已完成的全量审计。

**升级/重启/回滚顺序（均为未来 F3 交付要求）**：

1. owner 先关闭全部登记入口，禁用 service/timer/automation 自动重启和新 CLI/TUI 会话；保存入口/路径 inventory。等待每个已启动 writer 退出，停止在途 worker 并 wait 子进程；必要时终止旧进程后确认退出。结合 manager 状态、已登记进程树及打开文件检查确认 quiescence；单次 process scan 不足，入口在整个维护期保持关闭。任一 owner、遗漏进程、别名或调用链无法验证则不迁移、不发布、不写新 schema。
2. stores 全部静止后保留一致的 JSON/SQLite/WAL 恢复备份。移除所有登记旧可执行文件、包管理器旧链接、构建输出/脚本内固定旧路径；如保留离线归档，不得位于登记可执行入口，恢复它必须重新走关闭 gate。将所有入口绑定到经 F1–F4 独立评审的新 version/hash，package auto-update/rollback 也必须进入同一维护流程。不得修改 approved #165/#166。
3. 仅 owner 的新版本维护入口在入口仍关闭时执行迁移/raw 审计/合法旧条目 adoption；无 token 的旧回调不能复用。迁移失败保持关闭，不开旧程序“修复”。跑 V7 的逐入口解析/legacy 拒绝验收并保存证据后才激活；新 daemon/TUI/automation 经 launcher 冷启动，先 recovery barrier 后 writer/P。
4. 激活后每次 managed launch 重新核对版本/hash/owner/路径，禁止入口接受 legacy executable。安装变更必须先关入口并停所有 writer，不能在活进程旁切回旧版本；长驻进程在受控维护中一律退出/重启。owner 持续维护 inventory 与配置控制，而不是只在首次启动扫描；发现配置漂移/控制丢失立即关闭入口、停止长驻 writer/调度，保持 stores 与未消费凭据，诊断后人工重新盘点。关闭新 writer 无法撤销已经发生的越界旧写；这种情况视为边界失效，不能继续声称存储正确。
5. 升级/重启崩溃默认保持 closed，只有同一允许的新版本可在维护模式检查两 stores、完成屏障，再重新开放。**禁止旧版原地回滚 live stores**。激活前可在全部退出后恢复完整迁移前备份；激活后旧版回滚必须另立离线恢复方案，将 live HOME 与所有入口退役，恢复完整一致的迁移前快照到隔离旧 HOME，明确丢失后续本地状态且无法撤回网络 P/ACK；不提供自动降级、不混用新旧 JSON/DB。没有可验证恢复方案就保持关闭，前向修复。

本节控制能力在当前代码尚不存在；F3 必须实现受控 launcher/安装维护与 fail-closed gate 并交付 V7，单独评审/合入后才满足依赖。若实际安装无法完整控制，rev7 在该环境仍 **BLOCKED**，须先另行评审存储/UID 隔离方案；不能以“测试绿了”声称任意 same-HOME writer 已被排除。

rev7 已获独立设计评审 PASS（不含实现或运行时验收）。先完成并合入 F1–F4，才可 S7；受影响 S8/S9 接线与 S10b 队列副作用也依赖这些前置。依赖、owner 与验收编号见[实现计划](m1-group-fanout-impl-plan-20261008.md)。

#### 3.7 必须可观察的验收

- **V1**：F1 迁移/类型/allowlist；R 后 S/P 前 kill：generation +1，attempts/RetryCount/时间/relay_count/Issue 原值，P=0；重启后新 R 再 +1。S 后 P 前 kill 单列：attempts +1、结果未知、预算不变。legacy 基数有明确标签。
- **V2**：两 Store、两个真实进程、同 HOME 同 QueueID 的 A/B 交错；B 预留后 A 晚失败/ACK，SQLite 与 outbox 均不变；覆盖 O3 校验前后抢锁与 T3/O2 后迟到回调，证明 fence 覆盖实际 RetryCount/status 写。
- **V3**：O3 JSON commit 后 T4 前 SIGKILL；重启强制屏障准确重放一次，RetryCount 不再增加；T4 后再 kill 同样幂等；含 exhausted、write/dir-fsync uncertain、不能恢复时阻止 B reservation。
- **V4**：D6 missing / exhausted pending / exhausted failed，原子写 witness 后 T2' 前 kill；新 QueueID 可凭耐久 witness 恢复；同旧 QueueID pending、无 witness、伪造/错 revision、重复请求均不得重置预算。queued+不同 QueueID exhausted 仍零修改。
- **V5**：枚举全部 row state × 0/1/2 条 × 两种 pending 预算区间/failed × same/different/missing QueueID × raw/witness/result 状态，验证 §3.5 first-match、primary/details 与 mutates/publishes/deletes；orphan 只在无 row；accepted 清理 uncertain 后报告稳定。
- **V6**：交付 §3.2 两个源路径复制 fixture 与其原始 hash，覆盖 null-relays 的 adoption、budget initialization、O3/T4 与 D6/T2' recovery、DM default-relay 行为，及新建默认选择 []；每步比较 relays/event 原始片段，后续更新保留已消费 result/witness 原片段。再测字段级缺失/null、optional/条件字段、重复/未知 key、大小写/转义、group Route/QueueID、跨 route EventID、坏 event、数值溢出；在 normalize/auto-ID 前拒绝并保留双方。不得把 valid relays:null 判为损坏。
- **V7**：F3 在 S7 前交付 §3.6.1 完整 writer/caller/SQLite/group 路径清单、owner/权限/版本 hash/每个 launcher 解析证据及关闭→quiesce→升级→冷重启记录。对表中**每个登记入口**在激活时、O3 JSON 已落盘且 T4 未提交、D6 已落盘且 T2' 未提交（含 kill 后）尝试请求/启动 exact base、#165、#166 的旧 writer：旧路径不存在、launcher 拒绝或 stub 仅转发新版本；记录实际 exec 路径/hash 与拒绝码，并用 OS 文件访问追踪/测试审计证明**没有旧进程打开 live stores**。转发只以只读 probe 验证；拒绝/不存在测试前后 JSON bytes 与 SQLite 逻辑 snapshot 不变，显式恢复后 receipt/witness 只投影一次。覆盖一次性 DM enqueue、clear、cleanup、TUI/daemon 重启、自动化和 group 回调；单测 flock/marker 或只观测“未丢数据”不算入口排除证据。另注入未知入口、owner/hash/HOME 失配、自动重启未关、升级中断、回滚请求，验证 fail closed；未满足条件不得启动 S7。此结果仅证明受控入口排除旧版本，**不证明任意 copied same-UID/same-HOME executable 被阻止**，该绕过明确不在支持模型内。
- V7 还覆盖合作 writer 锁顺序/屏障、clear/cleanup 保护，T1/O1/T2/R/S/P/T3/O3/T4/D6/T2'/O2 全持久边界故障注入、DM 历史 0 群行与冻结 event 不变；单元/race 之外需真实子进程锁与 SIGKILL 测试。

---

## 4. inbound 路由

### 决定

在每个现有 DM 持久化调用**之前**，用 #145 的 `messaging.VerifyAgentMessage` 一次完成验签、单收件人校验和解密，再按 `Route()` 做 typed 分流。共有三个插入点（这三处就是代码中仅有的 inbound DM 写入点）：

| # | 文件 | 函数 | 当前在此之前写 DM | 改动 |
| --- | --- | --- | --- | --- |
| R1 | `internal/messaging/inbox_watch.go` | `storeIncomingWatchEvent` | `store.StoreIncomingMessageOnce(...)` | 用 `VerifyAgentMessage(*event, recipientSK)` 替换 `validateInboxEvent` + `decodeInboxContent`；`switch msg.Route()`：`AgentRouteDirect` → 原 DM 路径；`AgentRouteReservedGroup` → `opts.Group.HandleReserved(ctx, msg)`；`AgentRouteInvalid` / 错误 → emit 脱敏错误后 return |
| R2 | `internal/daemon/daemon.go` | `processIncomingEvent` | `hooks.store(...)`，之后是 `logf` / `notify` / `shouldAutoReply` | 用 `VerifyAgentMessage` 替换现有 `ValidateAgentMessageEvent` + 末个 `p` 匹配 + `DecodeMessageContent`（同时修掉「多个 `p` 时只看最后一个」的宽松检查）；reserved 分支调用 `hooks.group` 后**直接 return**，不经过 `logf` 正文、`notify`、`shouldAutoReply` |
| R3 | `internal/messaging/agent.go` | `AgentInboxCmd` 的 Action 循环 | `StoreIncomingMessageOnce(&evt, ...)`（仅 `autoDecrypt` 时） | 解密时改用 `VerifyAgentMessage`；reserved 分支不写 DM、不加入 `entries` 展示列表，改为在输出中计数为 “N group messages (use `hyphae groupchat`)”（命令命名见 **D8**）。`--decrypt=false` 时维持现状（不持久化密文，显示 `[encrypted message]`），因为无法分类，也不会写库 |

注入接口（`internal/messaging`，避免 import cycle）：

```go
type GroupInboundResult struct {
    Stored bool        // 首次 durable 写入（消息入历史，或控制 envelope 引起状态迁移）
    Kind   string      // "message" | "invite" | "accept" | "decline" | "activate" | "cancel"
    GroupID, LogicalID string
    Transient bool     // 见第 6 节：顺序未到（如激活前收到成员消息），不得视为已处理
}

type GroupInboundSink interface {
    HandleReserved(ctx context.Context, msg VerifiedAgentMessage) (GroupInboundResult, error)
}

type InboxWatchOptions struct {
    Store *storage.MessageStore
    Group GroupInboundSink // nil 时 reserved 消息一律拒绝（fail-closed），绝不回落 DM
}

func WatchAgentInboxWithOptions(ctx context.Context, nickname string, relays []string,
    opts InboxWatchOptions, emit func(AgentInboxWatchUpdate)) error
```

`WatchAgentInboxWithStore` 保留为 `Group: nil` 的薄包装，旧调用方行为只会变得更严格（reserved 消息不再进入 DM）。`AgentInboxWatchUpdate` 新增 `Group *GroupInboundResult`；DM 的 `Message` 字段语义不变。daemon 的 `incomingReceiveHooks` 新增 `group func(context.Context, messaging.VerifiedAgentMessage) (messaging.GroupInboundResult, error)`。

`groupchat` 侧实现（P0-F，`internal/groupchat/inbound.go`）：`FromVerifiedAgentMessage` → `Decode`（未知版本得到 `ErrUnsupportedVersion`、坏 magic/坏 JSON 得到错误）→ 按 `Envelope.Type` 调用 `ReceiveMessage` / `ReceiveInvite` / `ReceiveAcceptance` / `ReceiveActivation` / `ReceiveDecline` / `ReceiveCancel`。其中会返回待发送 envelope 的方法（如 `ReceiveAcceptance` 返回激活 envelope），其结果经第 3 节 T1（`envelope_type` ≠ message）进入 fanout，不在路由函数里直接发布。

路由规则：

- `AgentRouteReservedGroup` 包含未知版本和损坏 magic（#145 只看前缀）。它们只能失败关闭：不写 DM，不写群，emit 只带 event ID 前缀与错误类别，不带 payload。
- 明文（未加密）的 reserved 消息由 `FromVerifiedAgentMessage` 拒绝，同样不回落 DM。
- 普通 JSON / 普通文本仍是 `AgentRouteDirect`，DM 行为不变。
- TUI 由 app 层把同一个 `*sql.DB` 构造的 `groupchat.Store` 装配成 sink 传入 R1；daemon 在 R2 装配同一实现。不新开订阅（I2），也不需要外部 daemon。

### 为什么不是另一个方案

- **先写 DM 再识别群消息并删除/隐藏**：在两步之间崩溃或被其他读者读到，群 envelope 就已进入 DM 历史；auto-reply 在 R2 中紧跟 store 执行，来不及拦截。
- **群单独开一条订阅 / 用 `groupchat.VerifyIncoming` 再解密一次**：违反 I2，并让两条路径的校验可能漂移；#145 已让 `VerifyIncoming` 也委托到 `VerifyAgentMessage`。
- **只改 R1（TUI watcher）**：daemon（R2）和 `agent inbox`（R3）会继续把群 envelope 当 DM 存储、通知并 auto-reply。
- **sink 缺失时回落 DM**：直接违反 I3；fail-closed 的代价只是「未装配的进程看不到群消息」，而装配了 sink 的进程会通过 relay 历史回放取回。
- **在 `storage.StoreIncomingMessageOnce` 内部检测前缀并拒绝**：存储层看到的是已解密正文，在那里拒绝会让调用方误判为存储错误，并且 daemon 的 `notify`/auto-reply 在其他分支仍可能拿到正文；分流要放在调用方、在任何副作用之前。作为纵深防御，P0-F 仍可在 `StoreIncomingMessageOnce` 加一道「明文以 `hyphae.group/` 开头即返回错误」，但这不能替代 R1–R3。

### 验收

- 单元 R1/R2/R3 各一组：输入 v1 message / invite、`hyphae.group/v99\n{}`、`hyphae.group/broken`、明文 reserved、普通 JSON 私聊。断言：前四类 `messages` 表行数为 0；R2 的 `notify` 与 `reply` hook 调用次数为 0；普通 JSON 进入 DM 且内容不变。
- 单元：`Group == nil` 时 reserved 消息不写任何表，emit 一条错误且错误文本不含 payload。
- 单元：R2 中多 `p` 标签 event 被拒（回归现有宽松检查）。
- E2E：daemon 以 `--auto-reply` 运行时 Alice 向群发言；Bob 的 daemon 日志无 auto-reply 发送记录，Bob 的 `hyphae history` 无该条，Bob 的群历史（`groupchat_messages`）有该条。

---

## 5. ACK 语义与迁移约束（D14）

NIP-01 `OK true` 仅表示至少一个 relay 接受 event，不证明对方取到/已读。`relay_acks=1` 是布尔证据，`relay_count` 是最近 S 的目标数，显示 `relay accepted (≥1 of N)`；M1 不增加 delivered/read 或回执 envelope。

本表约束的每格还必须通过 §3.2 raw 检查、§3.3 锁/CAS/屏障与 §3.5 优先级；状态等级本身不能授权写入。

| from → to | prepared | queued | failed | relay_accepted |
| --- | --- | --- | --- | --- |
| prepared | T1 重入只读 | 仅 O1 唯一有效 active pending + T2 | 拒绝；O1 失败保持 prepared，返回 operation diagnostic | 拒绝，无 durable queue/ACK |
| queued | 拒绝 | 同 QueueID 的 R/S/当前非耗尽 O3→T4；D14 不换队列 | 当前 O3 耗尽，或 §3.5 同队列耗尽/queue_missing | 仅当前 started token 的 P OK→T3 |
| failed | 拒绝 | 仅 D6 witness + T2'，新 QueueID | 只读，或未消费同一授权结果的幂等投影 | 拒绝；先前无 token 的“延迟 ACK”不能绕过 fence |
| relay_accepted | 拒绝 | 拒绝 | 拒绝 | 不改 row；只允许 §3.5 精确 O2 |

- 冻结 intent 主键、event_id/event_json、recipient、created_at、max_retries。所有 INTEGER 字段先严格类型/范围检查，拒绝未知字段、负数、溢出、格式错误文本；canonicalNpub 在读/写一致。多 recipient created_at 不同必须报错，不能取排序首行。
- QueueID 仅 T2 从空引入、T2' 持有效 witness 从 failed 换新；其余迁移不替换、不清空。queued + 不同 QueueID 保留双方、零 mutation/publish/delete，不能借 exhaustion 洗成 failed 再 queued。
- T3 必须有当前 started token、唯一匹配的非空持久 QueueID、relay_acks=1 与 accepted_at>0；终态所有字段冻结。新 generation 后旧 ACK/失败都不能修改任何 durable record 或触发 O2。
- 计数、时间、Issue 唯一定义在 §3.1/§3.5：generation 不等于 attempts，不用 MAX 固定 relay_count，不用 attempts/MaxRetries 表示预算。报告只取耐久证据；诊断不冒充 ACK 或 delivery Issue。
- 逐格测试允许/拒绝与完整不变字段；结合 V1–V7 检查 stale O3、恢复 witness 与精确清理。所有 CLI/TUI 文案禁止 delivered/已送达/已读。

---

## 6. 重放与去重

### 决定

#### 接收端事务边界

```text
relay event ─▶ VerifyAgentMessage（纯函数，无写入）
            ─▶ Route 分流（纯函数）
            ─▶ groupchat.Receive*：一个 SQLite tx 内完成
                 授权检查（签名者 authority、active roster、recipient == envelope target、
                          creator/group/invite/roster hash 绑定）
               + 去重（groupchat_messages 主键 (local_npub, group_id, logical_id)、UNIQUE(event_id)；
                       邀请/成员表主键）
               + 状态迁移 / 历史插入
            ─▶ commit 成功且 first=true ─▶ emit typed update（TUI 刷新、daemon 计数）
            ─▶ daemon 内存 seen.Add(event_id)（仅 commit 之后；只是缓存）
```

- 只有 commit 之后才通知界面；`first=false`（重复）不 emit；出错不 emit 成功、不进 seen。
- 去重以 SQLite 为权威：relay 历史 walk 与 live 订阅重叠、多 relay 重复投递、daemon 与 TUI 同时处理、重启后重放，都靠上述主键和 UNIQUE 约束收敛为一次。
- 同一 logical ID、内容不同 → `ErrLogicalIDConflict`，拒绝且不覆盖已存正文（防止成员重放篡改）。
- 生命周期 envelope 的重放由 #138 的状态迁移保证幂等且不倒退（`left`/`cancelled` 不可被激活重放复活）。

#### 顺序未到（transient）

Carol 的群消息可能先于 Alice 发给 Bob 的激活到达 Bob。此时 `storeMessage` 返回 `ErrGroupNotActive`，但这个结果不是终态。规则：

- sink 必须区分**暂态**（本机有该群、本机邀请为 `accepted`、群为 `pending`/`activating`）和**终态**（未知群、`cancelled`、`left`、roster 不符、坏 envelope）。暂态返回 `Transient=true`：不写入、不 emit 成功、不进 seen。
- 本机任一群激活 commit（`ReceiveActivation` 返回 first=true）后，R1 watcher 对每个 relay 立即再执行一次已有的 `relayquery.Walk`（与连接时的历史恢复使用同一函数、同一过滤器），回收被暂缓的消息；daemon 的下一轮扫描会自然重新处理，因为暂态事件未进入 seen。不新增暂存表，不在本地保存解密后的暂态明文。

#### 发送端事务边界

见第 3.3 节 T1–T4 / O1–O3 / R/S。发送端本机历史只在 T1 写一次；重试/对账仅按契约更新 fanout 和队列证据，不触碰 groupchat_messages；过期 ACK 不写入，所以本机不会重复显示。

### 为什么不是另一个方案

- **先 emit / 先标 seen、后写库**：崩溃或写库失败会让界面显示一条不存在的消息，或让 daemon 永久跳过它。
- **以 event ID 为接收端去重主键**：即使发送端违反 I4 重签了同一 logical ID，接收端也必须只显示一次；logical ID 主键加 event ID UNIQUE 两层都要。
- **暂态消息直接丢弃**：Bob 将永久缺少激活前到达的群消息，直到下次重连才会恢复，而 TUI 长时间不重连。
- **暂态消息存入本地暂存表（含明文）**：会多出一份不受 group 状态约束的明文副本，还需要清理策略；重新 walk relay 复用已有路径，代价只是一次历史分页。

### 验收

- 单元：同一 event 连续投递 3 次、跨两个 relay 各投递一次 → 群历史 1 行，emit 1 次。
- 单元：同 logical ID 不同 event ID、同 body（模拟发送端重签）→ 1 行；同 logical ID 不同 body → `ErrLogicalIDConflict`，原行不变。
- 单元：先投递成员消息（暂态）再投递激活 → 激活 commit 后触发 re-walk，消息入历史 1 次；对 `cancelled` 群投递消息 → 终态拒绝，re-walk 后仍不入库。
- 单元：在 commit 前让 emit 回调 panic/失败的注入用例中，DB 无行时界面不得收到更新。
- E2E：三人交替各发 3 条唯一正文，然后三端全部重启并强制完整历史 walk；各端群历史恰好 9 条、顺序一致，无 DM 行，状态不倒退。

---

## 7. #138 / #145 接口缺口（只列出，不改其文件）

| ID | 缺口 | 影响 | 负责人 | 关闭条件 |
| --- | --- | --- | --- | --- |
| G1 | 已关闭：base 的 S2 已有 storeMessageTx | S4 可在同事务写历史/fanout | S4 owner | 使用已有 Tx 原语；T1 失败回滚测试 |
| G2 | `MarkActivationQueued(localNpub, groupID, inviteID, eventID string, queued bool)` 以布尔表达「已入队」 | 与第 3 节「不得用布尔代替证据」冲突；调用方可在无 outbox 证据时激活成员 | #138 作者 | 去掉 `queued bool`；改为要求 `groupchat_fanout` 中 `(group_id, 'activate', invite_id, invitee)` 行存在、`event_id` 相同且 `state ∈ {queued, relay_accepted}`，在同一事务中校验。由 S10b 在 F1–F4 后完成，控制激活与 T2 同事务并通过评审 |
| G3 | `storeMessage` 对 `pending`/`activating`/`cancelled` 一律返回 `ErrGroupNotActive` | sink 无法区分暂态与终态（第 6 节） | P0-F 实现者 | 在 `groupchat` 新增 `ErrGroupActivationPending`（本机邀请 accepted 且群未激活时返回），`cancelled` 改返回 `ErrGroupCancelled`；P0-F 合并前附第 6 节暂态单元测试 |
| G4 | `ReceiveAcceptance` / `CancelGroup` 等返回 `[]Envelope`，但没有说明由谁加密/签名/入队 | 控制 envelope 可能绕过 durable intent 直接发布；状态已提交而 envelope 未持久化时无法找回 | P0-E 实现者（S10a、S10b） | （**D9**）**状态迁移与 fanout 行插入在同一事务**：S10a 为 `CreateGroup` / `AcceptInvite` / `DeclineInvite` / `ReceiveAcceptance` / `CancelGroup` 提供 Tx 变体（原公开方法变薄包装，行为不变），S10b 的 `*WithFanout` 在一个事务内调用 Tx 变体并经 `prepareFanoutTx(envelopeType, sendKey=invite_id, ...)` 插入 fanout 行；S10b 合并前附「控制 envelope 也有 fanout 行」与「fanout 插入失败时状态迁移一并回滚」的测试 |
| G5 | （已关闭）#145 曾未合并，`VerifyAgentMessage` 不在当时的 main | 第 4 节 R1–R3 依赖它 | #145 作者 | #136→#138→#145 已依序合入当前 main；P0-F 可按第 4 节接线，无需重新提交该边界 |
| G6 | `groupchat_messages` 的 `UNIQUE(event_id)` 是全库唯一而不是按 `local_npub` 唯一 | 同一 HOME 多身份时没有实际冲突（单 `p` 保证 event 不同），无需修改；在此记录以免实现者误改 | P0-F 实现者 | 无需修改；P0-F 增加「同 HOME 两身份各收一份」测试，作为不需修改的证据 |

**D9**（约束 S10a、S10b）：
- 决定：G4 的关闭条件由「结果经 `PrepareFanout` 进入 T1」收紧为「**状态迁移与 fanout 行插入在同一 SQLite 事务**」，实现路径为 S10a 的 Tx 变体 + S10b 的 `*WithFanout`。
- 理由：原文没要求同一事务。`AcceptInvite` 可重入，但 `CreateGroup` 每次生成新 group ID、`CancelGroup` 要求 `StatePending`：若状态先提交、再在另一个事务里插 fanout 行，两步之间崩溃后再调用一次要么得到另一个群，要么因状态已变而报错，原 envelope 永远拿不回来，对方永远收不到邀请 / 取消。
- 为什么不是另一个方案：「先插 fanout 行、后迁移状态」在状态迁移失败时会留下已签名、可被重试循环发布的控制 envelope，对方收到一个本机并未发生的状态变化；「状态提交后靠对账补发 envelope」需要从状态反推 envelope 内容（含新 group ID、roster hash），等于第二套构造逻辑，违反 I4 的「只重发持久化的 event」。

## 8. 交付拆分（供 coordinator 派发）

- **P0-E**：`messaging.BuildAgentMessageEvent`（加密路径，D10）；`groupchat/fanout.go`（表、`PrepareFanout`（T1，D12）、T2–T4、`ReconcileFanout`（D4）、`FanoutReport`（D2））；`OutboxEntry.Route` + `group_pending/group_failed`（D5）+ 协调恢复 API + witness（D6、F4）；`AttemptSendRouted`（D11、D13）与三个重试方经 `SetGroupOutboxProvider` 接线（D7）；`hyphae groupchat {send,retry,status}`（D8）；第 1、2、3、5 节验收（E2E 按 D3）；关闭 G1、G2、G4（D9）。必须先完成 F1–F4 的独立 follow-up，不能跳到 S7。切片拆分与顺序见 [实现拆分方案](m1-group-fanout-impl-plan-20261008.md)。
- **P0-F**：R1/R2/R3 分流；`GroupInboundSink` 与 `groupchat/inbound.go`；暂态 re-walk；TUI 群模型的投递摘要展示；第 4、6 节验收；当前 main 已有 #145，可补第 1 节 `Route()==AgentRouteReservedGroup` 断言（D1）；关闭 G3、G6。
- 两者共同完成：真实 relay（`wss://relay.aastar.io`）+ 三个隔离 HOME（alice/bob/carol）的 E2E 脚本 `test_groupchat_fanout_e2e.sh`。现有 `test_group_e2e.sh` 不计入证据（见群聊设计基线）。
