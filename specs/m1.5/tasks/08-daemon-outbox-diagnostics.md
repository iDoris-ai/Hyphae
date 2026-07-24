# Task 08：daemon outbox 诊断/清理命令

> 规模：S · depends_on：无 · 发现来源：2026-07-24 真实自测（daemon 启动时报告"9 pending messages... 0 sent, 9 failed"，怀疑是历史遗留脏数据，缺一个排查手段）

## 目标

当前 outbox 只有"daemon 自动重试"这一种交互方式，出问题时用户完全没办法看清楚"到底卡了什么"。加一个只读诊断命令 + 一个显式清理命令。

## 接口

```
agent-speaker storage outbox list                    # 列出所有 outbox 条目，含状态/失败次数/年龄/目标 relay
agent-speaker storage outbox list --failed-only       # 只看失败的
agent-speaker storage outbox clear --failed           # 清掉所有失败次数超过阈值的条目（需要二次确认或 --yes）
agent-speaker storage outbox retry --id <id>          # 手动触发单条重试（不用等 daemon 的 60s 周期）
```

## 设计

读现有 `internal/messaging` 里 outbox 的存储结构（`outbox.json` 或已经在 SQLite 化——先确认现状，这个任务不改变 outbox 的存储位置/格式，只加读取和清理的命令）。`list` 命令直接读现有结构展示；`clear --failed` 需要一个"失败次数超过 N 次才算可清理"的阈值（默认给个合理值比如 5，可以加 `--min-failures` 覆盖）；`retry --id` 复用 daemon 内部现有的单条重试逻辑（如果现在是私有函数，酌情导出或重构成可复用的形式，不要复制粘贴一份重试逻辑出来）。

## 数据

不新增存储结构，复用现有 outbox 数据；如果现有 outbox 条目里没有记录"失败原因"字段，评估要不要顺手加一个（能帮助诊断，但如果改动现有 outbox 结构影响面较大，可以先只加 list/clear，把"记录失败原因"拆成一条新的 TODO，不要在这个 S 规模任务里顺带做大改动）。

## 流程

`list`/`retry` 是只读或幂等操作，风险低。`clear --failed` 是破坏性操作（删除数据），必须有确认步骤——没传 `--yes` 时先打印将要删除的条目数量和一个 `y/N` 交互确认，不能无提示直接删。

## 验收标准

1. 用测试 fixture（手工构造几条不同状态的 outbox 条目）验证 `list`/`list --failed-only` 输出正确
2. `clear --failed` 在没有 `--yes` 时会等待确认，不会静默删除；`--yes` 时正确清除，且不影响未超过失败阈值的条目
3. `retry --id` 正确触发单条重试，不影响其他条目
4. 用这个命令实际跑一遍本地环境里那 9 条历史失败记录，确认能看清楚它们的状态（这一步是本任务的"真实验收"，不只是跑通单元测试）
5. `go test ./...` 全绿

## 实现笔记

- **outbox 实际存储在 `~/.agent-speaker/outbox.json`（JSON 文件），不是 SQLite**——CLAUDE.md 架构表里"SQLite ... for messages and outbox"这句话是过时/不准确的（`messages.db` 里根本没有 outbox 表）。本任务没有改这个事实，只是确认现状；顺手校对了一下但没有修改 CLAUDE.md（不属于本任务范围）。
- **`retry --id` 复用逻辑，不是复制粘贴**：把 `internal/daemon/daemon.go` 里 `processOutbox` 循环体中"解析事件 → 尝试各 relay → 更新 outbox 状态"这一段（原来内联在 daemon 的重试循环里）提取成 `internal/messaging.AttemptSend(ctx, ob, entry, defaultRelays, dialTimeout) (SendResult, error)`，daemon 的自动重试循环和 CLI 的 `outbox retry` 命令都调用这一个函数。daemon 保留了自己的指数退避资格检查（`AttemptSend` 本身不做退避判断——手动 retry 的整个意义就是跳过等待，符合 spec 要求）。这个提取几乎是行为不变的重构：把原来直接写在 daemon.go 里的 `internal/daemon` 测试和 `internal/messaging` 全套测试跑一遍都全绿，没有引入新的行为差异。
- **`outbox` 命令放在 `internal/messaging` 而不是 `internal/storage`**：`internal/messaging` 已经 import 了 `internal/storage`（用于 SQLite 消息存储），如果反过来在 `internal/storage` 里 import `internal/messaging` 会产生 import cycle。解决办法：`OutboxCmd` 定义在 `internal/messaging`，在 `cmd/agent-speaker/main.go`（组合根，两个包都能 import）里用 `storage.StorageCmd.Commands = append(storage.StorageCmd.Commands, messaging.OutboxCmd)` 挂到 `storage` 命令组下面，最终用户看到的还是 `agent-speaker storage outbox list/clear/retry`，跟 spec 的接口设计完全一致。
- **没有加"失败原因"字段**：现有 `OutboxEntry` 没有记录失败原因（比如 relay 拒绝的具体错误），`AttemptSend`/daemon 原来的逻辑本来就把 relay 的 publish 错误直接丢弃、只用来判断成功/失败。按 spec 的指引（"如果改动现有 outbox 结构影响面较大，可以先只加 list/clear...把'记录失败原因'拆成一条新的 TODO"），这次没有加这个字段——加了会牵涉到 `OutboxEntry` 序列化格式变化和 daemon/CLI 两处调用点都要传递错误详情，超出 S 规模。记在下面 README 的"发现的问题"里当独立 TODO。
- **`clear --failed` 的判定标准是 `status == "failed" || retry_count >= min-failures`（默认 5）**，不是只看 `status` 字段——这是因为发现了一个更深的 pre-existing bug（见下面第一条），单纯看 `status=="failed"` 完全不可靠。
- **`list` 展示时把 ID 从原始字节 hex 编码**：`AddToOutbox` 存的是 `string(event.ID[:])`（32 字节原始二进制内容当 Go string 存），直接打印会在终端里显示成乱码控制字符。只在展示层做了 hex 编码（`hexOutboxID`），不改变实际存储的字段内容；`retry --id`/后续可能的 `clear` 按 ID 操作时，先尝试把传入值当 hex 解码，解码失败就当字面量字符串处理（兼容脚本直接传原始 ID 的场景）。ID 列特意没有截断（不像 RECIPIENT 列会截断）——截断后用户没法把它复制粘贴回 `retry --id`，之前踩过这个坑，修正后才定下最终版本。

