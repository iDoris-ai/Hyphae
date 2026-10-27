# 交接说明：Agent24 把 Hyphae 锁升级到 v0.27.0

日期：2026-10-27。写给 Agent24 团队。本文是 Hyphae 一侧的设计与约定接口说明，由 Agent24 自行完成接线。Hyphae 不修改、也没有读取 Agent24 仓库代码；凡涉及 Agent24 内部文件和现状的地方，原先标「待确认」的各项已由 Agent24 在 iDoris-ai/Agent24#1346 逐条核对并回帖，下文标注「Agent24 已核对」处的事实来自 Agent24 的回复，不是 Hyphae 读 Agent24 代码得出的。有出入时以 Hyphae 发布的二进制实际输出和 `SHA256SUMS` 为准，并在 Hyphae 仓库提 issue。

## 1. 要升级到哪里

| 项 | 值 | 来源 |
| --- | --- | --- |
| 当前已知的 Agent24 锁（升级起点） | Hyphae `144555ca6b043be77225786ddbf4e6bce5ad3228`（v0.26.3） | `.github/joint-ci-baseline.json` 的 `production_hyphae_sha`；对应 Agent24 `3a433bb0`（#1193 合并）。**已核对（2026-10-27，Agent24 在 iDoris-ai/Agent24#1346 回帖）：** 此后锁已两次变动。v0.27.0 `9126147e` 已合入 Agent24 main（#1352）；v0.27.1 `859d8fb7` 见 Agent24 #1368，合并前以其为准。`144555ca` 仅是 10-24 起点。Agent24 仓库里没有 `joint-ci-baseline.json`，该文件在 Hyphae 一侧 |
| 最新版本 | `v0.27.1`（`859d8fb7e79c67beb3c7a5a5d6d58c36baac7d0f`） | 相对 v0.27.0 只多群聊 TUI 与 TUI 侧群发布器注册，命令 JSON 契约不变；https://github.com/iDoris-ai/Hyphae/releases/tag/v0.27.1 。Agent24 目标锁 v0.27.1 |
| 目标 tag | `v0.27.0` | https://github.com/iDoris-ai/Hyphae/releases/tag/v0.27.0 （2026-10-26 发布） |
| 目标 commit | `9126147e8eb5e497e3055f9eda27473fefbb91b2`（PR #288 合并提交） | `gh api repos/iDoris-ai/Hyphae/git/ref/tags/v0.27.0`，已核对 tag 指向该 commit |
| Go 工具链 | `go1.26.4` | release 的 `hyphae.lock.json` |
| 版本显示 | CLI 自报 `dev`；用 manifest 和 hash 识别版本 | release notes |

### 1.1 Release 资产（共 9 个）

`artifact-manifest.json`、`hyphae-darwin-arm64`、`hyphae-darwin-arm64.tar.gz`、`hyphae-linux-amd64`、`hyphae-linux-amd64.tar.gz`、`hyphae-relay-darwin-arm64`、`hyphae-relay-linux-amd64`、`hyphae.lock.json`、`SHA256SUMS`。

### 1.2 校验和怎么取

不要抄本文里的数值，自己取并核对：

```bash
gh release download v0.27.0 -R iDoris-ai/Hyphae -p SHA256SUMS -p hyphae.lock.json -p artifact-manifest.json
cat SHA256SUMS                # 以此为准（列出除自身外的 8 个文件）
shasum -a 256 -c SHA256SUMS   # 需把对应二进制一并下载到同目录
cat hyphae.lock.json          # 看 source_sha 与两平台 binaries
```

2026-10-27 取到的 `hyphae.lock.json`（仅供对照，仍以 release 现取为准）：`source_sha` = `9126147e8eb5e497e3055f9eda27473fefbb91b2`；`binaries.darwin-arm64` 以 `6b7969a3` 开头，即 `hyphae-darwin-arm64` 的 sha256；`binaries.linux-x64` 以 `98e5e9d6` 开头，即 `hyphae-linux-amd64` 的 sha256；`darwin-x64`、`linux-arm64` 为 `null`。完整数值请用上面命令取全，以 `SHA256SUMS` 为准。

release notes 声明：两次独立构建得到完全相同的 `SHA256SUMS`。

