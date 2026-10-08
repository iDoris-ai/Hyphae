# M1 群消息 fanout 与 inbound 路由契约（P0-E/F 前置）

状态：2026-10-08 设计契约，**不含实现**。P0-E（发送 fanout / durable send-intent / 重试）与 P0-F（inbound 路由 / 界面通知）的实现者按本文照做；偏离本文任何一条决定，须先改本文并经评审。

修订 rev2（2026-10-08）：按 [P0-E 实现拆分方案](m1-group-fanout-impl-plan-20261008.md) 第 6 节登记的 D1–D13 修订本文。各条以 `**D#**` 标记写在原章节内（决定、理由、为什么不是另一个方案、约束的切片）；切片编号（S1–S10b、E2E-A/B）以实现拆分方案第 2 节为准。实现 PR 一律以本修订版为唯一依据。

依据与基线（本文写作时的代码事实）：

- 发布计划：[M1 收尾计划](m1-release-plan-20261007.md)「加密群聊」一节。
- 群协议：#136（已入 main）`internal/groupchat/protocol.go`、`verified.go`；设计基线 [M1 群聊最小设计](m1-group-chat-design-20261007.md)。
- 状态层：#138 **已合入 main**（`6c5e751`）`internal/groupchat/store.go`、`messages.go`；写事务统一经 `beginImmediate()`。
- 已验证入站边界：#145（**仍 open**，REVIEW_REQUIRED，基于 #138 合并前的旧栈）`internal/messaging/verified_agent.go` 的 `VerifyAgentMessage` / `AgentMessageRoute`，以及 `groupchat.FromVerifiedAgentMessage`。本文的 inbound 路由**以 #145 为前提**；#145 不合并则 P0-F 不开工。发送侧（P0-E）不依赖 #145。
- 收件守卫：**已合入 main**，形态为 `messaging.RejectReservedGroupPayload`（`internal/messaging/agent.go`），被 `inbox_watch.go`、`daemon.go` `processIncomingEvent`、`AgentInboxCmd` 三处调用，对 `hyphae.group/` 前缀做 fail-closed。它对应第 4 节 R1–R3 的「不回落 DM」半边，是本文的子集，不提供群路由；P0-F 集成时以本文第 4 节为准，guard 并入 R1–R3 的 reserved 分支。（handoff 中记录的未推送 commit `1cf7bb3` 已被该合入版本取代，不再作为基线。）
- 现有发送/重试：`internal/messaging/outbox.go`（JSON 文件 outbox，`UpdateOutbox` 加锁 + fsync/rename）、`agent.go` 的 `sendQueuedAgentMessage`、`queued_agent.go` 的 `AgentMessageDeliveryState`、`internal/tui/offline_outbox.go`（#143）、`internal/daemon/daemon.go` 的重试循环。
- 并行中的 #151（open，CHANGES_REQUESTED）：在同一函数族新增 `AttemptSendWithKeyStore`，并改 `tui/offline_outbox.go`、`agent.go sendQueuedAgentMessage`；与 3.4 的 `AttemptSendRouted` 签名相关，见 **D13**。

**D1**（约束 S4；延后断言归 P0-F）：
- 决定：基线以 `origin/main = 6c5e751` 为准（上列）。第 1 节单元验收中「通过 `VerifyAgentMessage`、`Route()==AgentRouteReservedGroup`」改为「产出的 event 能通过 main 上已有的 `groupchat.VerifyIncoming` 解出逐字节相同的 envelope」；`Route()==AgentRouteReservedGroup` 断言延后，由 P0-F 在 #145 合入后补上（P0-F 开工前提本就是 #145 合入，见 G5）。
- 理由：契约写作时 #138 未合、守卫未推送；现在二者已在 main，而 #145 不在 main。S4 若照旧验收，会被迫等待一个与发送侧无关的 PR。
- 为什么不是另一个方案：「S4 等 #145 合入」会把发送侧关键路径绑到收件侧 PR 上，而 #145 还需按 #138 合并后的主干重做；「S4 自带一份 `VerifyAgentMessage`」会与 #145 产生两份解密边界，违反 I2。`groupchat.VerifyIncoming` 已在 main、覆盖 kind、event ID 与签名、`ValidateAgentMessageEvent`、恰一个且匹配的 `p`、NIP-44 解密并拒绝明文，再经 `groupchat.Decode` 即可比对 envelope，足以证明 event 可被收件侧接受；只缺「按前缀分类为 reserved」这一项，正是延后给 P0-F 的断言。

不变量（全文共用）：

- **I1** 不引入共享群密钥；每条逻辑消息逐收件人 NIP-44 加密。
- **I2** 不新增 relay 订阅、不新增解密实现；入站只经 `messaging.VerifyAgentMessage` 解密一次。
- **I3** 群 envelope（`hyphae.group/` 前缀，含未知/损坏版本）永不写入 DM 表 `messages`，永不进入 auto-reply、DM 通知或 DM 展示。
- **I4** 已签名 event 一旦持久化即冻结：重试只重发同一 event（同 event ID / 同 `d` / 同密文 / 同签名），绝不重新加密或重新签名。
- **I5** 状态单调：任何重放、重启、并发重试都不能让收件人投递状态或群状态倒退，也不能让同一逻辑消息显示两次。

### rev2 修订索引（D1–D13）

