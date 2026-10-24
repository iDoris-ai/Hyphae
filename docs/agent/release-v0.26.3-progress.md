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

## 日志

- 2026-10-24：建立文档。`v0.26.2` tag 在 `9c1b9f5a`，无 Release；Agent24 main `f16e65a9` 锁 Hyphae `671c584`。
- 2026-10-24：T1–T7 全部完成。v0.26.3 已发布，Agent24 锁到 `144555ca`（#1193），三条联调和混合版本互通通过，Hyphae joint-ci baseline 已跟进（#281）。main 现在是 `58f378dc`。

## 范围外 / 后续

- Agent24 的 Rust / REST / UI 没有群聊接线；T22 消息 UI（发消息、看历史）也没做。这是下一阶段，不在本计划内。
- 旧 relay 与新客户端的组合没测。
- 上游 nostrlib 竞争修复 PR（NIP-34 事件 `e6561592…`）无回应，上游 10-22 版也没修；仍靠 `iDoris-ai/nostr` fork 的 `replace`。
- #280 留下的 4 个 P2、`--drop-recipient` PTY 用例在当前 main 上重跑，见 `handoff-20261024.md`。
- Agent24 worktree `/Users/jason/Dev/auraai/Agent24-hyphae-lock-v0263` 和 Hyphae worktree `agent-speaker-worktrees/joint-baseline-v0263` 可以删，本会话没清理。