### 1.3 Agent24 侧要改什么（Agent24 已核对）

v0.26.3 那次升级的先例（见 `docs/agent/release-v0.26.3-progress.md` 的 T4.1/T4.2）改了：`rust/crates/agent24-comm/hyphae.lock.json`（`source_sha` 与两平台 hash）、`src/binary.rs` 里 `embedded_lock_parses_and_resolves_baseline_platform` 的断言、`docs/Deployment/HYPHAE-NETWORK-DELIVERY.md` 的当前锁说明，另写了升级记录 `docs/comm/LOCK-UPGRADE-20261024.md`。这些是 10-24 的记录。Agent24 核对后的结论：布局基本一致，有以下补充。
- 锁经 `include_str!` 嵌入（`rust/crates/agent24-comm/src/binary.rs`），`embedded_lock_parses_and_resolves_baseline_platform` 的断言仍是必改文件。
- 升级还要改 `docs/Deployment/HYPHAE-HEALTH-ROLLBACK-CONTRACT.md`（它引用当前锁 `source_sha`，两处），上面的清单漏了这个文件。
- 升级记录现在命名为 `docs/comm/LOCK-UPGRADE-<日期>[b]`（v0.27.0 是 `LOCK-UPGRADE-20261027.md`，v0.27.1 是 `LOCK-UPGRADE-20261027b.md`），不是 20261024 那份。
- 运行时数据布局（由 Agent24 提供）：`<state_dir>/comm/hyphae-home`（Hyphae 的 HOME，agent24d 强制设置 `HOME`）、`comm/bin`（校验后拷入的二进制副本）、`comm/hyphae-daemon.pid`、`comm/logs/hyphae-daemon.log`、`comm/daemon-autostart.json`。二进制来源按 `A24_HYPHAE_BIN` → 旧 `A24_SPEAKER_BIN` → agent24d 同目录 `hyphae` 解析，校验的是嵌入锁里的 sha256。
- Agent24 的 `darwin-x64`、`linux-arm64` 条目在 v0.27.x 无 release 资产，保持 null。

## 2. v0.27.0 新增 / 变化

权威来源：`docs/agent/group-chat-agent24-interface-20261025.md`（命令表与状态语义）和 `internal/groupchat/contract_json_test.go`（`TestCommandJSONContract`，字段集被测试钉住，增删改名会让测试失败）。

### 2.1 通用约定

- 加 `--json`（或 `HYPHAE_OUTPUT=json`）。成功：stdout 输出 `{"ok":true,"data":…}`。失败：stderr 输出 `{"ok":false,"error":"<code>","message":"…"}`，部分失败时多一个 `data`。
- 退出码（`internal/common/output.go`）：0 成功；1 用户错误；2 网络错误；3 鉴权错误；4 其他错误；5 写冲突。
- 需要口令时用 `--password-stdin`，不要用 `--password`。身份用 `--identity <昵称>`，缺省为默认身份。

### 2.2 `hyphae groupchat` 子命令与 `data` 字段

| 命令 | 成功 `data` 字段 | 备注 |
| --- | --- | --- |
| `create --name N --identity A --invitee <npub>…` | `group_id` | 对本机没有私钥的被邀请人，邀请以加密事件排队，由 daemon 发布 |
| `invite list --group G --identity A` | `invites[]`：`group_id, invite_id, invitee_npub, state` | v0.27.0 起带 `state`（#287） |
| `accept --invite I --identity B` | `group_id, state` | `state` 为 `pending` 或 `active`，两者退出码都是 0。以前在等待期会以 `group is not active`、退出码 4 失败，现已改变 |
| `send --group-id G --body T [--logical-id L] [--max-retries N] --identity A` | `accepted, created_at, failed, group_id, logical_id, queued, recipients[], state` | 建议调用方自己传 `--logical-id` |
| `status --group-id G --logical-id L --identity A` | `bookkeeping, failed, group_id, logical_id, partial, queued, recipients[]` | 只读。字段集与 `send` 不完全相同（测试里是两组不同的键），按各自字段解析 |
| `retry --group-id G --logical-id L --recipient <npub> --identity A` | 与 `send` 相同 | 沿用原签名事件，换新 queue id，不产生重复消息 |
| `list --identity A` | `groups[]`：`creator_npub, group_id, members, name, state, updated_at` | v0.27.0 新增（#286） |
| `members --group-id G --identity A` | `members[]`：`is_creator, npub, state` | v0.27.0 新增（#286） |
| `history --group-id G [--limit 20] [--before-id ID \| --after-id ID] --identity A` | `messages[]`：`content, created_at, event_id, group_id, id, is_encrypted, plaintext, sender` | v0.27.0 新增游标（#287）。`id` 即发送时的 `logical_id`；发送者自己的行 `event_id`、`content` 为空，显示用 `plaintext`；未知游标或同时给两个游标 → 退出码 1 |