| ID | 位置 | 一句话决定 | 约束的切片 |
| --- | --- | --- | --- |
| D1 | 依据与基线、§1 验收 | 基线更新；§1 验收改用 `groupchat.VerifyIncoming`，`Route()` 断言延后到 P0-F | S4（P0-F 补断言） |
| D2 | §2 `RecipientDelivery`、CLI、§5 | `relay_acks ∈ {0,1}`、`relay_count = len(targets)`、删 `Relays`、文案 `relay accepted (≥1 of N)` | S3、S6、S9 |
| D3 | §2 验收 E2E | 投递状态只取决于发送方→relay；E2E 改为全 relay 不可达 0/2→2/2 + Go e2e 注入 1/2 | E2E-A |
| D4 | §3.5 对账表 | 增 `failed`+`group_pending`→T2'、`failed`+`group_failed`→无动作；扫描含有条目的 `failed` 行 | S7 |
| D5 | §3.2 | `CleanupOutbox`/`recordAttemptFailure`/`inspectAttemptQueue`/`isFailedOrStuck` 按 route 取状态值 | S5a |
| D6 | §3.2、§2 手动重试、§3.5 | 新原语 `RequeueGroupOutboxEntry`（单次 `UpdateOutbox` 内删旧 `group_failed` + 加新 `group_pending`） | S5a（S6 使用） |
| D7 | §3.4 | `messaging.SetGroupOutboxProvider(p)`，`main.go` 注册一次；未注册→`route_handler_missing` | S8a（S8b 使用） |
| D8 | §2 CLI、全文命令名 | 新协议命令为 `hyphae groupchat {send,retry,status}`；不碰旧 `hyphae group` | S9 |
| D9 | §7 G4 | G4 关闭条件 = 状态迁移与 fanout 行插入同一事务（S10a Tx 变体 + S10b `*WithFanout`） | S10a、S10b |
| D10 | §1 构造函数 | 只收拢**加密路径**；明文分支与 daemon auto-reply 维持现状 | S1 |
| D11 | §3.4 | `SendResult` 追加 `Issue`；哨兵错误 `ErrGroupRouteHandlerMissing` | S5b |
| D12 | §3.3 | 签名在 T1 事务内；同 `(group, logical_id)` 已有 fanout 行时返回既有行、不再签名 | S4 |
| D13 | §3.4 签名 | 末参改为 `AttemptOptions{Handlers; KeyStore}`；**定稿阻塞在 #151**，闭合条件 = #151 合并或关闭 | S5b |

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
- 单元（**D1**，P0-F，#145 合入后）：同一 event 通过 `messaging.VerifyAgentMessage`，`Route()` 为 `AgentRouteReservedGroup`。该断言不属于 S4 的合并条件。
- 单元（**D12**，S4）：同 `(group, logical_id)` 第二次进入 T1 时签名函数调用次数为 0，返回的 event ID 与第一次相同。
- 单元：同一 `(group, logical_id)` 两次进入 T1 只得一行本机历史；同 logical ID 不同 body 返回 `ErrLogicalIDConflict`，并且不新增任何 fanout 行。
- E2E（真实 relay 抓包）：relay 上两份 event 的 `Content` 都不含正文、群名或 roster 的明文。

---

## 2. 部分发送

### 决定

投递状态**逐收件人**记录、逐收件人报告；整体状态只是派生摘要，不单独落盘。

报告结构（P0-E 新增 `internal/groupchat/fanout.go`）：

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
    RelayAcks     int                                  `json:"relay_acks"`  // D2：∈ {0,1}；1 = 最近一次尝试至少一个 relay 接受
    RelayCount    int                                  `json:"relay_count"` // D2：最近一次尝试的目标 relay 数 = len(targets)
    Attempts      int                                  `json:"attempts"`
    MaxRetries    int                                  `json:"max_retries"`
    LastAttemptAt int64                                `json:"last_attempt_at"`
    AcceptedAt    int64                                `json:"accepted_at"` // relay_accepted 时刻；否则 0
}

