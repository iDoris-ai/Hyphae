# M1 F2e — outbox typed writer inventory 与迁移批次

- 日期：2026-10-11（UTC+7）
- 基线：堆叠分支 `m1-f2e1-typed-writer-guard`（main `8b0b4d3` + #178/#179/#180 合并 + F2d-2a/b/c）。行号以该分支为准。
- 依据：[schema 补充](m1-outbox-result-witness-schema.md) §5、§7 F2e 行；[rev7 契约](m1-group-fanout-contract-20261008.md) §3.5/§3.6.1。
- 状态：**盘点 + F2e-1 守卫**。不是 F2e 完成，不关闭 V6/V7。

## 1. 为什么 typed writer 必须先挡住

全部 typed 写入最终走两个入口：`UpdateOutbox`（`internal/messaging/outbox.go:90`）与 `SaveOutbox`（`outbox.go:74`）。两者用 `readOutbox` 的宽松 `json.Unmarshal` 读入 `types.Outbox`，再用 `writeOutbox` 的 `json.MarshalIndent` 整文件重写。`types.OutboxEntry`（`pkg/types/types.go:39`）只有 11 个 legacy 成员，因此：

- `fanout_protocol` / `failure_result` / `recovery_witness` 在任何一次 typed 写入后**静默消失**，包括与该群条目无关的 DM 入队、cleanup、clear。
- 未授权字段的原始片段（`relays:null` 周围空白、`event_json` 外层转义形式）被重新序列化，违反 §5「未改 entry 原始对象 bytes 不变」。

F2e-1（本批）在两个入口的锁内加 `requireTypedOutboxRoundTrip`（`outbox_typed_guard.go`）：文件只要含 legacy 11 成员之外的任何成员（字段名先解码，`fanout_protocol` 同样命中）或 `entries` 之外的顶层成员，就返回 `ErrOutboxTypedWriteHeld`，**零写、不刷新 mtime**。`UpdateOutbox` 连 no-op 事务也拒绝，因为所有 typed 发送路径都先经 `currentOutboxAttempt` 的 `UpdateOutbox`，这样旧路径不会在看不到协议记录的情况下 publish。只读 `LoadOutbox` 不受影响（`outbox list` 仍可查看）。

当前 main 没有任何生产路径写协议记录（F2d-2 原语均 inert），所以守卫今天不改变生产行为；它是 F3 接线 O3/D6 之前必须存在的绊线。

## 2. Typed writer 清单（全部经 `UpdateOutbox` / `SaveOutbox`）

| # | 调用点 | 入口 | 触发方 | 迁移批次 |
| --- | --- | --- | --- | --- |
| W1 | `enqueueOutboxEntry` `outbox.go:188`（经 `AddToOutbox` `outbox.go:162`） | UpdateOutbox | DM 发送 `sendQueuedAgentMessage` `agent.go:359`；`SendQueuedAgentMessage` 来自 TUI `tui/offline_outbox.go:243`、daemon 自动回复 `daemon/daemon.go:867` | F2e-2 |
| W2 | `UpdateOutboxStatus` `outbox.go:219` | UpdateOutbox | 公开 API（仅测试引用） | F2e-2 |
| W3 | `incrementOutboxRetry` `outbox.go:241`（`IncrementOutboxRetry`） | UpdateOutbox | 公开 API | F2e-2 |
| W4 | `RemoveFromOutbox` `outbox.go:267` | UpdateOutbox | 公开 API | F2e-2 |
| W5 | `currentOutboxAttempt` `outbox.go:674`（补 QueueID） | UpdateOutbox | 每次 `AttemptSend*`：daemon `daemon.go:292`、TUI `offline_outbox.go:139`、`outbox retry` `outbox_commands.go:346` | F2e-3（与 F3 R/S fence 同批） |
| W6 | `attemptSendWithKeyStore` `outbox.go:535`（DM 成功标 sent/移除） | UpdateOutbox | DM 发送 | F2e-2 |
| W7 | `recordAttemptFailure` `outbox.go:731`（DM 与群失败计数） | UpdateOutbox | DM/群发送失败；群经 `recordGroupAttemptFailure` | **群路径归 F3 O3**；DM 归 F2e-2 |
| W8 | `removeGroupOutboxEntry` `outbox.go:443` | UpdateOutbox | 群 ACK 后移除 | **F3 O2** |
| W9 | `enqueueGroupOutboxEntry` `outbox_group.go:75`（Enqueue/Requeue） | UpdateOutbox | `groupchat.(*Store).queueTargets` `groupchat/fanout_queue.go:34` | **Requeue 归 F4 D6**；首次 enqueue 归 F2e-3 |
| W10 | `CleanupOutbox` `outbox.go:823` | UpdateOutbox | daemon 周期清理 `daemon.go:698` | F2e-2（须保留群 failed 证据，§5） |
| W11 | `outbox clear` 补 QueueID `outbox_commands.go:185` | UpdateOutbox | CLI `storage outbox clear` | F2e-2 |
| W12 | `clearConfirmedOutboxEntries` `outbox_commands.go:265` | UpdateOutbox | CLI `storage outbox clear` | F2e-2 |
| W13 | `SaveOutbox` `outbox.go:74` | SaveOutbox | 仅测试与显式整文件替换 | 保持守卫，不迁移 |

Raw（metadata-aware）writer 只有 `commitRawOutboxEdit`（`outbox_raw_scan.go`），目前调用方仅 `commitOutboxO3` / `commitOutboxD6`，均无生产 caller。

## 3. 迁移批次（每批生产 Go ≤250 行，逐批列剩余 caller）

| 批 | 范围 | 前置 | 必需验收 |
| --- | --- | --- | --- |
| F2e-1（本 PR） | 两入口守卫 + 本清单 | F2d-2a/b/c | 每个 W1–W13 入口在含协议记录时零写、零 publish；legacy-only 文件行为不变（全量 `go test ./... -race`、`-tags integration ./tests`） |
| F2e-2 | DM 路径 W1/W2/W3/W4/W6/W7(DM)/W10/W11/W12 改为 raw 片段补丁：只改目标 DM 条目的授权字段，其余条目原始 bytes 不变 | F2e-1 + F3 持锁消费核对接口（§5：未消费凭据持续挡住通用 DM/clear/cleanup 写入；JSON 层无法自证已消费） | DM default relays/null 回归；含群协议记录时 DM 入队/清理成功且群条目 bytes 与 receipt digest 不变；clear/cleanup 不删群 failed 证据 |
| F2e-3 | W5 与首次群 enqueue（W9 非 requeue） | F3 R/S fence 接口评审 | 持锁只读核对 token/phase；未消费凭据阻止新 R/S；QueueID 补齐不改其他字节 |
| F3 / F4 | W7(群)→O3、W8→O2、W9(requeue)→D6 | 完整 F2 + F3/F4 契约 | V2–V5、V7 原计划；本清单不替代 |

## 4. 未覆盖 / 已知边界

- 守卫只看成员名，不验证 legacy 值是否合法；坏 legacy 值仍由 `readOutbox` 原有行为处理（与本 PR 前一致）。
- 守卫生效期间，文件一旦含协议记录，DM 发送、daemon 重试与 cleanup 会报 `ErrOutboxTypedWriteHeld`。这是 F2e-2 之前的有意 fail-closed；在 F2e-2 合入前**不得**接线任何写协议记录的生产路径。
- 旧二进制（不含守卫）仍会抹掉记录；部署排除属于 rev7 §3.6.1 / V7，本 PR 不涉及。