`recipients[]` 每项至少含 `recipient_npub, event_id, queue_id, state, relay_acks, relay_count, attempts, max_retries`（测试钉住这些字段）；另有 `issue`、`retry_queue_id`、`attempt_phase` 等诊断字段，界面不应依赖。

### 2.3 部分失败的信封（重要）

`send` 部分收件人失败时：退出码 4，stdout 为空，stderr 为

```json
{"ok":false,"error":"other_error","message":"logical <id>: partial fanout queued X failed Y","data":{ ...与成功时相同的发送报告... }}
```

`TestCommandJSONContract` 用 `HYPHAE_FAIL_GROUP_RECIPIENT` 钉住了这一形状。Agent24 应解析 stderr 的 `data`，不要把退出码 4 一概当作发送失败。

### 2.4 状态语义

`state` 取 `prepared`、`queued`、`relay_accepted`、`failed`。`relay_accepted` 只表示至少一个 relay 接受了该收件人的那份事件，**不代表对方收到或已读**，界面最多写「已发出」。`queued` = 已可靠入队等待发布；`failed` = 重试预算用尽，需要用户触发 `retry`。界面不得自己重新调用 `send` 来重发。消息正文来自网络，显示前须清除控制字符并限长。详见接口文档第 4 节。

### 2.5 其他变化

- 生产 daemon 现在会发布群 outbox 行（此前只有测试注册了群发布器，群事件不会离开 outbox）。跨设备群聊的前提是每台设备的 `hyphae daemon` 在运行。（#284、#285）
- 旧 `group remove-member` / `group delete` 改为返回用户错误、退出码 1，不再假装成功（#283）。
- 单聊命令在本次发布中无已知契约变化。Agent24 的 v0.26.3 单聊联调（`joint_round1`、`send_history_e2e`、`daemon_joint`）结论见 `release-v0.26.3-progress.md`；Agent24 已在 v0.27.0（#1352）与 v0.27.1（#1368）上重跑，三条均通过。

## 3. 已知缺口与 Agent24 需注意的事项

来自 release notes 与接口文档第 5 节，均为 Hyphae 一侧事实：

1. **没有群聊 TUI**。`hyphae group chat` 是占位输出，`hyphae tui` 只有单聊（H-G7，可选，未做）。群聊界面只能由 Agent24 经 CLI 实现。
2. **TUI 进程不注册群发布器**，只有 `hyphae daemon` 发布群事件。若用户只开 TUI 不开 daemon，排队的群事件不会发出。**Agent24 已核对：** Agent24 只跑 `hyphae daemon`，不使用 `hyphae tui`；由其 `HyphaeDaemonSupervisor` 监管（启动参数 `daemon --identity <昵称> --password-stdin --json --notify=false --auto-reply=false [--relay …]`，口令走 stdin，独立进程组，不传 `--watch-interval`）。崩溃重启 500ms 起翻倍、连续 5 次熔断、稳定运行 60 秒复位；停止为 SIGTERM 进程组、宽限后 SIGKILL，按 pid 加启动时间校验后才杀。v0.27.1 起 TUI 也会注册发布器，对 Agent24 无影响。
3. **`activating` 卡住**：创建者 daemon 在记下最后一个 accept 与排队激活之间崩溃，群会停在 `activating`，要等同一 accept 事件重放才修复，没有定期修复。界面应能容忍群长时间处于 `activating`。
4. **离线成员 / 重启只覆盖同机单 HOME**：`TestM1GroupInviteOfflinePartialFanoutExactlyOnce` 覆盖同机版本；跨 HOME 只做了 `TestGroupAcrossThreeHomesThroughRelay`（建群、邀请、接受、激活、消息、`relay_accepted`）。跨 HOME 的离线 / 重启 / 部分失败重试没有重做，不能对外声称已验证。
5. 本地 `history` 只含已落库消息，不是 relay 上的全量历史。
6. 旧 relay 与新客户端的组合未测（见 `release-v0.26.3-progress.md` 范围外一节）。
7. `accept` 应由明确的用户操作触发，不要自动接受邀请（接口文档第 6 节建议）。
8. Agent24 的 `/api/v1/comm/*` 或 Rust 路由若已对 `groupchat` 做过假设（例如把 `accept` 非 0 退出码当失败），需对照 2.2 重新检查。**Agent24 已核对：** 全仓目前没有 `groupchat` 调用；信封解析按退出码类别与信封处理（退出码 0 要求 `ok:true`，1–5 要求 stderr 的 `ok:false` 信封并保留 `data`），`accept` 的 `pending` 与 `send` 部分失败都能原样解析。群聊接线拆为 Agent24 #1360、#1361、#1362。