type FanoutReport struct {
    GroupID    string                              `json:"group_id"`
    LogicalID  string                              `json:"logical_id"`
    CreatedAt  int64                               `json:"created_at"`
    State      messaging.AgentMessageDeliveryState `json:"state"` // 派生，规则见下
    Accepted   int                                 `json:"accepted"`
    Queued     int                                 `json:"queued"`   // prepared 计入 queued
    Failed     int                                 `json:"failed"`
    Recipients []RecipientDelivery                 `json:"recipients"` // 按 RecipientNpub 升序，稳定输出
}
```

**D2**（约束 S3 的字段定义、S6 的 T3 写入、S9 的 CLI 文案）：
- 决定：M1 中 `relay_acks ∈ {0,1}`，含义是「最近一次尝试至少一个 relay 接受」，**不是** OK relay 的个数；`relay_count = len(targets)`，即该次尝试配置的目标 relay 数；**删除 `Relays` 字段**（上面结构已删）。CLI / TUI 文案统一为 `relay accepted (≥1 of N)`，不显示 `k/N relays` 形式的比例。逐 relay 结果留给后续里程碑（届时需要改 `publishToRelays` 的返回值，并作为新字段追加）。
- 理由：main 上 `publishToRelays` 返回 `bool`，且**第一个 relay 成功即返回**，后续 relay 根本没有被尝试；按原契约填「OK relay 数」只能伪造数据。改 `publishToRelays` 会改动所有 DM 发送路径并与 #151 正面冲突。
- 为什么不是另一个方案：「在 P0-E 改 `publishToRelays` 为逐 relay 发布并返回结果」会改变 DM 发布的时延与行为（原本首个成功即停），超出 S5b「DM 分支零可观察变化」的边界；「保留 `Relays` 字段但留空」会让 `--json` 消费方误以为数据存在。`relay_accepted` 的判定（≥1 个 OK）不受影响，与第 5 节一致。

新增 issue 常量（加到 `queued_agent.go` 现有 `AgentMessageDeliveryIssue` 枚举旁）：`queue_missing`（SQLite 为 queued 但 outbox 条目不见且无 ACK 证据）、`queue_duplicate`（outbox 中同 event ID 多于一条）、`route_handler_missing`（重试方未装配群处理器，见第 3 节）。

派生整体状态，复用现有三值 `AgentMessageDeliveryState`：任一收件人 `failed` → `failed`；否则任一 `prepared`/`queued` → `queued`；否则 → `relay_accepted`。「部分」由计数表达（例如 `queued`，`accepted=1/2`），不新造第四个整体状态。

CLI `hyphae groupchat send`（命令命名空间见 **D8**）：

- 文本输出逐收件人一行，收件人用联系人昵称（无则 npub 前 16 位），且经终端控制字符转义（relay 文案按 **D2**）：

  ```text
  📤 group <name> · logical 3f2a…  accepted 1/2
     bob    ✓ relay accepted (≥1 of 2)  event 9ab1…
     carol  ⏳ queued                    attempt 1/10  event 77cd…
  ```

- `--json` 输出完整 `FanoutReport`。
- 退出码：全部 `relay_accepted` → 0；否则返回 `common.NewExitErrorWithData(common.ErrCodeOther, err, report)`。这与现有 `agent msg` 「已入队待重试仍返回非零」一致，脚本才能区分「全员 relay 已收」和「部分在路上」。
- 文案只说 “relay accepted / 已提交 relay”，不得出现 “delivered / 已送达 / 已读”。

TUI（P0-F 的群模型）：本机发出的每条消息尾部显示派生摘要，例如 `✓ 2/2`、`⏳ 1/2`、`✗ 1/2`；选中该消息展开逐收件人行（同 CLI 字段）。状态更新来自 SQLite 重读，而不是 worker 内存，因此重开 TUI 后显示一致。

重试：

- 自动重试：同一收件人沿用原 outbox 条目（同 QueueID、同 event），由现有重试循环（daemon、TUI outbox worker、`storage outbox retry`）经 `AttemptSend` 发送，见第 3 节分派。
- 手动重试：`hyphae groupchat retry <group-id> <logical-id> [--recipient <npub>]` 只作用于 `failed` 行；它经 **D6** 的 `RequeueGroupOutboxEntry` 把 SQLite 中**原 `event_json`** 重新入 outbox（新 QueueID、**同 event ID**），行转回 `queued`。`relay_accepted` 的收件人永远不重发。
- 只读查询：`hyphae groupchat status <group-id> <logical-id> [--json]` 输出 `LoadFanoutReport` 的结果（同 `send` 的文本 / JSON 格式），不发布、不入队。

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

### 决定

**SQLite 是发送意图与投递状态的唯一权威；outbox JSON 只是工作队列。** 两者靠「冻结的 event ID」对账，不靠布尔。

#### 3.1 send-intent 表（字段级）

P0-E 在 `internal/groupchat/fanout.go` 的 migrate 中新增（不修改 #138 已有表）：

```sql
CREATE TABLE IF NOT EXISTS groupchat_fanout (
    local_npub      TEXT    NOT NULL,             -- 发送身份
    group_id        TEXT    NOT NULL,
    envelope_type   TEXT    NOT NULL,             -- 'message' | 'invite' | 'accept' | 'decline' | 'activate' | 'cancel'
    send_key        TEXT    NOT NULL,             -- message: logical_id；控制 envelope: invite_id
    recipient_npub  TEXT    NOT NULL,
    event_id        TEXT    NOT NULL,             -- 64 位小写 hex，签名后的 event ID，插入后不可变
    event_json      TEXT    NOT NULL,             -- 完整已签名 event（仅密文），插入后不可变
    queue_id        TEXT    NOT NULL DEFAULT '',  -- 已确认的 outbox QueueID；prepared 时为 ''
    state           TEXT    NOT NULL,             -- prepared | queued | relay_accepted | failed
    issue           TEXT    NOT NULL DEFAULT '',
    relay_acks      INTEGER NOT NULL DEFAULT 0,
    relay_count     INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    last_attempt_at INTEGER NOT NULL DEFAULT 0,
    accepted_at     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (local_npub, group_id, envelope_type, send_key, recipient_npub),
    UNIQUE (local_npub, event_id)
);
CREATE INDEX IF NOT EXISTS idx_groupchat_fanout_pending
    ON groupchat_fanout(local_npub, state) WHERE state IN ('prepared', 'queued');
```

- 稳定键 = `(local_npub, group_id, envelope_type, send_key, recipient_npub)`，即「稳定 logical ID × recipient」。
- 恢复证据 = `event_id` + `event_json`（原签名 event）+ `queue_id`（outbox 条目身份）。
- 控制 envelope（邀请/接受/激活/取消）走同一张表，`send_key = invite_id`。这样 #138 的激活「已入队」也能有证据（见缺口 G2）。

#### 3.2 outbox 条目扩展

`types.OutboxEntry` 新增 `Route string \`json:"route,omitempty"\``（`""` = DM，`"group"` = 群）。群条目的 `Status` 使用 `group_pending` / `group_failed`，**不用** `pending` / `failed`。

不变量：`Route == "group"` ⇔ `Status ∈ {group_pending, group_failed}`。不满足时视为损坏条目：不发送、不删除、不写任何历史，该收件人 issue 记为 `send_failed`，条目原样保留供人工检查。

理由：现有 `GetPendingOutbox` 只挑 `Status == "pending"`。旧版本二进制（例如升级后仍在跑的旧 daemon）读到新 outbox 时会忽略未知 `route` 字段；如果群条目仍叫 `pending`，旧 `attemptSend` 会把它发出并调用 `StoreOutgoingMessage`，在 DM 表写一条空正文的出站行，违反 I3。改用新状态值后，旧二进制会跳过这些条目。旧 `CleanupOutbox` 可能删掉 `LastAttempt == 0` 的群条目，但 SQLite 的 `event_json` 仍在，由 3.5 对账恢复，降级而不出错。

