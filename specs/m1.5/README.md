# M1.5 Spec Pack — Relay 自部署 + 花名册 + 短期修复清单

> 独立目录，供 `/loop` 自动化开发循环逐条消费。每个任务是一个独立文件，一个任务 = 一个 PR。
> 上游依据：[`../../docs/protocol-v2.md`](../../docs/protocol-v2.md)（架构决策）、[`../../docs/milestones/roadmap-v2.md`](../../docs/milestones/roadmap-v2.md)（M1.5 章节）、[`../../docs/buzz-comparison-analysis.md`](../../docs/buzz-comparison-analysis.md)（Buzz 借鉴分析）、[`../../docs/TODO.md`](../../docs/TODO.md)（短期任务 + V1 自测发现的 bug）。
> 本目录把上述四份文档里属于 **M1.5 阶段** 的内容合并、拆解成可独立提交 PR 的最小任务单元。
> 创建日期：2026-07-24

## 怎么用

1. `/loop` 每轮从下面的任务表里挑 **第一个 status = ready 且 depends_on 全部 done** 的任务
2. 按 [`LOOP_PLAYBOOK.md`](LOOP_PLAYBOOK.md) 的流程执行（实现 → 自测 → 自我 review → Codex review → 开 PR → 等后台 bot review → merge 或修复重来）
3. 任务完成（PR 已 merge）后，把下面表格里对应行的 status 改成 `done`，提交这个改动（可以和下一个任务的第一个 commit 一起，也可以单独一个小 commit）
4. 循环直到全部 `done`

## 任务表（按建议执行顺序排列，序号即依赖顺序，非强制路由——只要 depends_on 满足，可以调整顺序）

