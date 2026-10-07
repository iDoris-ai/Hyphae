# M1 TUI 离线队列与加密群聊验收工装

状态：本文件和 runner 是测试工装，不代表产品功能验收通过。离线 runner 必须显式 `--execute`；不带此选项会返回 `77` / `expected_gap`，因此不会进入默认测试链。三人加密群聊目前仅输出断言骨架，返回 `77` / `expected_gap`，不作假通过。

## 可复用入口及其边界

- `tests/cli_offline_retry_e2e_test.go`：真实 CLI/relay、临时 HOME、NIP-44 加密 outbox、relay 同数据目录重启、显式 `storage outbox retry`、event ID 与 history 去重。它不启动 PTY，也不验证 TUI 自主重试。
- `docs/agent/m1-tui-live-inbox-acceptance-20261007.md`（#135）：记录了三个隔离 HOME、真实 CLI/TUI PTY、本地持久 relay、无外部 daemon 和 relay 重启后 inbox recovery 的验收经验；当时未有可重跑 PTY 驱动，且离线发送/outbox 不在 #135 范围。
- `test_tui_e2e.sh`：仅测试 help 命令并提示人工交互；它依赖 `./bin/hyphae` 和当前 HOME，不作为隔离验收器。
- `test_group_e2e.sh`：依赖公网 relay 和共享 HOME，发送未加密 DM 并吞掉错误，不构成加密群聊证据。
- 群聊协议/状态设计参考了本机 sibling worktree `codex/m1-group-design-20261007`（commit `d0e2d3b`，`docs/agent/m1-group-chat-design-20261007.md`；该文档未包含在本验收工装基线上）：规定固定 roster、显式邀请接受/激活、每收件人独立 NIP-44 event、稳定 event ID 重试、单 inbox router 与幂等存储；并明确真实 CLI/TUI 三用户链路未完成。

## 离线 PTY runner

```sh
python3 scripts/tui_offline_pty_acceptance.py
python3 scripts/tui_offline_pty_acceptance.py --group-skeleton
```

上面两条现在均应显示 `expected_gap` 并以 77 退出。等 TUI outbox 功能分支实现并同步 UX 后，显式运行：

```sh
python3 scripts/tui_offline_pty_acceptance.py \
  --execute --bin-dir /tmp/hyphae-tui-offline-candidate \
  --source-commit <40-hex-clean-source-commit> \
  --expected-cli-sha256 <64-hex> --expected-relay-sha256 <64-hex> \
  --evidence /tmp/hyphae-tui-offline-evidence.json
```

默认从当前 runner worktree 构建 `./cmd/hyphae` 与 `./cmd/hyphae-relay`；对正式候选二进制使用 `--bin-dir`，并同时传入 exact source commit 及两个预期二进制 SHA-256，runner 会在启动产品前复核 hash 并把它们记入脱敏摘要。运行目录必须是本机可信源码/二进制。脚本只使用两个 0700 临时 HOME 和一个临时 relay data dir，relay 仅绑定随机 loopback 端口。

执行序列：使用真实命令 `hyphae tui chat --with <contact> --relay <loopback-url>` 打开 sender/receiver 对话 PTY → 停 relay → 输入唯一合成正文 → 要求 sender UI 显示 `Outbox: queued for retry • <12位event ID> (not delivered; awaiting relay ACK)` → 核对 durable outbox 中仅有加密 event、没有唯一正文 → 退出并重开 sender TUI，要求同 event ID 的 queued 状态恢复 → 在同端口恢复同 data dir relay → 等 sender 自动 retry 并显示 `Outbox: relay accepted • <同event ID> (recipient delivery/read not confirmed)` → 等接收 TUI 实时显示唯一正文 → SQLite 双方历史都恰好一行且 event ID 相同，receiver 当前画面恰好显示一次正文；最后扫描本地 relay data 文件，确认其中无唯一正文明文。

这些状态文案是 adapter 当前依据 TUI offline worker 的接口同步版本；若 UX 文案变化，先同步更新 adapter，不得放宽为“任意非空输出”。发送状态只证明 relay 接受，不证明用户已读/送达。PTY 内容仅保留在内存中，summary 不写正文、npub/nsec、密文、raw relay/PTY 日志；event ID 仅写截断 SHA-256。失败输出刻意使用通用阶段信息，不透传 CLI stderr。若指定 evidence 文件，必须位于调用者 HOME 之外且目标不存在，防止意外覆盖或把验收材料写回 production HOME。

runner 不启动 daemon，仅清理它自己创建的 PTY 子进程和 relay 子进程；HOME、relay data、Go build cache 都在临时目录，退出后整体移除。它不得用于用户默认 HOME，不会扫描或停止其他服务。`--bin-dir` 场景由调用者负责确认二进制来源；验收摘要不声称代码来源或发布状态。

### 本机真实链路结果（2026-10-07）