**D5**（约束 S5a）：
- 决定：**新二进制**中以下四个函数必须按 `entry.Route` 取对应状态值（DM：`pending`/`failed`；群：`group_pending`/`group_failed`），不得硬编码 DM 值：
  - `CleanupOutbox`：保留条件中的「pending」对群条目指 `group_pending`；从未尝试（`LastAttempt == 0`）的 `group_pending` 必须保留。
  - `recordAttemptFailure`：重试耗尽时群条目写 `group_failed`，DM 条目仍写 `failed`。
  - `inspectAttemptQueue`：判断「仍排队」时群条目以 `group_pending` 为准。
  - `outbox_commands.go` `isFailedOrStuck`：群条目以 `group_failed`、或 `group_pending` 且 `RetryCount >= MaxRetries` 判定。
- 理由：上一段只讨论了旧二进制。main 上新二进制的 `CleanupOutbox` 只保留 `Status == "pending"` 或近期条目，会在 daemon 第一次清理时删掉从未尝试的 `group_pending`；`recordAttemptFailure` 写死 `"failed"`，会把群条目改成 DM 状态，破坏 Route⇔Status 不变量，此后旧二进制与 `GetPendingOutbox` 都会把它当 DM 处理。
- 为什么不是另一个方案：「靠 3.5 对账兜底被误删的条目」把新代码自己的 bug 当作常态降级，且 `queued` + 0 条目会被标 `queue_missing` 而不自动恢复，用户必须手动 retry；「群条目在入队时就把 `LastAttempt` 设为当前时间以躲过清理」是利用清理规则的副作用，一旦 `maxAge` 过期仍会被删，且 `recordAttemptFailure` 的问题仍在。
- 验收（S5a）：`CleanupOutbox` 后 `LastAttempt == 0` 的 `group_pending` 仍在；群条目耗尽后 `Status == group_failed`、`Route == group`；`storage outbox list --failed-only` 列出 `group_failed`；DM 条目在这四个函数中的行为与 main 逐用例一致。

**D6**（约束 S5a 提供原语，S6 `RequeueFailed` 与 `groupchat retry` 使用）：
- 决定：在 `internal/messaging` 新增原语 `RequeueGroupOutboxEntry`。它在**一次 `UpdateOutbox` 回调内**完成：(1) 若同 event ID 已有 `group_pending` 条目 → 不改动，直接返回该既有条目；(2) 否则删除同 event ID 的全部 `group_failed` 条目，并追加一条新的 `group_pending` 条目（新 QueueID、同 `EventJSON`、`RetryCount = 0`）。同 event ID 存在 Route⇔Status 不变量被破坏的条目时返回错误、不改动。普通入队 `EnqueueGroupOutboxEntry` 仍是「同 event ID 已存在即返回既有条目」。
- 理由：`groupchat retry` 要用同一 event ID 换新 QueueID，而原 `group_failed` 条目仍在 outbox：按 3.5「入队前先查同 event ID」会直接返回旧的失败条目，什么都没重排；先追加再删会在两次写之间出现 2 条，被判为 `queue_duplicate`，`AttemptSend` 也会拒绝同 ID 多条。
- 为什么不是另一个方案：「先调用一次删除、再调用一次入队」是两次 `UpdateOutbox`，两次之间崩溃会留下「行为 failed、条目 0 条」，与用户执行 `storage outbox clear` 的状态不可区分；「原地把 `group_failed` 改回 `group_pending` 并清零重试」保留了旧 QueueID，违反第 2 节「新 QueueID」的约定，T2' 的「QueueID 不同即被另一进程重排」判据也会失效。

#### 3.3 发送顺序（每一步都是独立的持久化边界）

```text
T1  SQLite tx（D12）：读 active roster 与群状态（同一快照）
              + StoreLocalMessage 语义写 groupchat_messages 本机行
              + 逐收件人 BuildAgentMessageEvent 加密并签名
              + 为每个收件人 INSERT groupchat_fanout(state=prepared, event_id, event_json)
              —— 全部成功或全部回滚；提交后本机历史才可见
              —— 同 (group, logical_id) 已有 fanout 行：不签名，直接返回既有行
for each recipient（可并行，互不影响）:
  O1  outbox：enqueueOutboxEntry(Route=group, Status=group_pending, EventJSON=event_json)
              —— UpdateOutbox 的 fsync+rename 成功返回才算有证据
  T2  SQLite tx：state prepared→queued, queue_id=<O1 返回的 QueueID>
              —— WHERE state='prepared' AND event_id=?；0 行受影响视为已被并发推进，读回现状即可
  P   relay publish（不持锁）
  成功：
    T3  SQLite tx：state→relay_accepted, relay_acks, relay_count, accepted_at, issue=''
    O2  outbox：按 QueueID 删除条目（attemptSend 现有「先 store、后删除」顺序）
  失败：
    O3  outbox：recordAttemptFailure（RetryCount++；耗尽则 Status=group_failed）
    T4  SQLite tx：attempts++, last_attempt_at, issue；O3 判定耗尽时 state→failed
```

硬性规则：

- **成功状态落盘（T3）之后才清理队列（O2）。** 若 T3 失败，outbox 条目保留，下一轮重发同一 event（relay 按 event ID 去重，接收端按 logical ID 去重），然后再尝试 T3。
- **签名只发生在 T1 事务内（D12）。** T1 提交之后任何路径都只读取 `event_json`，不得持有「重新构造 event」的代码路径。
- T1 提交之前崩溃：什么都没写（事务回滚，已签名但未提交的 event 从未离开进程），用户看到发送失败，可重新发送（新 logical ID，因为本机没有任何记录）。

