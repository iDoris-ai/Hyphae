# 群聊接线与消息界面：Hyphae 提供的契约（给 Agent24）

日期：2026-10-25，2026-10-26 更新为接线版本状态（H-G0…H-G6 已合并，见第 5 节）。基线：Hyphae v0.26.3（`144555ca`）；接线版本见 `docs/agent/release-*-progress.md`。本文是 Hyphae 一侧的设计与约定接口；**不修改 Agent24 代码**。Agent24 按本文自行实现界面，有出入时以 Hyphae 的实际二进制输出为准，并在 Hyphae 仓库提 issue。

## 1. 边界

| 事项 | 谁做 |
| --- | --- |
| 群的创建、邀请、接受、激活、逐收件人加密发送、重试、状态、本地历史 | Hyphae（`hyphae groupchat …`） |
| 稳定的 `--json` 输出、错误码、退出码、状态语义 | Hyphae（本文第 3、4 节） |
| 群聊列表、消息流、发送框、逐人投递状态、重试按钮、邀请确认页 | Agent24（通过它自己的 `/api/v1/comm/*` 调 Hyphae CLI） |
| 终端里的群聊 TUI | Hyphae（可选，独立于 Agent24） |

## 2. 现状：什么能用，什么不能

**M1 的群聊目前只在「同一个 HOME / 同一个 keystore 里放着所有成员身份」时成立。** 这是本次核对时发现的，之前把它说成「三人群聊完成」不准确，应读作「同机三身份群聊完成」。

| 能力 | 状态 | 证据 |
| --- | --- | --- |
| 同一 keystore 内三身份：create → accept → 激活 → 发送 → 恰好一次历史 | 可用 | `tests/m1_group_invite_offline_partial_fanout_e2e_test.go`；`internal/groupchat/lifecycle_cli_test.go` |
| 逐收件人 NIP-44 加密、签名、独立 event id | 可用 | `groupchat send`；契约见 `m1-group-fanout-contract-20261008.md` |
| 单个收件人失败时部分成功、可单独重试 | 可用 | e2e 的 `HYPHAE_FAIL_GROUP_RECIPIENT` 场景；`groupchat retry/status` |
| 跨设备建群：邀请经 relay 送到另一台机器上的身份 | 可用（接线版本） | `create` 对本机没有私钥的被邀请人排队发送；daemon 注册发布器发布群 outbox 行；`tests/m1_group_invite_remote_e2e_test.go`（三 HOME + 真 relay） |
| 跨设备接受与激活 | 可用（接线版本） | `accept` 在创建者不在本机时排队发给创建者，返回 `pending`；创建者的 daemon 在最后一人接受后发出激活；同一 e2e 覆盖到消息送达与 `relay_accepted` |
| 群聊 TUI | **未实现** | `hyphae group chat` 是占位输出；`hyphae tui` 只有单聊 |
| 列出我所在的群 | 可用 | `groupchat list`、`groupchat members` |

结论：接线版本起，多设备群聊可用（每台设备只需自己的密钥，经 relay 与 daemon）；前提是该设备的 `hyphae daemon` 在运行，否则排队的群事件不会发出。群聊 TUI 仍未实现（H-G7，可选）。

## 3. 命令契约（当前二进制已有的部分）

通用：`--json` 或 `HYPHAE_OUTPUT=json`；成功 `{"ok":true,"data":…}` 写 stdout，失败 `{"ok":false,"error":"<code>","message":"…"}` 写 stderr；退出码 0/1/2/3/4/5 对应 成功/用户错误/网络错误/鉴权错误/其他错误/写冲突（见 `internal/common/output.go`）。需要口令时用 `--password-stdin`，不要用 `--password` 参数。身份由 `--identity <昵称>` 指定，缺省为默认身份。

| 命令 | 成功 `data` | 说明 |
| --- | --- | --- |
| `groupchat create --name N --identity A --invitee <npub>…` | `{"group_id":"<32 hex>"}` | 邀请此时只落在本地库 |
| `groupchat invite list --group G --identity A` | `{"invites":[{"invite_id","invitee_npub","group_id","state"}]}` | 创建者查看待接受邀请 |
| `groupchat accept --invite I --identity B` | `{"group_id":"…","state":"pending" \| "active"}` | 接受已记录即成功（退出码 0）。`pending` = 还在等其他成员或创建者的激活；`active` = 群已激活。创建者不在本机时，接受以加密事件排队发给创建者，需本机 daemon 运行 |
| `groupchat send --group-id G --body T [--logical-id L] [--max-retries N] --identity A` | 发送报告：`group_id, logical_id, created_at, state, accepted, queued, failed, recipients[]` | **部分失败时退出码 4、stdout 为空、stderr 为 `{"ok":false,"error":"other_error","message":"logical <id>: partial fanout queued X failed Y","data":<与成功相同的发送报告>}`，`data` 含逐人结果。** 仍建议调用方自己传 `--logical-id`，便于之后 `status`/`retry` |
| `groupchat status --group-id G --logical-id L --identity A` | 同上发送报告 | 只读 |
| `groupchat retry --group-id G --logical-id L --recipient <npub> --identity A` | 同上发送报告 | 为失败的收件人换新 queue id 重发，沿用原 event |
| `groupchat list --identity A` | `{"groups":[{"group_id","name","state","creator_npub","members":[npub],"updated_at"}]}` | 无群时为 `[]` |
| `groupchat members --group-id G --identity A` | `{"members":[{"npub","state","is_creator"}]}` | 创建者在前 |
| `groupchat history --group-id G [--limit 20] [--before-id ID \| --after-id ID] --identity A` | `{"messages":[{"id","event_id","group_id","sender","content","plaintext","created_at","is_encrypted"}]}` | 只含本地已落库消息，最旧在前。`--before-id`：比该消息更旧的最新 `limit` 条；`--after-id`：比该消息更新的最旧 `limit` 条（增量刷新）；同秒消息按 id 排序，不重不漏；未知游标或同时给两个 → 退出码 1。`id` 就是发送时的 `logical_id`，可直接用于 `status`；发送者自己的行 `event_id` 为空、`content` 为空，显示用 `plaintext` |