## 4. 升级后建议的验收步骤

1. **核对产物**：按 1.2 取 `SHA256SUMS`、`hyphae.lock.json`，`shasum -a 256 -c`；确认 `source_sha` 为 `9126147e…`。Agent24 的 `.github/workflows/hyphae-lock-verify.yml` 可直接复用（Agent24 已确认）：它在锁文件变更时按 `source_sha` 与 `recipe` 在 ubuntu 上重建 linux-x64 并比对；darwin-arm64 无 CI 复现，由升级人本机复算。
2. **本地真二进制测试**：用 v0.27.0 的 darwin-arm64 / linux-amd64 CLI 跑 Agent24 自己调用真二进制的测试，以及三条 `#[ignore]` 联调用例（`joint_round1`、`send_history_e2e`、`daemon_joint`；环境变量 `HYPHAE_JOINT_BIN`、`HYPHAE_JOINT_RELAY` 的用法见 `release-v0.26.3-progress.md` T5.1～T5.3，relay 用同一 bundle 的 `hyphae-relay-*`）。
3. **群聊契约对照**：用真二进制对 2.2 的每个命令各跑一遍 `--json`，对照字段，尤其是 `accept` 的 `pending`、`send` 部分失败的 stderr `data`、`history --before-id/--after-id`。Hyphae 一侧的参考测试：`go test ./internal/groupchat -run TestCommandJSONContract -v`、`go test -tags integration ./tests -run TestGroupAcrossThreeHomesThroughRelay -v`。
4. **联合验收 workflow**（`.github/workflows/joint-ci.yml`，说明见 `docs/agent/joint-ci-acceptance.md`）：
   - 触发方式：`workflow_dispatch`、`workflow_call`、每日定时（cron `23 4 * * *`）；Hyphae 的普通 CI 也会调用它。手动触发例如 `gh workflow run joint-ci.yml -R iDoris-ai/Hyphae --ref main`（`--ref` 对应的 commit 即被验证的 candidate 源）。
   - 它跑 `pinned-production` 与 `candidate` 两种模式，各在 linux-x64 与 darwin-arm64 原生 runner 上跑十阶段 runner。
   - **pinned-production 用的是 `.github/joint-ci-baseline.json` 里记录的 Agent24 sha 和 production lock**。Agent24 把锁升到 v0.27.0 并合并后，Hyphae 需要另开 PR 更新该 baseline（`agent24_base_sha`、`production_lock_sha256`、`production_hyphae_sha`），先例是 Hyphae #281（见 `release-v0.26.3-progress.md` T7.1）。该 workflow 不会自动发现 Agent24 的新提交。Hyphae 现在的 baseline 仍是 Agent24 `3a433bb0…` / Hyphae `144555ca…`。
   - 只有某个确切 SHA 的 consumer job 成功后，才可说该 SHA 通过联合验收。
5. **结果回报**：把 Agent24 的锁升级 PR 号、合并 commit、联调日志结论反馈给 Hyphae，由 Hyphae 更新 baseline 与进度文档。