**D12**（约束 S4）：
- 决定：签名在 T1 事务**内**完成：在同一个 `beginImmediate` 事务里读取 active roster 与群状态、写本机历史行、逐收件人加密签名、插入 fanout 行。同一 `(local_npub, group_id, 'message', logical_id)` 已有 fanout 行时，T1 **直接返回既有行**（报告由既有行构造），**不再签名**、不新增行；body 不同仍按第 1 节返回 `ErrLogicalIDConflict`。
- 理由：roster 与群状态必须和插入的 fanout 行属于同一快照，否则在「读 roster → 签名 → 开事务」之间成员被移除或群被取消，会给已不在 roster 的人签发 event；N 次 NIP-44 加密 + Schnorr 签名在毫秒级，持写锁的时间可接受。重入时不签名，是 I4「event 一旦持久化即冻结」的直接推论：重签会产生同一收件人的第二个 event ID。
- 为什么不是另一个方案：「事务外先签名、事务内插入」需要在事务内重新校验 roster 并在不一致时丢弃签名结果重来，逻辑更复杂且仍有 TOCTOU；「重入时重新签名、靠 `UNIQUE` 冲突回退」会白做 N 次加密，并让「哪一份 event 被持久化」依赖插入顺序，测试无法断言 event ID 稳定。
- 每一步失败都反映为对应收件人的 `Issue`，不吞错。

#### 3.4 `AttemptSend` 按 route 分派

`messaging` 不能 import `groupchat`（`groupchat` 已 import `messaging`）。P0-E 在 `internal/messaging/outbox.go` 新增注入点：

```go
type GroupOutboxHandler interface {
    // 发布前调用：若 SQLite 已是 relay_accepted，返回 skip=true，调用方直接删条目，不再发布。
    BeforePublish(entry types.OutboxEntry) (skip bool, err error)
    // 取代 StoreOutgoingMessage：T3。必须幂等；event ID 不匹配返回错误。
    MarkRelayAccepted(entry types.OutboxEntry, relayAcks, relayCount int) error
    // T4。exhausted 来自 O3 的结果。
    RecordAttemptFailure(entry types.OutboxEntry, exhausted bool, issue AgentMessageDeliveryIssue) error
}

type OutboxHandlers struct{ Group GroupOutboxHandler }

// D13：末参为选项结构；字段集合在 #151 落定后定稿（见下方 D13）。
type AttemptOptions struct {
    Handlers OutboxHandlers
    KeyStore *types.KeyStore // 对齐 #151 的 AttemptSendWithKeyStore；#151 关闭则删除此字段
}

func AttemptSendRouted(ctx context.Context, ob *types.Outbox, entry types.OutboxEntry,
    defaultRelays []string, dialTimeout time.Duration, opts AttemptOptions) (SendResult, error)

// D11：SendResult 追加字段（DM 调用方不读取它，行为不受影响）
type SendResult struct {
    // ……现有字段不变……
    Issue AgentMessageDeliveryIssue // 未尝试或失败时的原因；DM 路径保持零值
}

var ErrGroupRouteHandlerMissing = errors.New("group outbox entry has no route handler")

// D7：进程级注册点
type GroupOutboxProvider interface {
    // 为给定本机身份返回 handler 与对账入口；身份打不开时返回错误
    HandlerFor(localNpub string) (GroupOutboxHandler, error)
    Reconcile(localNpub string) error
}

func SetGroupOutboxProvider(p GroupOutboxProvider)
```

- `Route == ""`：行为与现有 `AttemptSend` 完全一致。现有 `AttemptSend` 改为 `AttemptSendRouted(..., AttemptOptions{})`。
- `Route == "group"` 且 `opts.Handlers.Group == nil`：**不发布**、不写任何历史，返回 `SendResult{Attempted: false, Issue: route_handler_missing}` 与错误 `ErrGroupRouteHandlerMissing`（**D11**）。这样未装配群处理器的调用方不可能把群 event 写进 DM。
- 三个重试方（`daemon.go` 重试循环、`tui/offline_outbox.go` `retryPendingOutbox`、`outbox_commands.go` `storage outbox retry`）都改调 `AttemptSendRouted`，handler 一律经 **D7** 的进程级 provider 取得。群条目的挑选用新函数 `GetPendingGroupOutbox`（`Status == group_pending`）；`GetPendingOutbox` 维持只返回 DM。

**D7**（约束 S8a 定义并接线 daemon / `storage outbox retry`；S8b 的 TUI 复用同一注册点）：
- 决定：新增 `messaging.SetGroupOutboxProvider(p GroupOutboxProvider)`，由 `cmd/hyphae/main.go` 在启动时注册一次（实现为 `groupchat.NewOutboxProvider`）。三个重试方都从该 provider 取 `GroupOutboxHandler`、在每轮之前调用其 `Reconcile`。**未注册（provider 为 nil）或 `HandlerFor` 返回错误时，群条目一律得到 `route_handler_missing`**：不发布、不删除、不写任何历史（安全默认）。
- 理由：`storage outbox retry` 的实现位于 `internal/messaging` 包内，`messaging` 不能 import `groupchat`（`groupchat` 已 import `messaging`），原文「由 app 层传入 handler」在该调用方没有可传参的 app 层。
- 为什么不是另一个方案：「把 `storage outbox retry` 搬到 `cmd/hyphae`」会让 outbox 命令分散在两个包，并改动现有 DM 命令的结构；「`messaging` 定义接口、由各调用方各自构造 handler」需要 daemon 与 CLI 各自知道如何打开 `groupchat.Store`，同样跨包；「未注册时回落 DM 发送」直接违反 I3。全局注册只发生在 `main` 一处，测试可显式注册 / 置空。