| # | 任务 | 文件 | depends_on | 规模 | status |
|---|---|---|---|---|---|
| 1 | 修复强制 ANSI 颜色输出 | [`tasks/01-fix-forced-ansi-color.md`](tasks/01-fix-forced-ansi-color.md) | — | XS | done ([#13](https://github.com/iDoris-ai/agent-speaker/pull/13)) |
| 2 | CLI 全局 `--json` 输出模式 | [`tasks/02-json-output-mode.md`](tasks/02-json-output-mode.md) | 1 | M | done ([#14](https://github.com/iDoris-ai/agent-speaker/pull/14)) |
| 3 | 修复 `history conversation` 与 `agent msg` 联系人解析不一致 | [`tasks/03-history-contact-resolution-fix.md`](tasks/03-history-contact-resolution-fix.md) | — | S | done ([#15](https://github.com/iDoris-ai/agent-speaker/pull/15)) |
| 4 | 审计哈希链（`audit_log` 表） | [`tasks/04-audit-log-hash-chain.md`](tasks/04-audit-log-hash-chain.md) | — | M | done ([#16](https://github.com/iDoris-ai/agent-speaker/pull/16)) |
| 5 | 成员角色模型（Human/Agent） | [`tasks/05-member-role-model.md`](tasks/05-member-role-model.md) | — | M | done ([#18](https://github.com/iDoris-ai/agent-speaker/pull/18)) |
| 6 | Profile register 三模式 schema | [`tasks/06-profile-register-mode-schema.md`](tasks/06-profile-register-mode-schema.md) | — | M | done ([#19](https://github.com/iDoris-ai/agent-speaker/pull/19)) |
| 7 | Profile discover 过滤条件扩展 | [`tasks/07-profile-discover-filters.md`](tasks/07-profile-discover-filters.md) | 6 | S | done ([#20](https://github.com/iDoris-ai/agent-speaker/pull/20)) |
| 8 | daemon outbox 诊断/清理命令 | [`tasks/08-daemon-outbox-diagnostics.md`](tasks/08-daemon-outbox-diagnostics.md) | — | S | done ([#21](https://github.com/iDoris-ai/agent-speaker/pull/21)) |
| 9 | `internal/nostr` + `internal/daemon` 单元测试补齐 | [`tasks/09-nostr-daemon-test-coverage.md`](tasks/09-nostr-daemon-test-coverage.md) | — | M | done ([#22](https://github.com/iDoris-ai/agent-speaker/pull/22)) |
| 10 | relay 部署脚本加固（为切换 `relay-khatru` fork 做准备） | [`tasks/10-relay-deploy-hardening.md`](tasks/10-relay-deploy-hardening.md) | — | S | ready |

规模粗略换算：XS ≈ 1 次 loop 迭代 30 分钟内，S ≈ 1-2 小时，M ≈ 半天左右（对 /loop 来说就是"预计几轮自我修正"的量级，不代表真实人类工时）。

## 依赖图（文字版）

```
1 (ansi fix) ──> 2 (--json)
3 (contact resolution fix)          [独立]
4 (audit log)                        [独立]
5 (role model)                       [独立]
6 (register mode) ──> 7 (discover filters)
8 (outbox diagnostics)               [独立]
9 (test coverage)                    [独立]
10 (relay deploy hardening)          [独立]
```

7 个任务完全独立，可以任意顺序做甚至（如果 loop 支持并发分支）并行做；只有 `1→2`、`6→7` 是硬依赖。

## 跑 loop 过程中发现的、不属于当前任务范围的问题（记录以免丢失，后续单独排期）

- **`internal/group/db.go` 的 `generateGroupID`（`fmt.Sprintf("group_%s_%d", creator[:8], time.Now().UnixNano())`）在高并发下有极小概率生成重复 ID**，导致 `INSERT` 撞 `UNIQUE constraint failed: groups.id`。PR #18 review 时用同一 creator 连续发起 30 个并发 `CreateGroup` 调用复现（9 次里 2 次撞车），但这是 pre-existing 代码（PR #18 完全没碰这个函数），跟本次角色模型任务无关。真实场景下（独立 CLI 进程、有自然的进程启动 jitter）触发概率远低于测试用的紧凑并发场景。建议后续开一个小任务：换成带随机后缀/计数器或者 ULID 的 ID 生成方式。
- **`profile discover` 的 `--price-min`/`--price-max` 是 `cli.IntFlag`（只能传整数边界），跟 `--rating-min` 的 `cli.FloatFlag` 不一致**。PR #20 review 时发现：内部比较逻辑（`matchesPriceRange`）已经改成用 `float64` 精确比较（避免把 rate 价格截断成 int），但用户从 CLI 侧传不了小数边界（比如 `--price-max 99.99`）——不是正确性 bug，只是这次 fix 的动机（float 精度）跟 CLI 实际能表达的输入范围不匹配。非阻塞，建议后续把这两个 flag 也改成 `cli.FloatFlag`。
- **`internal/messaging/outbox.go` 的 `AddToOutbox` 直接用 `event.ID[:]`（32 字节原始二进制）当 `OutboxEntry.ID`，遇到两个问题**（任务 8 实现"outbox 诊断命令"时发现，本地真实 `outbox.json` 里的 13 条历史记录里有 9 条中招）：(a) 如果调用方在 `event.Sign()` 失败的情况下仍然把未签名事件塞进 outbox，`event.ID` 是全零字节，多条这样的记录会共享同一个 ID，导致 `UpdateOutboxStatus`/`IncrementOutboxRetry`（只对第一个匹配生效）永远更新不到"兄弟"记录，这些记录会永久卡在 `pending` 状态、`retry_count` 永远停在 0，`GetPendingOutbox` 一旦另一条同 ID 记录的 `retry_count` 超过 `max_retries` 就会把整组 ID 都排除在轮询之外。(b) 更深一层：把任意 32 字节原始内容当 Go string 传给 `json.Marshal`，遇到不合法 UTF-8 字节序列会被替换成 U+FFFD（`SaveOutbox` 第一次落盘时就发生，不可逆），所以磁盘上的 ID 字段本来就已经不是真实的原始哈希了。两者都不属于任务 8"加诊断命令"的范围（任务 8 明确不改变 outbox 存储格式），任务 8 的 `outbox list` 命令通过检测重复 ID 让这个问题变得可见（标 `⚠️dup`），但没有修复。建议后续开一个任务：(1) 修复 `event.Sign()` 失败时的错误处理，不要让未签名事件进 outbox——精确位置在 `internal/messaging/agent.go:154`（`event.Sign(senderSK)` 完全没检查返回的 error）；同一个文件的 `agent.go:207`（发送成功后调 `RemoveFromOutbox` 清理残留 outbox 记录，完全绕过 `AttemptSend`）是这个 bug 实际会造成数据丢失的确切触发点——任务 8 review 时把 `RemoveFromOutbox` 本身加固成"发现 ID 冲突就拒绝、什么都不删"，缓解了删错记录的风险，但 `agent.go:154` 忽略签名错误这个根因还没修。(2) 把 `OutboxEntry.ID` 的存储格式从原始 string 改成 hex 编码字符串（避免 UTF-8 替换问题，也让原始存储层面就是可读的）。
- **`internal/messaging.AgentKind`（`agent msg` 用的 Nostr kind）和 `internal/profile.ProfileKind`（agent profile 用的 kind）都是 `30078`**，两个完全不同的事件类型用了同一个 kind 数字。目前靠各自的 `d`/`c` tag 区分，任务 8 实现时没有观察到实际冲突，但这是个巧合而不是设计——值得以后有空时排查一下是否应该给其中一个换一个不同的 kind 号。
- **`scripts/deploy-relay.sh` 的 `local`/`tunnel` 模式当前对着真实的 khatru 上游完全跑不起来**（任务 10 加固 `check` 子命令时，按验收标准 4 的要求做回归验证时发现）：脚本硬编码 `EXAMPLE_DIR="$SRC_DIR/examples/basic"`（`scripts/deploy-relay.sh:167`），但 khatru 上游仓库现在的 `examples/` 目录下已经没有 `basic` 这个子目录了（实际是 `basic-badger`/`basic-sqlite3`/`basic-postgres`/`basic-elasticsearch` 等，说明上游把这个 example 拆分或改名了）——`git clone` 本身成功，但接下来 `cd "$EXAMPLE_DIR"` 前的目录存在性检查会直接 `die`。用 `git stash` 切回 `main`（PR #22 合并后的状态，任务 10 之前）复现了同样的失败，确认是 pre-existing、与本次任务 10 的改动无关，任务 10 的 diff 前后行为完全一致（没有引入新问题，也没有意外修复它）。任务 10 的 spec 明确只要求给 `check` 子命令做诊断加固，不要求改 `local`/`tunnel` 的构建逻辑，所以这里只记录、不在本任务修。建议后续开一个任务：把 `EXAMPLE_DIR` 的子路径也做成可配置（同 `RELAY_REPO`/`RELAY_REF` 的模式，比如 `RELAY_EXAMPLE_DIR` 环境变量），或者干脆等 `relay-khatru` fork 真正建好后直接把 fork 的仓库根目录当 `EXAMPLE_DIR`（fork 预期是独立仓库、没有 khatru 那种多 example 并存的目录结构，这个问题届时可能自然消失）。
- **`internal/messaging/outbox.go` 的 `SaveOutbox` 对并发写者不安全，有两种失败模式**（PR #21 review 时，仓库自己的 review bot 用 20 个并发 goroutine 各自 load→改一条记录→save 复现）：(a) **丢更新**——`SaveOutbox` 是"整个读→在内存改→整个写回"，两个并发调用者如果前后脚 `LoadOutbox`，后写的会拿着一份过时的快照覆盖掉前一个的改动，实测 20 个并发操作里经常有 18-19 个被静默撤销。(b) **文件损坏**——`SaveOutbox` 写的临时文件路径是固定的 `file + ".tmp"`，不是每次写各自独立的名字，两个并发写者的临时文件内容有小概率互相交叉写入，导致临时文件本身在 rename 之前就已经是损坏的 JSON（复现时遇到过一次 `invalid character '{' after top-level value`）。**这个 bug 本身是 pre-existing 的（`SaveOutbox` 的实现任务 8 完全没碰），但任务 8 加的 `storage outbox retry`/`clear` 命令是第一次让"daemon 的后台自动重试循环"和"用户手动跑的 CLI 命令"这两个独立进程有可能真的同时操作同一个 outbox.json**——在这之前只有 daemon 自己单线程的 60 秒循环会写这个文件，没有真正的并发写者。任务 8 的 spec 明确不允许改变 outbox 的存储格式/机制，加跨进程锁属于更大的架构改动，所以只记录、没有修。建议后续开一个任务：(1) 至少把 `SaveOutbox` 的临时文件名改成每次写唯一（`os.CreateTemp` 或者加 PID/随机后缀），这一步能消除"交叉写入导致文件损坏"这个失败模式，不改变磁盘上的 JSON 格式；(2) 给 outbox 的 load-modify-save 关键区加真正的互斥（比如基于 flock 的锁文件，或者把所有 outbox 写操作都路由到一个 daemon 持有的 channel/actor），才能同时解决"丢更新"这个问题——(1) 单独做不能解决 (2)。

## 明确排除在本 spec pack 之外（不是 loop 任务）

- **创建 `AuraAIHQ/relay-khatru` 仓库本身** —— 这是一次性的人类基础设施操作（建 GitHub 仓库、配置 CI/发布），不适合也不应该由自动化循环代劳。任务 10 只覆盖 agent-speaker 这边脚本的准备工作，不创建新仓库。
- **M2 及之后的任务** —— 依赖 `docs/protocol-v2.md` §10 里还没拍板的开放决策（D1-D5，向量算法、邀请券是否上链等），等 M1.5 跑完、这些决策定下来后再产出下一批 spec pack（建议放在 `specs/m2/`）。

## 里程碑完成定义（M1.5 Done）

- 上面 10 个任务全部 `done`（PR 已 merge 到 `main`）
- `go build ./... && go vet ./... && go test ./...` 全绿
- `docs/milestones/roadmap-v2.md` 里 M1.5 的状态从 🔄 改成 ✅，并补一段"实际完成情况 vs 计划"的简短说明（每个任务花了几轮 loop、有没有中途改变设计）
- `docs/TODO.md` 对应勾掉的项目打 `[x]`
