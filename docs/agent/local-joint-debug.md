# Agent24 × Hyphae 本机联合调试 runner

状态：2026-10-05 的固定旧组合与 2026-10-06 的 merged-source 组合均在本机真实四进程联调 `PASS`。新一轮使用 Agent24 `fc862cf3f765f3e59686e816aea6fa4792f10da2` 构建的 CLI/agent24d，与 production lock 指定的 Hyphae `671c584f9e9eb807a15968e2aa42fd7507e178b8` 二进制/relay；所有源 SHA、lock 和二进制 hash 均显式传入并匹配。Hyphae #121 合并于 `3fc1f02f67d6845fc8f1d08dd3bba44d7b678d6b`，其生产源码与 lock 的 `671c584` 一致。当前 runner 有 25 项单测。两次真实运行各自的固定输入和证据分列记录；不要把旧组合的 hash 套用至新组合。禁止使用其他机器上的 Codex。该 runner 本身不构建源码、不访问 GitHub，也不读取或修改当前用户的 `~/.hyphae`、`~/.agent24`。

本文件仅记录列明源码/二进制 hash 与 production lock 下的 T20 本机 CLI/daemon/relay 联调，不代表 COMM6a 桌面 UI 或 E-M1 整体验收，也不外推至后续 API/UI/依赖升级。Hyphae 的 `671c584..3fc1f02` 生产树相同性仅限这两个 SHA。COMM6a API #678、UI #679 均已合并，但 targeted component tests 16/16 与 typecheck 不等同桌面 UI+真实 relay 验收；后续 UI 与当前项目状态见[2026-10-06 交接快照](handoff-20261006.md)。Hyphae PR #122（文档 head `31c0cd2`）fresh CI run `37463001377` 全绿后已并入 main `8bbaeb8`。旧 head `fb576411` 的 macOS Python timeout-fixture failure 保留为历史记录，详见交接文档；该 failure 根因仍未证实。

## 范围与输入

`scripts/test_agent24_joint.py` 使用 Python 标准库启动四个预构建程序：Hyphae CLI、Agent24 CLI、`agent24d` 和 Hyphae relay。每个二进制都必须给出本机实际文件路径及完整 SHA-256；同时必须给出两仓完整 40 位源码 SHA、Hyphae production lock 和 lock 的 SHA-256、独立输出目录。lock 的 `source_sha` 必须等于传入的 Hyphae SHA，当前平台的 `binaries` 摘要必须等于 Hyphae CLI 预期摘要。任何参数缺失、lock/制品 hash 错误、认证正对照失败或功能断言失败都返回非零，不能降级为 skip。

联调全程使用 runner 自建的临时 `HOME`、relay 数据目录和随机身份/正文。Agent24d 以 `A24_HYPHAE_BIN` 指向本次显式传入的 Hyphae CLI，并设置 `A24_COMM_PASSWORD_STORE=memory`；Agent24 托管 keystore 位于隔离 HOME 的 `.agent24/comm/hyphae-home`。随机密码只经 Hyphae CLI stdin 或 loopback HTTP 请求体传递，不写入 argv、日志或证据。HTTP bearer token 只从隔离 HOME 的 `daemon.json` 读取，用于验证未授权请求收到 401/403、授权请求成功；token 不写入证据。

示例命令（所有路径和摘要需替换成这次本机产物的真实值；不要复制示例值作为基线）：

```sh
python3 scripts/test_agent24_joint.py \
  --hyphae-bin /absolute/path/to/hyphae \
  --expected-hyphae-sha256 <64-hex> \
  --agent24-bin /absolute/path/to/agent24 \
  --expected-agent24-sha256 <64-hex> \
  --agent24d-bin /absolute/path/to/agent24d \
  --expected-agent24d-sha256 <64-hex> \
  --relay-bin /absolute/path/to/hyphae-relay \
  --expected-relay-sha256 <64-hex> \
  --hyphae-sha <40-hex> \
  --agent24-sha <40-hex> \
  --lock /absolute/path/to/hyphae.lock.json \
  --expected-lock-sha256 <64-hex> \
  --output-dir /absolute/path/to/joint-evidence
```

runner 在首次启动及每次重建 Agent24d 后，重新读取当前 `daemon.json` 中的 bearer token/loopback 端口，并发送 `POST /api/v1/comm/unlock`，JSON 为 `{"password": <临时密码>, "remember": false}`。成功必须是 HTTP 200 且 envelope 的 `data.unlocked=true`、`data.remembered=false`；仅 HTTP 404 是 `BLOCKED` 并停止后续依赖步骤，其余状态或响应形状不匹配都是 `FAIL`。密码只存在于请求体，不会写入执行记录或证据。

## 硬断言与阶段

Runner 输出 `PASS <stage>`。所有断言都成功后生成 `result: PASS` 的 `evidence.json` 并返回 0；unlock 返回 404 时生成 `result: BLOCKED`，保留前序证据并以退出码 2 停止。主要门槛为：