**D11**（约束 S5b）：
- 决定：`SendResult` **追加** `Issue AgentMessageDeliveryIssue` 字段，并新增哨兵错误 `ErrGroupRouteHandlerMissing`；DM 路径不设置 `Issue`（保持零值），现有字段语义不变。
- 理由：上一条要求返回 issue `route_handler_missing`，但 main 上 `SendResult` 没有 issue 字段；调用方需要用 `errors.Is` 区分「未装配」与发布失败，以决定写 `route_handler_missing` 而不是 `send_failed`。
- 为什么不是另一个方案：「只返回错误、让调用方解析错误文本」脆弱且不可测试；「新建 `GroupSendResult` 类型」要让三个重试方按 route 处理两种返回类型，分叉 DM / 群的结果处理代码；追加字段对现有 DM 调用方是源码兼容的。

**D13**（约束 S5b；**定稿阻塞在 #151**）：
- 决定：`AttemptSendRouted` 的最后一个参数是选项结构 `AttemptOptions{Handlers OutboxHandlers; KeyStore *types.KeyStore}`，而不是裸的 `OutboxHandlers`。「末参为选项结构」这一形状本修订即定；结构内的字段集合（是否含 `KeyStore`、`AttemptSendWithKeyStore` 是否改为 `AttemptSendRouted(..., AttemptOptions{KeyStore: ks})` 的薄包装）随 #151 结论定稿。
- 阻塞与闭合：**阻塞在 #151**；**闭合条件 = #151 合并或关闭**。负责人：S5b 实现者，在 S5b 开工前提交一个仅改本文的文档 PR 写死字段集合（#151 合并 → 保留 `KeyStore` 并对齐其语义；#151 关闭 → 删除 `KeyStore` 字段）。S5b 不得在该文档 PR 合入前开 PR。
- 理由：#151 正在给同一函数族新增 `AttemptSendWithKeyStore(..., ks)`；若两者各加一个位置参数，会出现 `AttemptSendRouted` / `AttemptSendWithKeyStore` / 二者组合三个入口，签名互相打架。
- 为什么不是另一个方案：「现在就按 #151 当前 head 定死 `KeyStore` 字段」—— #151 处于 CHANGES_REQUESTED，KeyStore 语义仍可能变；「保持 `handlers OutboxHandlers` 末参，等 #151 合入后再加参数」会在 S5b 合入后立刻需要一次破坏性签名改动，违反实现规划「后片不重写前片」的约束。

#### 3.5 幂等恢复：`ReconcileFanout(localNpub string) (FanoutReconcileReport, error)`

调用时机：TUI 启动、`groupchat send` / `groupchat retry` 开始前、daemon 每轮重试前。逐行处理 `state IN ('prepared','queued')`、仍有 outbox 条目的 `relay_accepted` 行，以及仍有 outbox 条目的 `failed` 行（**D4**）：

| SQLite 行 | outbox 中 `ID == event_id` 的群条目 | 动作 |
| --- | --- | --- |
| prepared | 恰 1 条 | T2 采纳该条目 QueueID（O1 已提交、T2 前崩溃） |
| prepared | 0 条 | O1 用 `event_json` 入队（同 event ID）→ T2 |
| queued | 恰 1 条且 QueueID 相同，`group_pending` | 无动作，交给重试循环 |
| queued | 恰 1 条，`group_failed` | T4：state→failed，issue `retry_exhausted`（O3 后、T4 前崩溃） |
| queued | 恰 1 条但 QueueID 不同 | T2' 采纳新 QueueID（另一进程的 `groupchat retry` 已重新入队） |
| queued | 0 条 | state→failed，issue `queue_missing`。不自动重新入队：条目缺失可能是用户执行了 `storage outbox clear`，要尊重用户意图；由 `groupchat retry` 显式恢复 |
| failed | 恰 1 条，`group_pending`（**D4**） | T2' 采纳该条目 QueueID：`failed→queued`（`groupchat retry` 已完成 `RequeueGroupOutboxEntry`、T2' 前崩溃，或另一进程的 retry 刚重排） |
| failed | 恰 1 条，`group_failed`（**D4**） | 无动作（耗尽后的遗留条目，等待用户 `groupchat retry`） |
| 任意 | ≥2 条 | 不发送，issue `queue_duplicate`，原样上报（`AttemptSend` 本就拒绝同 ID 多条） |
| relay_accepted | ≥1 条 | O2 删除条目，不发布（T3 后、O2 前崩溃） |
| 无对应行 | 有群条目（孤儿） | 不发布、不删除，上报 orphan。可能属于另一个 HOME 的数据库，删除不可逆 |

对账只读 outbox、只按上表写入；重复运行结果不变（幂等）。所有「入队」都先检查同 event ID 条目是否已存在，避免制造重复；手动重排一律经 **D6** 的 `RequeueGroupOutboxEntry`，不得「先删后加」两步完成。

**D4**（约束 S7）：
- 决定：对账表增加上面两行 `failed` 情形，并把扫描范围扩大到「仍有 outbox 条目的 `failed` 行」。`failed` + 0 条维持无动作（不在扫描范围内）。
- 理由：`groupchat retry` 先经 `RequeueGroupOutboxEntry` 写出新的 `group_pending` 条目（O1），再做 `failed→queued`（T2'）；两步之间崩溃或与 reconcile 并发时，原表没有 `failed` 行的对应动作：outbox 里已有 `group_pending` 条目、会被重试循环发布，而 SQLite 仍显示 `failed`，报告与事实相反。`failed` + 遗留 `group_failed` 未定义同理。
- 为什么不是另一个方案：「`failed` 行一律不扫描，靠下次 `groupchat retry` 再处理」会让已入队的 event 被发布而 SQLite 显示失败，状态与事实相反；「`failed` + `group_pending` 时删除该条目」会撤销用户刚发起的重试；「`failed` + `group_failed` 时自动重排」违反第 3.5 节「不自动重新入队、尊重用户意图」的原则。