`recipients[]` 每项：`recipient_npub, event_id, queue_id, state, relay_acks, relay_count, attempts, max_retries`，另有 `issue`、`retry_queue_id`、`attempt_phase` 等诊断字段；界面只依赖前五个，其余可忽略。

## 4. 状态语义（界面不得越界）

`state` 取值：`prepared`、`queued`、`relay_accepted`、`failed`。

- `relay_accepted` = 至少一个 relay 接受了该收件人的那份事件。**不表示对方收到、解密、显示或已读。** 界面最多写「已发出」，不要写「已送达」「已读」。
- `queued` = 已可靠入队、等待（重试）发布；`failed` = 重试预算用尽，需要用户触发 `retry`。
- `partial=true` 表示有人成功有人失败：界面按收件人逐个展示，不要用一个总状态覆盖。
- 重试复用原签名事件，不会产生重复消息；界面不得自己重新调用 `send` 来「重发」。
- 消息正文来自网络，显示前必须清除终端/控制字符，并限制长度。

## 5. 交付状态

| 编号 | 内容 | 状态 |
| --- | --- | --- |
| H-G0 | 命令 JSON 字段集固定成测试 | 已合并：`internal/groupchat/contract_json_test.go`（字段增删改名会失败） |
| H-G1 | 跨设备邀请投递（含 daemon 发布器） | 已合并 #284 |
| H-G2 | 跨设备 accept / 激活，`accept` 返回 `pending` | 已合并 #285 |
| H-G3 | `groupchat list` / `members` | 已合并 #286 |
| H-G4 | `history` 游标、部分失败带 `data`、邀请 `state` | 已合并 #287（游标是消息 id：`--before-id/--after-id`，而不是原设想的时间戳，时间戳在同秒时做不到不重不漏） |
| H-G5 | 增量刷新 | 已由 `--after-id` 提供 |
| H-G6 | 三个独立 HOME + 真 relay 的端到端 | 已合并：`TestGroupAcrossThreeHomesThroughRelay`（建群→邀请→接受→激活→消息→`relay_accepted`）。离线成员 / 重启 / 部分失败重试仍由同机的 `TestM1GroupInviteOfflinePartialFanoutExactlyOnce` 覆盖，尚未在跨 HOME 版本里重做 |
| H-G7（可选） | Hyphae 群聊 TUI | 未做 |

已知缺口：创建者 daemon 在记下最后一个 accept 与排队激活之间崩溃时，群停在 `activating`，要等同一 accept 事件重放才会修复，没有定期修复；TUI 进程不注册发布器，只有 `hyphae daemon` 发布群事件。设计依据仍是 `m1-group-chat-design-20261007.md` 与 `m1-group-fanout-contract-20261008.md`。

## 6. 给 Agent24 的界面建议（可自行取舍）

1. 群聊列表用 `groupchat list`；邀请页用 `invite list`（按群）。
2. 消息流：用 `history` 拉取，按 `created_at` 排序；自己发的消息旁显示逐人状态，数据来自 `status --logical-id <该消息的 id>`。
3. 发送：先生成 `logical_id` 再调用 `send`；成功就渲染返回的逐人 `state`，退出码 4 且信息含 `partial fanout` 时改调 `status` 渲染；`failed` 的收件人给「重试」按钮，对应 `retry`。
4. 邀请确认页：展示群名、创建者 npub 指纹、成员列表，用户明确点击后才调用 `accept`；不要自动接受。
5. 所有错误按 `error` 码分类，不向用户暴露 stack 或本地路径。

## 7. 验收与证据

本文的事实部分对应：`go test ./...`、`-tags integration ./tests`、`internal/groupchat/lifecycle_cli_test.go` 与 e2e。第 5 节表中「已合并」的条目对应发布门禁中的测试；未列为已合并的（H-G7、跨 HOME 的离线/重启重做）不能对外声明为已完成。