修复候选在本机干净 worktree `codex/m1-tui-offline-20261007` / source commit `047e3e4e5b34128592f4d90cb7ca0e79cdfe4edb` 上运行，PTY runner commit 后续会由 offline PR cherry-pick。候选二进制及验收输入为：

- `hyphae` SHA-256：`ba68bdb81dfc4785cf2e443edf6698a0ff7fb42145d29c92d8898500e1b28b90`
- `hyphae-relay` SHA-256：`31ec2b39bdcac4d73ec84e16d2a598787c1844823ac559ea41e6e4c52b673f07`
- 运行命令：`python3 scripts/tui_offline_pty_acceptance.py --execute --bin-dir /tmp/hyphae-tui-offline-candidate.OpNjoE --source-commit 047e3e4e5b34128592f4d90cb7ca0e79cdfe4edb --expected-cli-sha256 ba68bdb81dfc4785cf2e443edf6698a0ff7fb42145d29c92d8898500e1b28b90 --expected-relay-sha256 31ec2b39bdcac4d73ec84e16d2a598787c1844823ac559ea41e6e4c52b673f07 --evidence /tmp/hyphae-tui-offline-pty-047e3e4-attempt1.json`
- 结果：`pass`；sender/receiver 两个隔离 HOME；断线前两边 inbox 已连接，relay 停止后两边均观察到 reconnecting；sender queued 状态明确未送达，TUI 重开后同 ID 队列恢复；同一 data dir relay 恢复后无 daemon 自动 retry 并获 ACK；签名 event 与当前 sender identity、唯一 receiver `p` tag、relay 存储事件匹配；relay 精确 ID 历史命中 1 条，双方历史行各 1 条，receiver UI 可见一次。
- 脱敏摘要文件：`/tmp/hyphae-tui-offline-pty-047e3e4-attempt1.json`。该文件只含 source/binary hash、event ID、身份/路由摘要 hash、状态布尔值与计数；无正文、私钥、密文或 raw PTY/relay 日志。
- 工装自测：`python3 scripts/test_tui_offline_pty_acceptance.py`，5 项通过。实现方报告该冻结产品源的 `go test ./...`、focused/race 与迟发布回归测试通过；这些不是本 PTY runner 自己执行的命令。

此前候选 `1602f182a8b1927c50d6b9f8ca6326bedfda34ad` 的 attempt2–5 未通过收件 TUI。attempt1 是本工具将命令写成 `hyphae chat` 的 harness 错误（CLI 实际入口是 `hyphae tui chat`），且 PTY parser 未屏蔽 OSC 查询，均已修正。随后诊断证明离线 event 已被 relay 按 exact ID 存储且签名/发送者/收件人正确；receiver watcher 经 `Relay reconnecting → Connected`，但 event 发布在历史 Walk 之后、重连 live filter 的 `Since=now-1` 又排除了该旧 `CreatedAt`，因此 TUI history 为 0。相同 receiver identity 的显式 CLI inbox 随后可获取并解密该 event。实现方移除 live subscription 的 `Since` 边界后，上述冻结候选全链通过。失败摘要保存在 `/tmp/hyphae-tui-offline-pty-1602f182-attempt3.json`、`attempt4.json` 和 `attempt5.json`，均不含正文/密钥/raw event。

## 三人加密群聊：断言骨架 / 当前 expected gap

执行 `python3 scripts/tui_offline_pty_acceptance.py --group-skeleton` 可机读输出下列断言清单，并以 77 返回。当前没有三用户群 UI runner，也不声称此组测试已执行：

1. Alice、Bob、Carol 各自使用独立临时 HOME；无共享 identity/keystore。
2. Alice 建立固定三人 roster 并分别邀请 Bob/Carol；未显式接受前，二人均为 pending，不能收发/看到群消息。
3. 两份有效接受回执齐备后才激活；拒绝或超时不得自动缩小 roster。
4. 三人轮流发送合成唯一正文；每位 active 成员 UI 与 durable 群历史均恰好显示/存储一次。
5. 单收件人 fanout 使用不同 event ID 和密文；断线重试必须复用原 event ID，relay 捕获内容不得含正文、群名或 roster。
6. relay 停止/以相同 data dir 重启后，不依赖 daemon 自动恢复；捕获方不重复显示/存储。
7. 私聊 JSON 不得误判成群信封；未知 sender、错误 group/roster/hash、伪造接受或未知版本应失败关闭，且不写 DM/群历史。

实现这些断言前，需先有可用的 CLI invitation/accept/group-send 与 TUI 群窗口；缺任意命令或界面时必须保留 `expected_gap`，不得以普通 DM、明文发送或仅单元测试代替真实群聊验收。

## 工装自测

```sh
python3 scripts/test_tui_offline_pty_acceptance.py
```

该自测只验证 ANSI 屏幕解析及 opt-in/gap 行为，不启动产品、relay、daemon 或任何用户 HOME。
