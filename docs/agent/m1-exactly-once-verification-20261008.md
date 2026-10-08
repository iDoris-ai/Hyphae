# M1 恰一次交付对抗性验证（2026-10-08）

分支：`codex/m1-exactly-once-verification-20261008`。原始代码基线：`f62ddd1`（#143/#135 已合入）。本机：Darwin ARM64，Go 1.27.1。

## 结果与修复

确认复现了 SQLite 历史先于 JSON outbox 提交所造成的孤立发送记录：子进程在真实 `writeOutbox` 完成临时文件 fsync/close、尚未执行 rename 时被 SIGKILL，原实现保留了新历史，但主 outbox 不含该事件，无法按原签名恢复发送。

修复先提交已有格式的签名队列，再持久化历史；历史失败时保留并检查真实磁盘条目，报告 queued + history_not_stored。TUI 在重开后重试前，用发送方私钥和收件人公钥恢复缺失的本地明文。CLI/daemon 共用的重试也在缺失历史时恢复正文，避免它先于 TUI 清队列后留下空明文。后台 ACK 的 requestID 为零时，现在也安排实际历史查询和界面刷新。

成功路径仍先持久化 outgoing history，后清除对应 QueueID 的队列条目；写历史或清队列失败都保留原签名的恢复/诊断语义。relay ACK 只代表 relay_accepted，不能推断对方已收、已显示或已读。