1. 四个本机二进制与 production lock 精确匹配；隔离 HOME 中启动真实 relay 和 agent24d。
2. Bearer HTTP 鉴权负向/正向控制，以及 Agent24 CLI 通过真实 daemon API 的正向控制。HTTP 与 CLI relay 配置结果一致。
3. 解锁成功后执行 Agent24 → Hyphae 和 Hyphae → Agent24 双向收发，逐项比对完整正文与相同 `event_id`。
4. `/comm/unlock` 返回 404 时，保留鉴权 HTTP、Agent24 CLI 只读/配置和 Hyphae 侧联调证据后记为 `BLOCKED`，不执行后续依赖步骤。
5. relay 断开时 Agent24 CLI send 仍应退出 0 且 `published_to=0`、`queued_for_retry=true`、`layer=L1`；恢复后 retry 保留原 `event_id`，对端历史恰有一条正文，outbox 中原 ID 不再 pending。
6. 停止接收 daemon 后发送 125 条；恢复后以 history 中 125 个完整 event ID 作为补收条件；重启后等待 process 状态为 running，再发送唯一 sentinel 并有界等待其入史，之后断言 125 条旧 ID 仍各一条且集合只新增 sentinel，不使用固定 sleep 判成功。
7. 切换默认身份触发托管 Hyphae daemon 重启；CLI 直接返回的 data 中 `process.generation` 必须是整数且变化，`process.state` 回到 running，前后 `consecutive_failures` 不变。
8. `memory` password store 在 Agent24d 重启后必须重新解锁；分别验证首次启动与 SIGTERM 重建后获取新 bearer/base、unlock，再启动并等待托管 daemon running。SIGKILL 仅作用于本 runner 持有的 agent24d 进程组。孤儿清理前及每次 TERM/KILL 前核对隔离 HOME pidfile 的 pid/pgid、binary hash 和 `start_marker` 与当前进程出生标记相等；信号后轮询整个 PGID 确认无成员，不以 leader 退出代替整组清理。身份变化则拒绝信号并以 cleanup failure 结束。禁止按名称批量杀进程。

`scripts/test_agent24_joint_test.py` 当前 25 项，只验证 runner 编排的安全边界，包括缺输入/缺文件/hash 错误/重复 lock 字段、positive-control 失败、隔离环境、进程组超时清理以及 SIGTERM/SIGKILL。它不等价于上述真实验收，也不会伪造 relay/HTTP/CLI 成功。

## 2026-10-05 本机真实联调结果

最终证据使用 schema `agent24-hyphae-joint-evidence/1`，运行时间为 `2026-10-05T15:57:43Z` 至 `15:59:16Z`，结果为 `PASS`，且 `failure` 与 `cleanup_failure` 均为空。脱敏证据已由主代理安全归档至原 Hyphae checkout 的 ignored 路径 `build/agent-handoff/20261005/evidence.json`，SHA-256 为 `5b5da8cdaebf6bcddc80f530c8bdcbc971f44df48f9e468e434e54303820a2fa`。不提交该原始证据、临时 HOME、数据库、密码或 bearer token，也不复制临时运行目录。

固定输入如下：

- Hyphae source：`671c584f9e9eb807a15968e2aa42fd7507e178b8`
- Agent24 组合 source：`4e0f255e061c78845fe9b372f5cfca7f2f372048`
- Hyphae CLI SHA-256：`d1171421e91ae62c40374bd00049cd51dd6ac1135b6cd7b31908968b9158df60`
- Agent24 CLI SHA-256：`3b841aa21b7990b6c4bc03e625a950db4c62efa8b9ee0d0b4f2d4f5b87e95125`
- `agent24d` SHA-256：`ce893b402262c3d9400c1409bf5eacef6fbad610b446b0f7a0efe31eb22498a9`
- Hyphae relay SHA-256：`a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef`
- production lock SHA-256：`a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`

通过的硬门包括：未认证 HTTP 为 401、认证与 Agent24 CLI 配置一致；首次及 `agent24d` 重启后 `remember:false` 解锁；Agent24→Hyphae 与 Hyphae→Agent24 的正文和 event ID 一致；relay 断线时 L1 入 outbox、恢复后原 ID 只投递一次；停止接收后发送 125 条，恢复全部补收且重启不重复；配置变化 generation 从 2 变为 3 且 failure counter 保持 0；SIGTERM、SIGKILL 与托管 Hyphae 进程组均按所有权校验完成清理。

同一最终组合上，COMM-5b 真实 ignored T3、dependency allowlist、六类零运行 fixture、默认 ignored 行为，以及 production-lock 匹配的 unlock real-binary test 均通过。runner 编排测试为 25 项通过。

## 2026-10-06 merged-source 本机复验

严格 runner 于 `2026-10-06T12:15:04.209Z` 至 `12:16:20.998Z` 运行并 `PASS`。本次 Agent24 CLI/agent24d 从 #676 合并提交 `fc862cf3f765f3e59686e816aea6fa4792f10da2` 构建；Hyphae CLI 与 relay 从 production lock 所指向的 `671c584f9e9eb807a15968e2aa42fd7507e178b8` 构建。该 lock SHA-256 为 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，其 `source_sha` 和 darwin-arm64 CLI hash 均与实际输入匹配。