### 为什么不是另一个方案

- **布尔「已入队」**（#138 现有 `MarkActivationQueued(..., queued bool)` 的形状）：调用方传 `true` 不需要任何证据；崩溃后无法回答「入的是哪个 event、队列里还在不在」。本方案要求 event ID + event_json + QueueID，并能与 outbox 逐条对账。
- **把群发送队列整体搬进 SQLite，不用 outbox**：会出现第二套重试循环、退避和 relay 发布逻辑，与 #143 刚接通的 TUI/daemon outbox 分叉；发布计划也明确复用现有 outbox。
- **把 outbox 搬进 SQLite 以获得单事务**：改动面覆盖所有 DM 发送、CLI outbox 命令和已有用户数据迁移，超出 M1；并且 relay 发布本来就不能放进事务，跨步对账仍然必要。
- **先入 outbox、后写 SQLite**：崩溃后 outbox 会有已签名 event 但没有本机历史和 logical ID 映射，重试方无法把 ACK 记到任何群消息上（只能写 DM，违反 I3）。先 T1 保证每个队列条目都有归属。
- **ACK 后先删队列、再写 SQLite**：在两步之间崩溃会丢失 ACK 证据，行停在 queued 且条目缺失，只能误判为 `queue_missing`。
- **群条目沿用 `pending` 状态 + `route` 字段**：旧二进制会把它当 DM 发送并写 DM 历史（见 3.2）。

### 验收

- 单元（故障注入，每个崩溃点一个用例）：在 T1 后、O1 后、T2 后、P 后、T3 后、O3 后分别中断进程，再运行 `ReconcileFanout` 和一次重试。断言：最终每个收件人恰好一个 outbox 条目或零条目；所有 event ID 等于 T1 写入的值；没有 `messages`（DM）表行；SQLite 状态符合 3.5 表格；重复运行 `ReconcileFanout` 不改变结果。
- 单元：`AttemptSendRouted` 处理群条目且 `handlers.Group == nil` 时，publisher 调用次数为 0、`StoreOutgoingMessage` 调用次数为 0、issue 为 `route_handler_missing`。
- 单元：T3 返回错误时 outbox 条目仍在；下一轮 `BeforePublish` 返回 skip=false，重发后 T3 成功再 O2。
- 单元：旧格式兼容：`GetPendingOutbox` 不返回 `group_pending` 条目；以当前 main 上不认 `route` 字段的 `GetPendingOutbox` + `attemptSend` 组合模拟旧二进制处理一份含群条目的 outbox，DM 表行数为 0、publisher 调用次数为 0。
- E2E：Alice 发送时在 publish 前 kill 进程（`SIGKILL`），重启 TUI 后 Bob、Carol 各收到一次，event ID 与 kill 前 SQLite 中记录的值一致。

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

## 5. ACK 语义

### 决定

relay ACK（NIP-01 `OK true`）只表示「某个 relay 接受存储了该 event」，不表示收件人取到，更不表示已读。M1 每收件人状态机：

```text
             T1                O1+T2               P ok + T3
   (none) ───────▶ prepared ───────────▶ queued ───────────────▶ relay_accepted (终态)
                      │                   │  ▲                       ▲
                      │ 对账：已有条目    │  │ P fail + O3/T4        │ 迟到 ACK（另一进程）
                      └───────▶ queued    │  └──(未耗尽)             │
                                          │                          │
                                          ▼ 耗尽 / queue_missing     │
                                        failed ──────────────────────┘
                                          │
                                          └── groupchat retry（同 event，新 QueueID）──▶ queued
```

- 状态等级：`prepared(0) < queued(1) = failed(1) < relay_accepted(2)`。SQLite 更新一律带 `WHERE` 条件，只允许升级；同级之间只允许 `queued→failed`（失败记账）和 `failed→queued`（显式 `groupchat retry`，或 **D4** 对账采纳已重排的条目）；`relay_accepted` 不可离开。
- `relay_accepted` 判定：一次尝试中至少 1 个目标 relay 返回 OK（与现有 `deliveryState` 的 `PublishedTo > 0` 一致）。按 **D2**，`relay_acks ∈ {0,1}` 只记录「是否至少一个接受」，`relay_count` 记录目标数，二者**不构成**比例。
- 不存在 `delivered` / `read` 状态：M1 协议没有回执 envelope，任何「已送达」的显示都没有证据。

### 为什么不是另一个方案

- **把 relay ACK 显示成已送达**：收件人可能多日不在线，relay 也可能过期删除；这正是发布计划明文禁止的表述。
- **M1 增加送达/已读回执 envelope**：每条消息会产生 N 份反向流量，并需要新的 envelope 类型与隐私选项；M1 范围（固定成员、明确同意、可靠重试）不需要它。本契约的决定是 M1 不做，状态机为它保留了在 `relay_accepted` 之后扩展的位置，不需要改已有状态。
- **要求所有目标 relay 都 ACK 才算 accepted**：单 relay 抖动就会让整体永远停在 queued，并且与 DM 现有判定不一致。

### 验收

- 单元：状态迁移表穷举测试：对 4×4 的（当前状态, 目标状态）组合，仅允许上面列出的迁移，其余返回错误且行不变。
- 单元：relay_accepted 之后再收到失败记账（另一进程的过期尝试）→ 行不变。
- 文案检查：`grep -rn -i 'delivered\|已送达\|已读' internal/groupchat internal/tui` 在群相关输出中无匹配（测试中以断言输出字符串实现）。

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

