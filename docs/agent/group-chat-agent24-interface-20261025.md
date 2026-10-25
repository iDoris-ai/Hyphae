# 群聊接线与消息界面：Hyphae 提供的契约（给 Agent24）

日期：2026-10-25。基线：Hyphae v0.26.3（`144555ca`）。本文是 Hyphae 一侧的设计与约定接口；**不修改 Agent24 代码**。Agent24 按本文自行实现界面，有出入时以 Hyphae 的实际二进制输出为准，并在 Hyphae 仓库提 issue。

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
| 跨设备建群：邀请经 relay 送到另一台机器上的身份 | **未接线** | `groupchat create` 把邀请「交给同机被邀请人」；`PrepareControlFanout` 只存在于库内，CLI 不调用 |
| 跨设备接受与激活 | **未接线** | `accept` 需要创建者私钥在本地 keystore（`secretForNpub`），`deliverActivations` 要求被邀请人私钥也在本地 |
| 群聊 TUI | **未实现** | `hyphae group chat` 是占位输出；`hyphae tui` 只有单聊 |
| 列出我所在的群 | **没有 CLI** | `Store.ListGroups` 存在，但没有对应的 `groupchat list` 命令 |

结论：Agent24 现在就能做的是「单机多身份」的界面验证；真正的多设备群聊要等 Hyphae 先交付第 5 节的 H-G1/H-G2。

## 3. 命令契约（当前二进制已有的部分）

通用：`--json` 或 `HYPHAE_OUTPUT=json`；成功 `{"ok":true,"data":…}` 写 stdout，失败 `{"ok":false,"error":"<code>","message":"…"}` 写 stderr；退出码 0/1/2/3/4/5 对应 成功/用户错误/网络错误/鉴权错误/其他错误/写冲突（见 `internal/common/output.go`）。需要口令时用 `--password-stdin`，不要用 `--password` 参数。身份由 `--identity <昵称>` 指定，缺省为默认身份。

| 命令 | 成功 `data` | 说明 |
| --- | --- | --- |
| `groupchat create --name N --identity A --invitee <npub>…` | `{"group_id":"<32 hex>"}` | 邀请此时只落在本地库 |
| `groupchat invite list --group G --identity A` | `{"invites":[{"invite_id","invitee_npub","group_id"}]}` | 创建者查看待接受邀请 |
| `groupchat accept --invite I --identity B` | `{"group_id":"…"}` | **仅当群已激活（全员接受）时成功**；其他成员还没接受时退出码 4、`other_error` / `group is not active`。接受本身已记录：最后一位接受后群即激活，先接受的人无需重来（2026-10-25 实测）。`invite list` 没有状态字段，界面无法区分「已接受」和「未接受」（见 H-G2） |
| `groupchat send --group-id G --body T [--logical-id L] [--max-retries N] --identity A` | 发送报告：`group_id, logical_id, created_at, state, accepted, queued, failed, recipients[]` | **部分失败时退出码 4、stdout 为空、stderr 只有一行 `{"ok":false,"error":"other_error","message":"logical <id>: partial fanout queued X failed Y"}`，不带逐人数据。** 调用方必须自己传 `--logical-id`，失败后用它调 `status` 取逐人结果 |
| `groupchat status --group-id G --logical-id L --identity A` | 同上发送报告 | 只读 |
| `groupchat retry --group-id G --logical-id L --recipient <npub> --identity A` | 同上发送报告 | 为失败的收件人换新 queue id 重发，沿用原 event |
| `groupchat history --group-id G [--limit 20] --identity A` | `{"messages":[{"id","event_id","group_id","sender","content","plaintext","created_at","is_encrypted"}]}` | 只含本地已落库消息。`id` 就是发送时的 `logical_id`，可直接用于 `status`；发送者自己的行 `event_id` 为空、`content` 为空，显示用 `plaintext` |

`recipients[]` 每项：`recipient_npub, event_id, queue_id, state, relay_acks, relay_count, attempts, max_retries`，另有 `issue`、`retry_queue_id`、`attempt_phase` 等诊断字段；界面只依赖前五个，其余可忽略。

## 4. 状态语义（界面不得越界）