本轮四个二进制 hash：Agent24 CLI `c5aa7559c70b3b37362cf1af3290a7e23f50ef554e6e5739e812775070e6e973`、agent24d `3c88466aa7f9d58715aa7adb1d3a50ddf30bc408bc9d3acce2d78cbb12896c5d`、Hyphae CLI `d1171421e91ae62c40374bd00049cd51dd6ac1135b6cd7b31908968b9158df60`、relay `a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef`。十个 stage 全部通过：隔离 HOME/真实 relay、初始及 agent24d 重启后 memory unlock、HTTP/CLI 正对照、双向 event_id/正文、断线后原 ID 恰投递一次、daemon 停机期间 125 条恢复且重启新增 0 条、配置重启 generation，以及 SIGTERM/SIGKILL/整个 PGID 清理。总体 `failure` 与 `cleanup_failure` 均为空。

脱敏证据由 root 归档于原 Hyphae checkout 的 ignored 文件 `build/agent-handoff/20261006/evidence.json`，SHA-256 为 `b9afc7c558d86fe6e2ef9cc6a282bc7df31e8118ec5a03f56839badfcdd11d45`。该 runner 使用临时隔离 HOME；原始临时目录不提交。

## 验收证据格式

成功、FAIL 和 BLOCKED 都会尝试在输出目录新建的 `agent24-joint-<UTC 时间>-<随机后缀>/evidence.json` 保存脱敏证据，字段包含：

- `schema`: `agent24-hyphae-joint-evidence/1`
- `hyphae_sha`、`agent24_sha`：两仓实际源码 SHA
- `binaries`：四个实际二进制的 SHA-256
- `production_lock_sha256`、`platform`、`result`（`PASS`、`FAIL` 或 `BLOCKED`）
- `started_at`、`ended_at`、`result`、`failure`/`blocked_at_stage`、`cleanup_failure`
- `assertions`：每个验收 stage，以及双向/retry event ID、HTTP 状态码、125 条数量和 generation
- `executions`：阶段、脱敏命令、exit/status、起止时间、输出字节数、cleanup 状态；stdout/stderr 和 HTTP 响应正文不保存
- `cleanup`：各受控 relay、agent24d、Hyphae PID/PGID 清理结果。受控进程启动/停止也记录脱敏命令、阶段、退出码和起止时间；不会保存 stderr 内容。

证据不包含密码、bearer token、消息正文、keystore、数据库、relay 内容或原始 stderr。失败命令保留脱敏参数形状、退出码、起止时间和子进程清理状态；失败本身写 `result: FAIL`，仅 unlock 路由缺失（404）写 `result: BLOCKED`。任何 cleanup 失败都使总体结果 FAIL，不能吞掉；PID 出生标记不匹配时拒绝 kill 并留下清理失败记录。不要把临时 HOME 或其数据库复制到共享仓库。通过证据应另行安全归档，文档链接只需指向本文件及已审阅的证据摘要。

## 当前困难与 Agent24 需要调整的事项

本轮真实联调已通过，不再存在阻止本机 Agent24×Hyphae 基础通信闭环的已知 blocker。过程中发现并关闭了三类问题：旧 Agent24 lock 与 Hyphae main 不一致；memory password store 缺少安全解锁入口；两个验收夹具分别存在本地 counter readiness 竞态和重复预置联系人。首轮 runner 失败证据保留为 `/tmp/agent24-joint-evidence-20261005/agent24-joint-20261005T155435Z-777881f9/evidence.json`，其 cleanup 全部完成；修复后以全新隔离 HOME 重跑通过。

请 Agent24 仓库在合并与后续优化中保持这些稳定性要求：

- 确保 `POST /api/v1/comm/unlock` 持续遵循已冻结契约：bearer 鉴权、`{password, remember:false}` 请求、`data.{unlocked,remembered}` 成功字段；memory backend 重启后可用临时密码重新登记，且后续 CLI/daemon 共用该账户密码。
- 明确 `comm daemon status` 的 generation 来源与生命周期：默认身份/relay 配置变化后应重启 Hyphae daemon 并返回可比较的新 generation；daemon stop/start 也需可观测。
- 将 `agent24d` 的 `A24_HYPHAE_BIN` 与 password store 配置从启动环境稳定传给真实服务；运行时必须校验传入 CLI 对应 production lock/hash。
- 保持断线发送的 CLI 成功 envelope 稳定为 exit 0，并持续提供 `event_id`、`published_to`、`queued_for_retry` 和 `layer` 字段，确保 runner 能用原 ID 调用 `comm outbox retry`。
- 保持 daemon 入站历史按 event ID 持久去重；离线 125 条批次必须可在启动后补收，正常重启不得产生新历史行或重复消息效果。

建议 Agent24 后续再做两项非阻塞优化：把本次真实 runner 纳入可重复的本机/CI 验收入口，并为 counter/provider fixture 提供统一 readiness helper，避免各测试重复实现启动同步。production lock、unlock 与 COMM-5b 门现随 #676 合并；Hyphae runner 随 #121 合并。上文 PASS 仍只对应列明的合并前固定输入，合并后的 Agent24 二进制组合需要匹配新 lock/hash 后再做真实联调。