沿用现有稳定 `.lock` 文件、flock、UpdateOutbox 与原子 rename；没有更改 outbox.json 磁盘格式或 internal/groupchat。`renameOutbox` 仅将现有 os.Rename 调用作为子进程故障注入点，生产仍执行同一个 syscall。与 [#147](https://github.com/iDoris-ai/Hyphae/pull/147) 的 I4（冻结签名事件）、第 3 节 Outbox ↔ SQLite 的不共事务判断一致；本 PR 不实现该契约的 groupchat_fanout 表和群 send-intent。

## 五个方向

| 方向 | 结论 | 方法与守卫 |
| --- | --- | --- |
| 1. relay 重放 | 未复现重复入库/展示 | 两个真实 websocket 订阅各投递同一加密签名 event 128 次，随后发送已签名的坏压缩 sentinel。收到两次 sentinel 错误回调证明所有重放已消费；首轮只通知一次、只存一行、View 正文一次。关闭并重开 TUI 后再投递 256 次：零新通知，历史/View 仍一次。移除 first-arrival emit 守卫后定向断言失败，实际 256 次通知。 |
| 2. 重启竞态 | 复现并修复孤立历史；补齐提交后的恢复 | 子进程停在真实 fsync 后、rename 前，父进程读取 readiness pipe 后 SIGKILL。基线有孤立历史；修复后无幻影历史，原有 durable 队列完整。另在 rename+目录 fsync 后、history 前 SIGKILL，新签名条目仍可加载；TUI 重开从该磁盘状态恢复加密正文。实际 TUI worker 子进程在 relay 收到 event、ACK 未返回时被 SIGKILL，重开后重发原事件。 |
| 3. 并发写者 | 未复现丢更新/JSON 损坏；复现并修复并发恢复的空明文 | TUI worker 处于 ACK 屏障时，独立进程向同 outbox.json 提交另一身份的 event；放行 ACK 后只删除原 QueueID，另一条完整保留。另跑 6 个进程入队、24 个 stale-snapshot add/remove。审计没有生产调用 SaveOutbox，生产 RMW 均经 UpdateOutbox；SaveOutbox 定义为显式整文件替换，不能拿 stale replacement 当合并事务。额外定向测试让共享 AttemptSend 先于 TUI 处理“已入队但无历史”的加密条目，原行为存空明文并清队列；现已补回明文后才清队列。 |
| 4. CreatedAt/Since | main 未复现漏收；旧过滤器回归可以确定复现 | 新测试让历史 REQ 返回空，在 live REQ 才返回一天前签名的 event，按实际 filter.Matches 过滤。当前收到一次；恢复 Since=Now()-1 后收到零次。已有本地持久 relay 定向测试也通过，并验证旧 Since 查询确实找不到旧事件。 |
| 5. 同 event ID 重试 | 未复现重签/新 ID | SIGKILL + ACK 丢失重试，以及缺失历史恢复，均比较完整 nostr.Event（ID、CreatedAt、PubKey、Tags、Content、Sig），不只比较 ID；消息和队列数量也检查。注入重签后 ID 一致性断言立即失败。 |

各方向可复现的定向命令（下面的最终组合命令实际运行并覆盖全部列出的测试，exit 0）：

```sh
# 1. 重放；同时包含一天前事件的 live 过滤验证
go test ./internal/tui -run '^TestExactlyOnceRelayReplayAndLateEventAcrossTUIRestart$' -count=1
# 2. 真实持久化边界 SIGKILL、历史失败保留签名证据
go test ./internal/messaging -run '^TestExactlyOnceCrashDuringEnqueue$|^TestExactlyOnceHistoryFailureRetainsOriginalQueue$' -count=1
# 3. 独立进程写者；共享重试抢先恢复
go test ./internal/tui -run '^TestExactlyOnceTUIWorkerAndProcessWriterDoNotLoseUpdates$|^TestExactlyOnceCompetingRetryRestoresHistoryBeforeQueueRemoval$' -count=1
go test ./internal/messaging -run '^TestOutboxUpdatesAcrossProcesses$|^TestOutboxConcurrentStaleMutationsPreserveAddsAndRemovals$' -count=1
# 4. 实际本地持久 relay 的旧事件迟发布
go test ./internal/messaging -run '^TestWatchAgentInboxReceivesLatePublishedOldCreatedEvent$' -count=1
# 5. 子进程被 kill 后重试原签名；恢复正文并执行真正的后台 ACK 刷新命令
go test ./internal/tui -run '^TestExactlyOnceTUIKillAfterPublishRetriesOriginalEvent$|^TestExactlyOnceTUIRestoresHistoryFromCommittedEncryptedQueue$' -count=1
```

最终组合命令与实际输出：

```text
go test ./internal/messaging ./internal/tui -run ^TestExactlyOnce|^TestOutboxUpdatesAcrossProcesses$|^TestOutboxConcurrentStaleMutationsPreserveAddsAndRemovals$|^TestWatchAgentInboxReceivesLatePublishedOldCreatedEvent$ -count=1
ok  	github.com/iDoris-ai/hyphae/internal/messaging	2.147s
ok  	github.com/iDoris-ai/hyphae/internal/tui	1.622s
```

## 基线失败证据

在临时保存修复后的 agent.go 后，实际执行了 `git show f62ddd1:internal/messaging/agent.go` 替换该文件，运行下面测试，再逐字节恢复修复后的源码；测试因孤立历史断言失败（exit 1），不是编译或超时失败。

复现命令（在验证分支上执行；EXIT trap 保证失败后仍恢复文件）：

```sh
(
  task_copy="$(mktemp)"
  cp internal/messaging/agent.go "$task_copy"
  trap 'cp "$task_copy" internal/messaging/agent.go; rm "$task_copy"' EXIT
  git show f62ddd1:internal/messaging/agent.go > internal/messaging/agent.go
  go test ./internal/messaging -run '^TestExactlyOnceCrashDuringEnqueue$/before-commit' -count=1
)
```

实际失败输出摘录（省略临时路径、随机 ID 与完整合成历史对象）：

```text
--- FAIL: TestExactlyOnceCrashDuringEnqueue (0.05s)
    --- FAIL: TestExactlyOnceCrashDuringEnqueue/before-commit (0.05s)
            Error: Expected nil, but got: &types.StoredMessage{...}
            Messages: no outgoing history may precede the signed-event queue commit
FAIL
FAIL github.com/iDoris-ai/hyphae/internal/messaging 1.283s
FAIL
```

## 移除守卫的反证

每次独立修改一个守卫、运行对应定向测试、检查 exit 1 且存在 `--- FAIL:`，随后恢复原始文件。无固定 sleep，无测试重试，无降低数量或内容断言。

| 移除/破坏守卫 | 必须失败的定向断言（均实际失败） |
| --- | --- |
| 恢复 history-before-queue | SIGKILL 后历史对象应为 nil，实际存在 |
| 不执行 TUI 缺失历史恢复 | relay 收到事件时，history 应已持久化，实际为 nil |
| 不执行共享重试缺失明文恢复 | 外部重试成功清队列后，正文应等于原明文，实际为空 |
| 不执行后台 ACK 历史刷新 | 应同时安排实际历史查询和等待下一更新，实际只有等待 |
| 不按 first-arrival 抑制重复通知 | 首轮应通知 1 次，实际 256 次 |
| 恢复 CreatedAt-based Since | 应收到旧事件 1 次，实际 0 次 |
| 重试时重新签名 | relay 的 ID 与持久化原 ID 应一致，实际不同 |

原有断言的顺序修正：

1. `TestAgentMsgCmd_HistoryOrOutboxFailureNeverPublishes/invalid_outbox_JSON` 的 history_stored 从 true 改为 false：无可提交队列时不能制造 outgoing history；新增实际查库断言保证该 ID 不存在。原有 error、零发布与状态断言保留。
2. `TestSendQueuedAgentMessage_UncertainEnqueueNeverPublishes` 的 HistoryStored 从 true 改为 false：队列提交结果不确定时不继续写历史。原有 QueueStateUnknown=true、QueuedForRetry=false、publisher 未调用、rename 后磁盘确有原签名条目等断言全部保留。
3. history write failure 场景保持“不发布/历史失败”断言，并新增实际签名队列条目保留与坏数据库目录未变的断言。故障数据库不能被查询来证明无行。

## 完整 gate 实际输出

`go build ./... && go vet ./...`：exit 0，stdout/stderr 均为空。

`go test ./... -count=1`：exit 0。

```text
ok  	github.com/iDoris-ai/hyphae/cmd/hyphae	6.656s
?   	github.com/iDoris-ai/hyphae/cmd/hyphae-relay	[no test files]
ok  	github.com/iDoris-ai/hyphae/internal/audit	1.897s
ok  	github.com/iDoris-ai/hyphae/internal/common	2.298s
ok  	github.com/iDoris-ai/hyphae/internal/daemon	15.633s
ok  	github.com/iDoris-ai/hyphae/internal/group	3.605s
ok  	github.com/iDoris-ai/hyphae/internal/groupchat	3.045s
ok  	github.com/iDoris-ai/hyphae/internal/identity	14.098s
ok  	github.com/iDoris-ai/hyphae/internal/messaging	14.511s
ok  	github.com/iDoris-ai/hyphae/internal/nostr	4.130s
?   	github.com/iDoris-ai/hyphae/internal/notify	[no test files]
ok  	github.com/iDoris-ai/hyphae/internal/profile	11.841s
ok  	github.com/iDoris-ai/hyphae/internal/relay	4.220s
ok  	github.com/iDoris-ai/hyphae/internal/relayconfig	4.822s
ok  	github.com/iDoris-ai/hyphae/internal/relayquery	9.927s
ok  	github.com/iDoris-ai/hyphae/internal/storage	4.523s
ok  	github.com/iDoris-ai/hyphae/internal/tui	5.402s
ok  	github.com/iDoris-ai/hyphae/internal/wireevent	3.413s
ok  	github.com/iDoris-ai/hyphae/pkg/compress	3.370s
ok  	github.com/iDoris-ai/hyphae/pkg/crypto	3.295s
ok  	github.com/iDoris-ai/hyphae/pkg/types	2.428s
?   	github.com/iDoris-ai/hyphae/scripts	[no test files]
ok  	github.com/iDoris-ai/hyphae/tests/contracts	1.737s
```

`go test ./internal/messaging/... ./internal/tui/... -race -count=1`：exit 0。

```text
ok  	github.com/iDoris-ai/hyphae/internal/messaging	21.307s
ok  	github.com/iDoris-ai/hyphae/internal/tui	7.569s
```

`git diff --check`、本次修改 Go 文件 gofmt 检查均无输出。pre-PR checker 的精确 head/规则版本/规模摘要见 PR 描述。

## 证据边界

- 未提交的输入（签名 event 尚未完成 outbox 原子提交、界面也未报告 queued）不属于已 durable 接受的发送；rename 前故障保证旧 durable 队列完整、不会出现误导的历史。rename 后故障保证原签名事件仍可恢复。
- 已经只剩历史、原签名 event 完全丢失的旧孤立记录无法从当前 SQLite 字段重建原签名；本 PR 防止再产生该状态，不做旧记录重签迁移。
- 本次 SIGKILL/模型 View 验证在 Darwin ARM64 运行；websocket 攻击 relay 是测试工装，时间过滤的已有测试使用实际本地 relay。未声称本轮完成 Linux PTY 或三身份全发布验收；Linux/Darwin CI 与 clestons 最新 head review 由 PR 流程执行。
