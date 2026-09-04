# Task 08：daemon outbox 诊断/清理命令

> 规模：S · depends_on：无 · 发现来源：2026-07-24 真实自测（daemon 启动时报告"9 pending messages... 0 sent, 9 failed"，怀疑是历史遗留脏数据，缺一个排查手段）

## 目标

当前 outbox 只有"daemon 自动重试"这一种交互方式，出问题时用户完全没办法看清楚"到底卡了什么"。加一个只读诊断命令 + 一个显式清理命令。

## 接口

```
hyphae storage outbox list                    # 列出所有 outbox 条目，含状态/失败次数/年龄/目标 relay
hyphae storage outbox list --failed-only       # 只看失败的
hyphae storage outbox clear --failed           # 清掉所有失败次数超过阈值的条目（需要二次确认或 --yes）
hyphae storage outbox retry --id <id>          # 手动触发单条重试（不用等 daemon 的 60s 周期）
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

- **outbox 实际存储在 `~/.hyphae/outbox.json`（JSON 文件），不是 SQLite**——CLAUDE.md 架构表里"SQLite ... for messages and outbox"这句话是过时/不准确的（`messages.db` 里根本没有 outbox 表）。本任务没有改这个事实，只是确认现状；顺手校对了一下但没有修改 CLAUDE.md（不属于本任务范围）。
- **`retry --id` 复用逻辑，不是复制粘贴**：把 `internal/daemon/daemon.go` 里 `processOutbox` 循环体中"解析事件 → 尝试各 relay → 更新 outbox 状态"这一段（原来内联在 daemon 的重试循环里）提取成 `internal/messaging.AttemptSend(ctx, ob, entry, defaultRelays, dialTimeout) (SendResult, error)`，daemon 的自动重试循环和 CLI 的 `outbox retry` 命令都调用这一个函数。daemon 保留了自己的指数退避资格检查（`AttemptSend` 本身不做退避判断——手动 retry 的整个意义就是跳过等待，符合 spec 要求）。这个提取几乎是行为不变的重构：把原来直接写在 daemon.go 里的 `internal/daemon` 测试和 `internal/messaging` 全套测试跑一遍都全绿，没有引入新的行为差异。
- **`outbox` 命令放在 `internal/messaging` 而不是 `internal/storage`**：`internal/messaging` 已经 import 了 `internal/storage`（用于 SQLite 消息存储），如果反过来在 `internal/storage` 里 import `internal/messaging` 会产生 import cycle。解决办法：`OutboxCmd` 定义在 `internal/messaging`，在 `cmd/hyphae/main.go`（组合根，两个包都能 import）里用 `storage.StorageCmd.Commands = append(storage.StorageCmd.Commands, messaging.OutboxCmd)` 挂到 `storage` 命令组下面，最终用户看到的还是 `hyphae storage outbox list/clear/retry`，跟 spec 的接口设计完全一致。
- **没有加"失败原因"字段**：现有 `OutboxEntry` 没有记录失败原因（比如 relay 拒绝的具体错误），`AttemptSend`/daemon 原来的逻辑本来就把 relay 的 publish 错误直接丢弃、只用来判断成功/失败。按 spec 的指引（"如果改动现有 outbox 结构影响面较大，可以先只加 list/clear...把'记录失败原因'拆成一条新的 TODO"），这次没有加这个字段——加了会牵涉到 `OutboxEntry` 序列化格式变化和 daemon/CLI 两处调用点都要传递错误详情，超出 S 规模。记在下面 README 的"发现的问题"里当独立 TODO。
- **`clear --failed` 的判定标准是 `status == "failed" || retry_count >= min-failures`（默认 5）**，不是只看 `status` 字段——这是因为发现了一个更深的 pre-existing bug（见下面第一条），单纯看 `status=="failed"` 完全不可靠。
- **`list` 展示时把 ID 从原始字节 hex 编码**：`AddToOutbox` 存的是 `string(event.ID[:])`（32 字节原始二进制内容当 Go string 存），直接打印会在终端里显示成乱码控制字符。只在展示层做了 hex 编码（`hexOutboxID`），不改变实际存储的字段内容；`retry --id`/后续可能的 `clear` 按 ID 操作时，先尝试把传入值当 hex 解码，解码失败就当字面量字符串处理（兼容脚本直接传原始 ID 的场景）。ID 列特意没有截断（不像 RECIPIENT 列会截断）——截断后用户没法把它复制粘贴回 `retry --id`，之前踩过这个坑，修正后才定下最终版本。

### 发现的两个更深层 pre-existing bug（不属于本任务范围，记到 README 的"发现的问题"里）

1. **`event.Sign()` 静默失败时，`OutboxEntry.ID` 会是全零字节，导致多条记录 ID 冲突**：`internal/messaging/outbox.go` 的 `AddToOutbox` 直接用 `event.ID[:]` 作为条目 ID，如果调用方在签名失败的情况下仍然把未签名的事件塞进 outbox，`event.ID` 就是 32 个零字节，多条这样的记录会共享同一个 ID。`UpdateOutboxStatus`/`IncrementOutboxRetry` 只对第一个匹配的条目生效，其余"兄弟"记录永远不会被更新——这正是本地真实环境里那 9 条历史记录里的情况（1 条 retry_count 涨到了 145，另外 8 条永远停在 0）。本任务的 `list` 命令通过"同一个 ID 出现次数 > 1 就标 ⚠️dup"把这个问题变得可见，但没有修复它（修复需要改 `AddToOutbox`/`event.Sign()` 调用方的错误处理，超出本任务范围）。
2. **`OutboxEntry.ID` 存原始字节到 JSON 里会被 Go 的 `encoding/json` 悄悄替换成 U+FFFD**：Go 的 `json.Marshal` 对 string 类型的字段要求是合法 UTF-8，遇到不合法的字节序列（32 字节的哈希几乎肯定不是合法 UTF-8）会替换成 U+FFFD（3 字节 `EF BF BD`），这个替换在 `SaveOutbox` 第一次调用 `json.MarshalIndent` 的时候就发生了，是**不可逆**的信息丢失——磁盘上的 `outbox.json` 里那些 ID 字段自此以后就不再是原始的 32 字节哈希了。事件本身的 `id`/`sig` 字段没有这个问题（`EventJSON` 里存的是 `nostr.Event` 自己的 JSON 序列化，那些字段本来就是 hex 字符串）。这个 bug 比第 1 条更深、影响面更大（几乎所有 outbox 条目的 ID 字段在写盘那一刻就已经损坏），修复需要改 `OutboxEntry.ID` 的存储格式（比如从原始 string 改成 hex 编码），这明确属于"改变 outbox 存储格式"，不属于本任务范围（spec 原文写的是"这个任务不改变 outbox 的存储位置/格式"）。
- 顺带发现一个跟本任务无关的小细节：`internal/messaging/agent.go` 的 `AgentKind = 30078` 和 `internal/profile/profile.go` 的 `ProfileKind = 30078` 用了同一个 Nostr kind 数字。目前靠各自的 `d`/`c` tag 区分，没有观察到实际冲突，但值得以后留意。

## Live 验收（对应验收标准 4）

用编译好的二进制直接跑 `storage outbox list` 对着真实的 `~/.hyphae/outbox.json`（13 条真实历史记录）：
- 9 条共享同一个全零字节 ID 的记录全部被正确标成 `⚠️dup`；其中 1 条（retry_count=145，远超 max_retries=10）被正确识别成 `pending (stuck)`，另外 8 条冻结在 `0/10`——这正是上面第 1 条 bug 的真实表现，现在完全可见。
- `list --failed-only` 正确只挑出那 1 条真正"卡死"的记录（8 条冻结在 0 重试的记录因为 retry_count 没有超过阈值，不会被 `--min-failures` 判定为可清理——这是第 1 条 bug 的直接后果，已经在上面记录，不在本任务修）。
- 用 `echo "n" | storage outbox clear --failed` 验证了不传 `--yes` 时确认提示正常工作，且没有误删真实数据（`diff` 校验前后 `outbox.json` 完全一致）。
- 用隔离的临时 `HOME`（不碰真实数据）搭配本地 `scripts/minirelay.go` 完整验证了 `retry --id` 的成功路径（relay 恢复后重试成功、条目正确从 outbox 移除）和失败路径（relay 不可达时重试失败、retry_count 正确递增、不影响其他条目），以及 `clear --failed --yes` 正确清除、`--min-failures` 阈值正确生效。

### Codex review（Tier 1）第一轮

Codex 提了 1 个 High + 2 个 Medium，都已修复：

1. **High（已修复）——重复 ID 的记录，成功重试后会把"兄弟"记录一起删掉**：`AttemptSend` 成功路径调的是 `RemoveFromOutbox(ob, entry.ID)`，这个函数按 ID 过滤，会删掉**所有**匹配这个 ID 的记录，不只是被重试的那一条。原来 `retry --id` 只是打印一句"检测到 N 条共享这个 ID，只重试第一条"的警告然后继续——但如果这次重试恰好发的是第一条并且成功了，`RemoveFromOutbox` 会把其余几条**从未真正发送过的**记录也一起从 outbox 里删掉，属于静默数据丢失。修复：`retry --id` 检测到多条记录共享同一个 ID 时**直接拒绝执行**，不再"选第一条继续"，报错提示用 `storage outbox list` 去看清楚情况——这类"记录之间根本无法通过 ID 区分"的场景，没有安全的自动化处理方式，交给人工判断。加了 `TestOutboxRetryCmd_DuplicateIDRefusesToRetry` 覆盖。
2. **Medium（已修复）——`AttemptSend` 重构后，成功/失败路径都变成"一步出错就提前返回，跳过后续步骤"，而原来 `daemon.go` 内联的逻辑是三步独立执行、互不影响**：比如原来即使 `UpdateOutboxStatus` 报错，也照样会继续尝试 `RemoveFromOutbox` 和 `StoreOutgoingMessage`；重构后第一步出错就直接 return，后两步完全不会执行。同样的问题也存在于失败路径（`IncrementOutboxRetry` 出错就会跳过"是否该标记为 failed"的判断）。修复：`SendResult` 加了 `Attempted` 字段（区分"完全没走到 relay 拨号这一步"，比如事件解析失败，跟"已经尝试过发送/更新，只是某个记账步骤报错了"），三步全部改回独立执行、用 `errors.Join` 收集所有报错但不提前退出；调用方（`daemon.go` 的 `processOutbox`、`outbox retry` 命令）改成先看 `Attempted`/`Sent`/`MarkedFailed` 判断真实结果，`err` 只作为附加警告打印，不再当成"这次尝试失败了"的信号。
3. **Medium（已修复）——`retry --id` 传一个恰好是合法 hex 但其实是字面量字符串的 ID 时，永远匹配不到**：原来的逻辑是"hex 解码成功就只用解码结果去匹配，不会再退回去试字面量"，导致 `--json-file`/脚本场景下如果调用方传的是未经 hex 编码的原始字符串 ID（哪怕这个字符串碰巧是合法 hex），会被误当成 hex 输入、解码成一堆不相关的字节、永远匹配不到。修复：改成`findOutboxMatches`——先按 hex 解码后的字节去匹配，只有解码结果**一条都没匹配上**时，才退回去试字面量匹配。补了 `TestFindOutboxMatches_HexFirstThenLiteralFallback`。
4. **确认无误（不修）——diff 里带了任务 7 的 README 状态更新**：这是本分支的第一个 commit，按已经确定的"折进下一个任务分支的第一个 commit"约定（详见 `LOOP_PLAYBOOK.md`），不是范围蔓延。
5. **未修（记录，不属于本任务范围）——`outbox.json` 读-改-写没有跨进程锁**：`clear`/`retry` 和 daemon 的自动重试循环都是"读整个文件 → 改内存 → 原子 rename 写回"，如果两个进程真的同时操作会有"后写的覆盖先写的"这类丢更新风险。这是 outbox.json 这个存储设计从一开始就有的架构限制（`SaveOutbox` 的原子 rename 保证的是"不会读到写了一半的文件"，不保证"两个并发写者不会互相覆盖"），任务 8 的 spec 明确要求不改变存储格式/机制，加跨进程锁属于更大的架构改动，不在本任务范围内。

### Codex review（Tier 1）第二轮

第一轮的 High 修复（duplicate ID 拒绝重试）只覆盖了 `retry --id` 这一条手动命令路径，Codex 第二轮指出：**daemon 自己的自动重试循环（`processOutbox`）直接调 `messaging.AttemptSend`，完全没有这层保护**——如果 daemon 在正常的 60 秒自动重试周期里恰好处理到重复 ID 组里的第一条并且发送成功，`RemoveFromOutbox` 一样会把同 ID 的所有"兄弟"记录一起删掉（正是本地真实环境那 9 条记录会遇到的风险）。这是个正确、重要的发现——第一轮的修复位置选错了（挡在了调用方，而不是共享逻辑本身）。

**修复**：把 duplicate-ID 检查从 `outbox retry` 命令挪到 `AttemptSend` 函数内部最前面（`countByID` 检查，发现同 ID 记录数 > 1 就直接拒绝，`Attempted` 保持 `false`，等价于"还没走到拨号这一步就退出"）——这样 daemon 的自动循环和 CLI 的手动命令都统一走这一层保护，不需要在每个调用点各自维护一份检查逻辑。`outbox retry` 命令自己原有的重复 ID 检查保留（体验更好：在真正调用 `AttemptSend` 之前就能给出针对性的报错，而不是等内部拒绝），两层检查不冲突、职责不同：CLI 层挡的是"用户传的 --id 查找结果有歧义"，`AttemptSend` 层挡的是"即将处理的这个 entry 本身的 ID 在 ob.Entries 里跟别的记录冲突"（后者才是真正保护 daemon 自动循环的那一层）。加了 `TestAttemptSend_RefusesDuplicateID`（直接测 `AttemptSend`）和 `TestProcessOutbox_SkipsDuplicateIDEntriesWithoutMutating`（`internal/daemon` 包里新增，直接调 `processOutbox` 验证 daemon 的真实代码路径也被保护到了，不只是 messaging 包内部）。

（顺带一提：这一轮 review 期间，本地 `internal/messaging/outbox_test.go` 一度出现又消失了一个不属于本 PR 的并发测试——跟任务 4/5 阶段遇到的情况一样，是仓库自己的 review bot 在本地做独立验证时留下的临时文件改动，不是这个分支的内容，已确认工作区恢复干净后再继续。）

### Codex review（Tier 1）第三轮

确认第二轮的修复位置正确（`AttemptSend` 的 `countByID` 检查确实在最开头、任何拨号/mutation 之前，daemon 的 `processOutbox` 正确处理 `!result.Attempted` 分支），但发现 2 个新问题：

1. **Medium（已修复）——`internal/messaging/agent.go` 的 `agent msg` 正常发送成功路径，绕过 `AttemptSend` 直接调 `RemoveFromOutbox`，同样会被 duplicate ID 坑**：`agent.go:207` 在消息发送成功后调用 `RemoveFromOutbox(ob, string(event.ID[:]))`，用来清理"这条消息之前可能因为失败进过 outbox"的残留记录——这条调用完全没走 `AttemptSend`，所以第二轮加的 `countByID` 保护对它没用。更麻烦的是 `agent.go:154` 的 `event.Sign(senderSK)` 完全没检查返回的 error——如果签名失败，`event.ID` 就是全零字节（正是本地真实环境那 9 条记录的成因），这次发送成功后会把 outbox 里所有全零 ID 的记录（可能是别的、完全无关、从未真正发送成功的消息）一起删掉。**没有去改 `agent.go`（那是 `agent msg` 命令的发送逻辑，修复"该不该检查/怎么处理签名失败"是一个需要单独设计的问题，不属于"给 outbox 加诊断命令"这个任务），而是把防护做在更底层、更集中的地方**：把 `RemoveFromOutbox` 本身加固——发现要删除的 ID 在 `ob.Entries` 里匹配了不止一条，直接拒绝、什么都不删，而不是像原来那样把所有匹配的记录一起删掉。这样无论未来谁调 `RemoveFromOutbox`（`AttemptSend`、`agent.go`，或者还没写的代码），这条"绝不会因为 ID 冲突而误删无关记录"的保证都成立，不需要在每个调用点各自补一份检查。`AttemptSend` 自己的 `countByID` 检查保留（避免重复的场景走到更后面才被拒绝，效率更好、语义也更早更清晰），`RemoveFromOutbox` 的这层是最后一道保险。加了 `TestRemoveFromOutbox_RefusesDuplicateID`，故意先 revert 掉这个修复验证测试真的会失败，再验证修复后通过。
2. **Low（已修复）——`daemon_test.go` 里第二轮加的 `TestProcessOutbox_SkipsDuplicateIDEntriesWithoutMutating` 测试不够严谨**：原来的写法用的是空 `EventJSON`，就算没有 duplicate-ID 检查，`AttemptSend` 也会先在"解析事件"这一步就失败退出（这是早于本任务就有的既有行为），所以这个测试就算没有新加的保护也会"碰巧通过"，没有真正验证到新加的逻辑。改成用合法、可解析的 event JSON，并且改成断言 `processOutbox` 打印出来的内容里包含"share this ID"这句话（只有新加的 duplicate-ID 检查才会产生这句话）——用同样的"先 revert 验证测试会失败"的方法确认了这个版本才是真正有效的回归测试。