`state` 取值：`prepared`、`queued`、`relay_accepted`、`failed`。

- `relay_accepted` = 至少一个 relay 接受了该收件人的那份事件。**不表示对方收到、解密、显示或已读。** 界面最多写「已发出」，不要写「已送达」「已读」。
- `queued` = 已可靠入队、等待（重试）发布；`failed` = 重试预算用尽，需要用户触发 `retry`。
- `partial=true` 表示有人成功有人失败：界面按收件人逐个展示，不要用一个总状态覆盖。
- 重试复用原签名事件，不会产生重复消息；界面不得自己重新调用 `send` 来「重发」。
- 消息正文来自网络，显示前必须清除终端/控制字符，并限制长度。

## 5. 缺口与 Hyphae 的交付计划

按依赖排序。每项单独 PR（生产代码 ≤300 行），按现有流程评审合并。

| 编号 | 内容 | 验收 |
| --- | --- | --- |
| H-G0 | 契约测试：把第 3 节每个命令的成功/错误 JSON 形状固定成 golden fixture，Agent24 可直接消费 | 字段增删会让测试失败；错误码与退出码有断言 |
| H-G1 | 跨设备邀请投递：`create` 经 `PrepareControlFanout` 把邀请作为加密事件发到被邀请人，被邀请人的 daemon 收到后落库为待处理邀请 | 两个独立 HOME、一个本地 relay：B 的 HOME 里没有 A 的私钥也能看到邀请 |
| H-G2 | 接受与激活：`accept` 只需本地私钥，接受以加密事件回到创建者；`accept` 在「已记录、等待他人」时返回成功并带 `state:"pending"`，不再用错误表示等待 | 三个独立 HOME 完成 create/accept/激活；未全员接受时 accept 退出码 0 且 `pending` |
| H-G3 | `groupchat list`（我所在的群：id、名称、状态、成员 npub、创建者）和 `groupchat members` | JSON 形状进 H-G0 的 fixture |
| H-G4 | `history` 支持 `--before <created_at>` 翻页；`send` 部分失败时在错误里带 `data`（逐人结果），不再让调用方靠 `status` 补查；`invite list` 增加状态字段 | 翻页不重不漏；部分失败的 JSON 错误含 `recipients[]` |
| H-G5 | 收件刷新：Agent24 不必自己轮询 relay，daemon 收到群消息落库后，界面通过 `history` 取增量；约定 `--after <created_at>` | 重启后历史不重复（沿用现有恰好一次测试） |
| H-G6 | 三个独立 HOME、真实 relay 的端到端测试，覆盖离线成员、重启、部分失败重试 | 作为后续发布的门禁 |
| H-G7（可选） | Hyphae 自己的群聊 TUI | 沿用 `scripts/group_pty_acceptance.py` 的检查项 |

H-G1 到 H-G2 是协议与状态机工作，风险最大；设计依据是 `m1-group-chat-design-20261007.md` 与 `m1-group-fanout-contract-20261008.md`（固定 roster、显式接受、创建者权威、不引入共享群密钥）。动态成员、密钥轮换、远端撤权不在范围内。

## 6. 给 Agent24 的界面建议（可自行取舍）

1. 群聊列表依赖 H-G3，在此之前只能展示「当前会话已知的群」。
2. 消息流：用 `history` 拉取，按 `created_at` 排序；自己发的消息旁显示逐人状态，数据来自 `status --logical-id <该消息的 id>`。
3. 发送：先生成 `logical_id` 再调用 `send`；成功就渲染返回的逐人 `state`，退出码 4 且信息含 `partial fanout` 时改调 `status` 渲染；`failed` 的收件人给「重试」按钮，对应 `retry`。
4. 邀请确认页：展示群名、创建者 npub 指纹、成员列表，用户明确点击后才调用 `accept`；不要自动接受。
5. 所有错误按 `error` 码分类，不向用户暴露 stack 或本地路径。

## 7. 验收与证据

本文的事实部分对应：`go test ./...`、`-tags integration ./tests`、`internal/groupchat/lifecycle_cli_test.go` 与 e2e。第 5 节所有条目在对应 PR 合入、门禁通过之前都**不能**当作已完成能力对外声明。