见第 3.3 节 T1–T4 / O1–O3。发送端本机历史只在 T1 写一次；重试、对账、迟到 ACK 都只修改 `groupchat_fanout`，不会触碰 `groupchat_messages`，所以本机不会重复显示。

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
| G1 | `storeMessage` / `StoreLocalMessage` 各自开启事务，无法与 `groupchat_fanout` 的 N 行插入组成 T1 原子事务 | T1 无法原子，可能出现「有本机历史无 fanout 行」 | #138 作者 | #138 合并前把 `storeMessage` 拆出 `storeMessageTx(tx *sql.Tx, ...)` 并保持外部 API 不变；否则 P0-E 在 #138 合并后的独立 PR 中完成该重构，且必须在 P0-E fanout 合并之前 |
| G2 | `MarkActivationQueued(localNpub, groupID, inviteID, eventID string, queued bool)` 以布尔表达「已入队」 | 与第 3 节「不得用布尔代替证据」冲突；调用方可在无 outbox 证据时激活成员 | #138 作者 | 去掉 `queued bool`；改为要求 `groupchat_fanout` 中 `(group_id, 'activate', invite_id, invitee)` 行存在、`event_id` 相同且 `state ∈ {queued, relay_accepted}`，在同一事务中校验。#138 合并前完成，或 P0-E 第一个提交完成且合并前通过评审 |
| G3 | `storeMessage` 对 `pending`/`activating`/`cancelled` 一律返回 `ErrGroupNotActive` | sink 无法区分暂态与终态（第 6 节） | P0-F 实现者 | 在 `groupchat` 新增 `ErrGroupActivationPending`（本机邀请 accepted 且群未激活时返回），`cancelled` 改返回 `ErrGroupCancelled`；P0-F 合并前附第 6 节暂态单元测试 |
| G4 | `ReceiveAcceptance` / `CancelGroup` 等返回 `[]Envelope`，但没有说明由谁加密/签名/入队 | 控制 envelope 可能绕过 durable intent 直接发布；状态已提交而 envelope 未持久化时无法找回 | P0-E 实现者（S10a、S10b） | （**D9**）**状态迁移与 fanout 行插入在同一事务**：S10a 为 `CreateGroup` / `AcceptInvite` / `DeclineInvite` / `ReceiveAcceptance` / `CancelGroup` 提供 Tx 变体（原公开方法变薄包装，行为不变），S10b 的 `*WithFanout` 在一个事务内调用 Tx 变体并经 `prepareFanoutTx(envelopeType, sendKey=invite_id, ...)` 插入 fanout 行；S10b 合并前附「控制 envelope 也有 fanout 行」与「fanout 插入失败时状态迁移一并回滚」的测试 |
| G5 | #145 尚未合并，`VerifyAgentMessage` 不在 main | 第 4 节 R1–R3 依赖它 | #145 作者 | #136→#138→#145 依次合并后 P0-F 才开工；若 #145 被拒，P0-F 必须在同等约束下（单次解密、opaque 值、前缀即 reserved）重新提交该边界，并先更新本文 |
| G6 | `groupchat_messages` 的 `UNIQUE(event_id)` 是全库唯一而不是按 `local_npub` 唯一 | 同一 HOME 多身份时没有实际冲突（单 `p` 保证 event 不同），无需修改；在此记录以免实现者误改 | P0-F 实现者 | 无需修改；P0-F 增加「同 HOME 两身份各收一份」测试，作为不需修改的证据 |

**D9**（约束 S10a、S10b）：
- 决定：G4 的关闭条件由「结果经 `PrepareFanout` 进入 T1」收紧为「**状态迁移与 fanout 行插入在同一 SQLite 事务**」，实现路径为 S10a 的 Tx 变体 + S10b 的 `*WithFanout`。
- 理由：原文没要求同一事务。`AcceptInvite` 可重入，但 `CreateGroup` 每次生成新 group ID、`CancelGroup` 要求 `StatePending`：若状态先提交、再在另一个事务里插 fanout 行，两步之间崩溃后再调用一次要么得到另一个群，要么因状态已变而报错，原 envelope 永远拿不回来，对方永远收不到邀请 / 取消。
- 为什么不是另一个方案：「先插 fanout 行、后迁移状态」在状态迁移失败时会留下已签名、可被重试循环发布的控制 envelope，对方收到一个本机并未发生的状态变化；「状态提交后靠对账补发 envelope」需要从状态反推 envelope 内容（含新 group ID、roster hash），等于第二套构造逻辑，违反 I4 的「只重发持久化的 event」。

## 8. 交付拆分（供 coordinator 派发）

- **P0-E**：`messaging.BuildAgentMessageEvent`（加密路径，D10）；`groupchat/fanout.go`（表、`PrepareFanout`（T1，D12）、T2–T4、`ReconcileFanout`（D4）、`FanoutReport`（D2））；`OutboxEntry.Route` + `group_pending/group_failed`（D5）+ `RequeueGroupOutboxEntry`（D6）；`AttemptSendRouted`（D11、D13）与三个重试方经 `SetGroupOutboxProvider` 接线（D7）；`hyphae groupchat {send,retry,status}`（D8）；第 1、2、3、5 节验收（E2E 按 D3）；关闭 G1、G2、G4（D9）。切片拆分与顺序见 [实现拆分方案](m1-group-fanout-impl-plan-20261008.md)。
- **P0-F**：R1/R2/R3 分流；`GroupInboundSink` 与 `groupchat/inbound.go`；暂态 re-walk；TUI 群模型的投递摘要展示；第 4、6 节验收；#145 合入后补第 1 节 `Route()==AgentRouteReservedGroup` 断言（D1）；关闭 G3、G6。
- 两者共同完成：真实 relay（`wss://relay.aastar.io`）+ 三个隔离 HOME（alice/bob/carol）的 E2E 脚本 `test_groupchat_fanout_e2e.sh`。现有 `test_group_e2e.sh` 不计入证据（见群聊设计基线）。
