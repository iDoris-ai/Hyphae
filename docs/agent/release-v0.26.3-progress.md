# v0.26.3 阶段发布 + Agent24 联调进度

规划来源：[Hyphae 现状与阶段发布计划](https://claude.ai/artifact/FxkWCxHbFXqgjqski5oxLE)（2026-10-24）。本文件是执行进度的权威记录：每完成一个子任务就更新状态、证据和时间。会话中断后从第一个非 DONE 的任务继续。

状态取值：TODO / DOING / DONE / BLOCKED / SKIPPED。时间为清迈时间（UTC+7）。

## 已定的默认决策

| 事项 | 决定 | 来源 |
|---|---|---|
| 版本号 | `144555ca` 发 `v0.26.3`；`v0.26.2` tag 保持不动，不补 Release | 报告推荐，用户 2026-10-24 说"开始吧" |
| Agent24 lock 升级 PR | 由本会话开，代码改动交给 Sonnet 子代理写 | 同上 |
| 上游 NIP-44 两项修复 | 不在本计划内 | 同上 |
| 合并规则 | clestons 批准精确 head、必需 CI 绿、MERGEABLE 后 `gh pr merge --merge`；不 self-approve，不 `--admin` | handoff-20261024 |

## 任务

| ID | 步骤 | 子任务 | 负责 | 状态 | 证据 / 备注 |
|---|---|---|---|---|---|
| T0 | 准备 | 建立本进度文档 | Hyphae | DONE | 本文件 |
| T1.1 | 1 验证 | 在 `144555ca` 上重跑三条 M1 exit 测试 | Hyphae | DONE | 2026-10-24：`go test -tags integration ./tests -run '…三条…' -count=1` 三条 PASS（3.63s / 3.33s / 4.29s）。代码与 `144555ca` 相同，当前分支只多文档提交。Go 1.27.2 |
| T1.2 | 1 验证 | `go vet`、`go test ./... -race`、`go test -tags integration ./tests` | Hyphae | DONE | 2026-10-24：`go vet ./...` 无输出；`go test ./... -race -count=1` 全部 ok；integration 全套 ok（98s）；M1/Offline 加 `-race` ok |
| T2.1 | 2 发布 | 准备 Go 1.26.4 工具链（本机缓存已无） | Hyphae | DONE | 2026-10-24：`GOTOOLCHAIN=go1.26.4 go version` 自动下载；工具链在 `~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.4.darwin-arm64`，构建时把它的 `bin` 放到 PATH 最前 |
| T2.2 | 2 发布 | 干净 checkout `144555ca`，用 `scripts/build_release_artifacts.py` 构建 bundle，两次构建 hash 一致 | Hyphae | DONE | 2026-10-24：两次构建 `SHA256SUMS` 完全一致。darwin-arm64 CLI `545636015d027fa4614ad48361829ff5a5116683a5553f8e2d08fb65ae14261c`；linux-x64 CLI `d741fb9461a6b16d92d471eb103536295808e4dc22a3874b193e12937ee8b99b`；lock 文件 `007fbfbce8f78c98b9a55faa4f60ba1ec28a2835e1d3510905fdbfc8e51c9cf8`。产物在会话 scratchpad `rel/out-a` |
| T2.3 | 2 发布 | `scripts/verify_artifact_bundle.py` 校验 bundle | Hyphae | DONE | 2026-10-24：`shasum -c SHA256SUMS` 八个文件全 OK；`hyphae.lock.json` 的 source_sha 和两平台 hash 与二进制一致；darwin 二进制可运行。`verify_artifact_bundle.py` 对 Agent24 现有生产 lock 报 `production-lock-sha256`：这是预期的，它拿新 bundle 对旧 lock，要等 T4 升级 lock 之后才会通过 |
| T2.4 | 2 发布 | 打 tag `v0.26.3` 指向 `144555ca` 并推送 | Hyphae | DONE | 2026-10-24：轻量 tag（与 v0.26.2 一致），`git ls-remote` 确认指向 `144555ca6b04…` |
| T2.5 | 2 发布 | 创建 GitHub Release，上传 assets，写 release notes | Hyphae | DONE | 2026-10-24：https://github.com/iDoris-ai/Hyphae/releases/tag/v0.26.3 ，非 draft，9 个 assets，digest 与 `SHA256SUMS` 一致；下载 `SHA256SUMS` 与本地逐字节相同，darwin CLI 和 lock 文件校验 OK |
| T3.1 | 3 joint-ci | 手动触发 joint-ci candidate 模式对准 `v0.26.3` | Hyphae | DONE | 2026-10-24：run [40109654088](https://github.com/iDoris-ai/Hyphae/actions/runs/40109654088)，head `144555ca`（= v0.26.3），六个 job 全部 success：darwin-arm64 与 linux-x64 的 producer，以及 candidate / pinned-production 各两个平台的十阶段 runner。注意：pinned-production 仍对 Agent24 旧锁 `671c584`，candidate 才是 v0.26.3 |
| T4.1 | 4 lock 升级 | 读 Agent24 LOCK-UPGRADE-20261005 先例，确定要改的文件 | Agent24 | DONE | 2026-10-24：先例在 `docs/comm/LOCK-UPGRADE-20261005.md`（#676）。要改：`rust/crates/agent24-comm/hyphae.lock.json`、`src/binary.rs` 里 `embedded_lock_parses_and_resolves_baseline_platform` 断言、`docs/Deployment/HYPHAE-NETWORK-DELIVERY.md` 当前锁说明，另写一份升级记录。工作流不用改（从 lock 读值）。在独立 worktree `/Users/jason/Dev/auraai/Agent24-hyphae-lock-v0263` 做，不动用户在 Agent24 主目录的分支和未跟踪文件 |
| T4.2 | 4 lock 升级 | 新分支改 `hyphae.lock.json`（`source_sha`、两平台 hash）和 `binary.rs` 断言 | Agent24 | DONE | 2026-10-24：分支 `jhfnetboy/hyphae-lock-v0.26.3-20261024`，基于 Agent24 origin/main `f16e65a9`；新增 `docs/comm/LOCK-UPGRADE-20261024.md`。6 行替换，没有改生产逻辑 |
| T4.3 | 4 lock 升级 | 本地 `cargo test -p agent24-comm` 和 lock verify | Agent24 | DONE | 2026-10-24：`HYPHAE_TEST_BIN=<v0.26.3 darwin 二进制> cargo test -p agent24-comm` 全部 ok：单元 163 项；`real_binary`、`import_real_binary`、`keystore_write_lock`、`router_lifecycle` 等调用真二进制的用例通过；3 个 `#[ignore]` 联调用例（`joint_round1`、`send_history_e2e`、`daemon_joint`、`unlock_real_binary`）留到 T5。linux lock verify 由 CI 的 `hyphae-lock-verify` 在 PR 上跑 |
| T4.4 | 4 lock 升级 | push、开 PR、等 CI 和 clestons 评审、合并 | Agent24 | DONE | 2026-10-24：[Agent24 #1193](https://github.com/iDoris-ai/Agent24/pull/1193) 已合并，merge commit `3a433bb0ce02a801a3a424ce731ec3878454e078`。clestons 批准精确 head `936e3c14`，7 项检查全过（含 `hyphae-lock-verify`），`--match-head-commit` 合并，没有 self-approve、没有 `--admin`。Codex 一条 P2（回滚契约文档仍写旧锁）已在合并前修掉 |
| T5.1 | 5 联调 | `joint_round1` | Agent24 | DONE | 2026-10-24：`cargo test -p agent24-comm --test joint_round1 -- --ignored --nocapture` 1 passed（4.23s）。日志末行 `all findings are asserted above; every step matched the expected behavior`。覆盖：A→B 收发、断线入队、同 event_id 重试、错口令 `AuthError`。环境：Agent24 origin/main `3a433bb0`（含 #1193），`HYPHAE_JOINT_BIN`=v0.26.3 darwin-arm64 CLI（sha256 `54563601…`，与新 lock 一致），`HYPHAE_JOINT_RELAY`=同 bundle 的 `hyphae-relay-darwin-arm64`，macOS arm64 本机。日志在会话 scratchpad `joint/` |
| T5.2 | 5 联调 | `send_history_e2e` | Agent24 | DONE | 2026-10-24：1 passed（3.02s），`send/pull/retry round trip against real binaries: all assertions passed`。环境：Agent24 origin/main `3a433bb0`（含 #1193），`HYPHAE_JOINT_BIN`=v0.26.3 darwin-arm64 CLI（sha256 `54563601…`，与新 lock 一致），`HYPHAE_JOINT_RELAY`=同 bundle 的 `hyphae-relay-darwin-arm64`，macOS arm64 本机。日志在会话 scratchpad `joint/` |
| T5.3 | 5 联调 | `daemon_joint` | Agent24 | DONE | 2026-10-24：1 passed（32.76s）：daemon 启动 → running → 经 watch-interval 自动把 B 的消息拉进 A 的 history → 停止，全程走 Agent24 路由。另跑 `unlock_real_binary`（`HYPHAE_TEST_BIN`）1 passed（8.60s）。环境：Agent24 origin/main `3a433bb0`（含 #1193），`HYPHAE_JOINT_BIN`=v0.26.3 darwin-arm64 CLI（sha256 `54563601…`，与新 lock 一致），`HYPHAE_JOINT_RELAY`=同 bundle 的 `hyphae-relay-darwin-arm64`，macOS arm64 本机。日志在会话 scratchpad `joint/` |
| T6.1 | 6 混合版本 | 旧 `671c584` 二进制与 `v0.26.3` 双向收发 | 两边 | DONE | 2026-10-24：A=旧 CLI（`671c584` 按 lock 配方重建，sha256 `d1171421…` 与旧 lock 一致，身份加密），B=v0.26.3 darwin CLI（`54563601…`），同一个 v0.26.3 relay，隔离 HOME。结果：旧→新、新→旧都 `published_to=1`，对端 history 各恰 1 条；各再拉取一次后仍是 1 条；新端 relay 断开时发送 `published_to=0, queued_for_retry=true`，relay 恢复后 `storage outbox retry` 返回 `sent=true`，同一 event id（`97eab33b…`），旧端 history 恰 1 条。脚本和输出在会话 scratchpad `mixed/run.sh`、`mixed/result.txt`。未覆盖：旧 relay 与新客户端的组合；群聊（旧版没有） |
| T7.1 | 7 基线 | Agent24 合入后更新 `.github/joint-ci-baseline.json`，开 PR | Hyphae | DONE | 2026-10-24：[Hyphae #281](https://github.com/iDoris-ai/Hyphae/pull/281) 已合并，merge commit `58f378dcc4531eff20e8ef3bc93e0de13d475a85`。baseline 改到 Agent24 `3a433bb0` / lock sha256 `78a58f5f…` / Hyphae `144555ca`。PR head `92b6fde7` 的 CI run [40112860007](https://github.com/iDoris-ai/Hyphae/actions/runs/40112860007) 全部 11 项通过，含两个构建 job 和四个十阶段 runner（candidate / pinned-production × darwin-arm64 / linux-x64），这是新 baseline 第一次在 Agent24 `3a433bb0` 上真跑。clestons 批准精确 head，`--match-head-commit` 合并。过程中两个返工：Codex P2（文档把旧 10/10 说成验证了新 pin，已改措辞）；clestons Blocker（我 `git add -A` 误提交 18 个 `.pyc`，已取消跟踪并加 `.gitignore`）|
| T8.1 | 8 发布后加固 | 在发布提交上跑完整门禁：vet、gofmt、`go test ./... -race`、`-tags integration ./tests`、Python 工具测试 | Hyphae | DONE | 2026-10-25：`main` 在 `c456122e`（含 v0.26.3 源码 `144555ca`）全部通过；`./tests` 105s、Python 119 项。逐包语句覆盖率 0%～98%；合并 `-coverpkg` 后进程内总覆盖率 79.4%（子进程里跑的 CLI 包装函数不计入，真实值更高） |
| T8.2 | 8 发布后加固 | 功能 × 测试矩阵：逐个子命令检查有没有 Go 级测试 | Hyphae | DONE | 2026-10-25：缺口是 `storage audit-log`、`storage migrate`、旧 `group` 的 `add-member/remove-member/delete`、`identity export`（只被 CI 不跑的 shell 脚本碰过）。`profile show/list/search/discover`、`tui chat/contacts` 仍只有脚本覆盖，未补 |
| T8.3 | 8 发布后加固 | 补 `cmd/hyphae/cli_surface_test.go`（5 个用真实二进制的测试） | Hyphae | DONE | 2026-10-25：全部通过；把旧的桩行为放回后 `TestLegacyGroupRosterLifecycle` 变红，证明断言有效。测试同时钉住 `audit-log --verify` 在篡改后非 0 退出 |
| T8.4 | 8 发布后加固 | 修缺陷：旧 `group remove-member`/`group delete` 是空桩，打印警告却退出码 0 | Hyphae | DONE | 2026-10-25：改为返回 `user_error`（退出码 1，stderr `… is not implemented yet`）。`group chat` 占位保持原样，因为 `internal/groupchat/lifecycle_cli_test.go` 与 `internal/tui/group_retry_test.go` 明确钉住它 |
| T8.5 | 8 发布后加固 | 修陈旧的群聊 PTY 探针 | Hyphae | DONE | 2026-10-25：`scripts/group_pty_acceptance.py` 只看 `group --help`，会误报「邀请/接受不存在」。改为探测 `groupchat`，原因文案改为「CLI 已有，但无群聊 TUI、跨设备邀请投递未实现」。检查项仍是 NOT_IMPLEMENTED，没有伪造通过 |
| T8.6 | 8 发布后加固 | 修 #282 合并后 Codex 的 P2：handoff 仍让人从 `144555ca` 开始 | Hyphae | DONE | 2026-10-25：`handoff-20261024.md` 区分「发布源码」与「当前 main」，以 `git fetch` 后的 `origin/main` 为准 |
| T8.7 | 8 发布后加固 | 核实群聊真实边界并更正此前说法 | Hyphae | DONE | 2026-10-25：群聊只在「所有成员身份在同一 keystore」时成立（`accept` 要创建者私钥，`deliverActivations` 要被邀请人私钥；`PrepareControlFanout` 未被 CLI 调用）。此前「M1 含三人群聊完成」应读作「同机三身份群聊完成」。详见 `group-chat-agent24-interface-20261025.md` |
| T8.8 | 8 发布后加固 | 清理临时资源 | Hyphae | DONE | 2026-10-25：删除两个已合并 worktree（Hyphae `joint-baseline-v0263`、Agent24 `Agent24-hyphae-lock-v0263`）及本地已合并分支；终止 10 个自 10-01 起空转的残留进程（`/bin/sh …/hyphae-<hash> daemon --relay wss://relay.example` 九个，脚本文件已删、PPID 1，其余一个 `ws://127.0.0.1:1`）和两个旧 sidecar/relay。保留 `/tmp/pr-daemon-check-fetch`（可复用的 pre-pr-check 副本）。`/private/tmp` 共 261 GB，大头是 130 个 `idoris-*` 构建目录（来自 iDoris/Agent24 仓库，不是本仓库产物），未动 |
| T9.1 | 9 群聊契约 | 写 Agent24 接口契约与 Hyphae 交付计划 | Hyphae | DONE | 2026-10-25：`docs/agent/group-chat-agent24-interface-20261025.md`。命令 JSON 形状全部用真实二进制核对过，包括两处文档最初写错后更正的行为（部分失败时无逐人数据；`id` 即 `logical_id`）。H-G0～H-G7 是 Hyphae 要做的，Agent24 仅按契约接入 |
| T10.1 | 10 接线版本 | 跨设备群聊接线：H-G1…H-G4/H-G5（#284–#287）；H-G0 契约测试与文档（#288） | Hyphae | DONE | 2026-10-26：五个 PR 均经 clestons 批准精确 head 后 `--merge`，未 self-approve、未 `--admin`。#284 被要求修改：生产 daemon 没有注册 group 发布器，所有群 outbox 行（邀请与消息）从不发布；已补 `groupPublisher` 与三 HOME 真 relay 测试（去掉注册会超时） |
| T10.2 | 10 接线版本 | 在 `9126147e` 跑发布门禁 | Hyphae | DONE | 2026-10-26：`go vet`、gofmt、`go test ./... -race`、`-tags integration ./tests`（196s，含 `TestGroupAcrossThreeHomesThroughRelay`）、Python 119 项全过 |
| T10.3 | 10 接线版本 | 两次干净构建 bundle，hash 一致；校验 | Hyphae | DONE | 2026-10-26：Go 1.26.4，`SHA256SUMS` 两次相同；darwin CLI `6b7969a3d4e9767e65927ebd0c2830d272c4b567f05e740a985291b9e462a01a`；`shasum -c` 全 OK；构建出的二进制有 `groupchat list` |
| T10.4 | 10 接线版本 | 打 tag `v0.27.0`（`9126147e`）、发 GitHub Release | Hyphae | DONE | 2026-10-26：https://github.com/iDoris-ai/Hyphae/releases/tag/v0.27.0 ，非 draft，9 个 assets；线上 `SHA256SUMS` 与本地逐字节相同，darwin CLI hash 一致 |
| T10.5 | 10 接线版本 | joint-ci 在 main 上手动触发 | Hyphae | DONE | 2026-10-26：[run 40283438544](https://github.com/iDoris-ai/Hyphae/actions/runs/40283438544)，head `9126147e`，六个 job 全部 success（两平台 producer，candidate / pinned-production 各两平台的十阶段 runner）。注意 joint-ci 跑的是旧 Agent24 契约下的十阶段，不含群聊跨设备流程；群聊跨设备由 `TestGroupAcrossThreeHomesThroughRelay` 覆盖 |
| T10.6 | 10 接线版本 | 群聊 TUI（H-G7，#291）与交接说明（#290）；打 `v0.27.1`（`859d8fb7`） | Hyphae | DONE | 2026-10-27：#291 被要求修改（群重试无退避、`ctrl+u` 翻页不生效、重复发送），修复后 clestons 批准精确 head `6292f680`。`v0.27.1` 两次构建 `SHA256SUMS` 一致，9 个 assets；joint-ci [run 40463638383](https://github.com/iDoris-ai/Hyphae/actions/runs/40463638383) 成功。Agent24 跟进 issue：iDoris-ai/Agent24#1346 |

## 日志

- 2026-10-24：建立文档。`v0.26.2` tag 在 `9c1b9f5a`，无 Release；Agent24 main `f16e65a9` 锁 Hyphae `671c584`。
- 2026-10-24：T1–T7 全部完成。v0.26.3 已发布，Agent24 锁到 `144555ca`（#1193），三条联调和混合版本互通通过，Hyphae joint-ci baseline 已跟进（#281）。main 现在是 `58f378dc`。
- 2026-10-25：T8 发布后加固与 T9.1 群聊契约。#282 已合并（`c456122e`），合并时 Codex 还有一条未处理的 P2，在 T8.6 修掉。

## 范围外 / 后续

- 接线版本 `v0.27.0` 已发（T10）。Agent24 升级 lock 与它自己的群聊界面由 Agent24 负责，按 `group-chat-agent24-interface-20261025.md` 对接；Hyphae 没有改 Agent24 仓库。

- Agent24 的 Rust / REST / UI 没有群聊接线；T22 消息 UI 也没做。契约已写（T9.1），但跨设备群聊要先有 Hyphae 的 H-G1/H-G2，Agent24 侧的界面由 Agent24 自己实现。
- 旧 relay 与新客户端的组合没测。
- 上游 nostrlib 竞争修复 PR（NIP-34 事件 `e6561592…`）无回应，上游 10-22 版也没修；仍靠 `iDoris-ai/nostr` fork 的 `replace`。
- #280 留下的 4 个 P2、`--drop-recipient` PTY 用例在当前 main 上重跑，见 `handoff-20261024.md`。
- 两个临时 worktree 已在 T8.8 删除。

## 后续待办（2026-10-27 记录，接线版本之后再做）

- `activating` 群的周期性修复：创建者 daemon 在记录最后一个 accept 与排队 activation 之间崩溃，群停在 `activating`，目前只有重放该 accept 才会修复。
- 跨 HOME 的离线成员与重启场景（目前只覆盖单 HOME 群）。
- 群聊 TUI：真实终端人工验收、真实 relay 的 TUI 端到端；逐人投递状态、重试按钮、邀请/接受；远端明文渲染前剥离 ANSI/控制字符（DM 界面同样没做）。
- `profile show/list/search/discover` 与其余 TUI 的 Go 测试。
- Agent24 升级锁后，更新 `.github/joint-ci-baseline.json` 的 `production_hyphae_sha`（先例 #281）；交接说明里的"待确认"项由 Agent24 核对。
- `/private/tmp` 下约 130 个 `idoris-*` 临时目录（等用户决定）；Claude Docs 状态报告更新。