### 发现的两个更深层 pre-existing bug（不属于本任务范围，记到 README 的"发现的问题"里）

1. **`event.Sign()` 静默失败时，`OutboxEntry.ID` 会是全零字节，导致多条记录 ID 冲突**：`internal/messaging/outbox.go` 的 `AddToOutbox` 直接用 `event.ID[:]` 作为条目 ID，如果调用方在签名失败的情况下仍然把未签名的事件塞进 outbox，`event.ID` 就是 32 个零字节，多条这样的记录会共享同一个 ID。`UpdateOutboxStatus`/`IncrementOutboxRetry` 只对第一个匹配的条目生效，其余"兄弟"记录永远不会被更新——这正是本地真实环境里那 9 条历史记录里的情况（1 条 retry_count 涨到了 145，另外 8 条永远停在 0）。本任务的 `list` 命令通过"同一个 ID 出现次数 > 1 就标 ⚠️dup"把这个问题变得可见，但没有修复它（修复需要改 `AddToOutbox`/`event.Sign()` 调用方的错误处理，超出本任务范围）。
2. **`OutboxEntry.ID` 存原始字节到 JSON 里会被 Go 的 `encoding/json` 悄悄替换成 U+FFFD**：Go 的 `json.Marshal` 对 string 类型的字段要求是合法 UTF-8，遇到不合法的字节序列（32 字节的哈希几乎肯定不是合法 UTF-8）会替换成 U+FFFD（3 字节 `EF BF BD`），这个替换在 `SaveOutbox` 第一次调用 `json.MarshalIndent` 的时候就发生了，是**不可逆**的信息丢失——磁盘上的 `outbox.json` 里那些 ID 字段自此以后就不再是原始的 32 字节哈希了。事件本身的 `id`/`sig` 字段没有这个问题（`EventJSON` 里存的是 `nostr.Event` 自己的 JSON 序列化，那些字段本来就是 hex 字符串）。这个 bug 比第 1 条更深、影响面更大（几乎所有 outbox 条目的 ID 字段在写盘那一刻就已经损坏），修复需要改 `OutboxEntry.ID` 的存储格式（比如从原始 string 改成 hex 编码），这明确属于"改变 outbox 存储格式"，不属于本任务范围（spec 原文写的是"这个任务不改变 outbox 的存储位置/格式"）。
- 顺带发现一个跟本任务无关的小细节：`internal/messaging/agent.go` 的 `AgentKind = 30078` 和 `internal/profile/profile.go` 的 `ProfileKind = 30078` 用了同一个 Nostr kind 数字。目前靠各自的 `d`/`c` tag 区分，没有观察到实际冲突，但值得以后留意。

## Live 验收（对应验收标准 4）

用编译好的二进制直接跑 `storage outbox list` 对着真实的 `~/.agent-speaker/outbox.json`（13 条真实历史记录）：
- 9 条共享同一个全零字节 ID 的记录全部被正确标成 `⚠️dup`；其中 1 条（retry_count=145，远超 max_retries=10）被正确识别成 `pending (stuck)`，另外 8 条冻结在 `0/10`——这正是上面第 1 条 bug 的真实表现，现在完全可见。
- `list --failed-only` 正确只挑出那 1 条真正"卡死"的记录（8 条冻结在 0 重试的记录因为 retry_count 没有超过阈值，不会被 `--min-failures` 判定为可清理——这是第 1 条 bug 的直接后果，已经在上面记录，不在本任务修）。
- 用 `echo "n" | storage outbox clear --failed` 验证了不传 `--yes` 时确认提示正常工作，且没有误删真实数据（`diff` 校验前后 `outbox.json` 完全一致）。
- 用隔离的临时 `HOME`（不碰真实数据）搭配本地 `scripts/minirelay.go` 完整验证了 `retry --id` 的成功路径（relay 恢复后重试成功、条目正确从 outbox 移除）和失败路径（relay 不可达时重试失败、retry_count 正确递增、不影响其他条目），以及 `clear --failed --yes` 正确清除、`--min-failures` 阈值正确生效。
