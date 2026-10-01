# Agent24 × Hyphae

## 2026-10-01 接线更新

### 第一轮联调 F1：历史查询与补收

已阅读 [#601 第一轮结果](https://github.com/iDoris-ai/Agent24/pull/601#issuecomment-5924511217) 与 [#620](https://github.com/iDoris-ai/Agent24/pull/620) `a9c4bd634504bcaf9491019ab4e5b6a8c680872c` 的记录，并对照 Hyphae `a4aa606` 源码确认：`history inbox` 仅查询本地历史，不主动拉取 relay。默认解密的单次补收使用 `agent inbox --as <nick> --password-stdin`；`--decrypt` 默认 true。口令由 stdin 传递，成功持久化后才会在 history 出现新消息。这个顺序同样适用于未加密身份，区别在于解锁需求。

托管模式由 Agent24 监管的 Hyphae daemon 在启动时及 watch interval 周期内直接执行 `watchInbox` / `relayquery.Walk`，解密并持久化消息；它不周期性启动 `agent inbox` 子进程。单次 inbox 有 limit，不能代替完整补收。daemon 接管后无需手动 inbox，但断线、查询/解密/落库失败仍可能延迟可见性；进程存活和空历史不证明同步完成。显式关闭 notify/auto-reply 的约定继续有效。

#620 报告了生产 lock 校验、加密创建、发送、同 event_id 断线重试和错误路径的实际测试；尚未覆盖 daemon/REST/UI、125 条积压、zero-run 和秘密扫描。建议下一轮补充 daemon 补收后 history 可见、重启去重、断线恢复与失败状态、实际 run/model/module 零调用计数。本文是 Hyphae 源码核验，不替代同 hash 的独立真实 relay 复测。针对 #601 的确认评论已准备；评论工具在执行前返回 `MCP tool call requires approval, but approval policy is never`，尚未发布。

### #620 验收工具源码评审

固定 [joint_round1.rs @a9c4bd63](https://github.com/iDoris-ai/Agent24/blob/a9c4bd634504bcaf9491019ab4e5b6a8c680872c/rust/crates/agent24-comm/tests/joint_round1.rs)：本轮实测记录可保留，但用于后续验收前应补齐以下断言与运行保障，由 Agent24 侧落实。

- L207/L214：显式运行 ignored 测试时，缺少任一真实二进制变量会直接 return，测试结果仍为 PASS。应让指定的联合验收调用失败，日常测试可继续用 ignore 排除。
- L260/L561：relay 启动后，仅正常路径 L495/L759 调用 stop_child。readiness 或后续断言 panic 会跳过清理；应由 RAII guard 在异常退出时 kill 并 wait，CLI 子进程也应受控。
- L131：B 侧 wait_with_output 无期限；应给 CLI 和管道写入设置有界超时，超时后回收并报告失败。
- L458/L748：拉取前非空、非法发送错误分类不符只写入 findings，最终仍可通过。冻结后的验收要求应严格断言；报告中的 B encrypted=true、重试 sent=true、正文与 ID 一致也应各有断言。本轮实际脚本只验证 A→B，不能将结果概括为已验证双向发送。

Hyphae 侧独立复测工具正在新 worktree 实现：读取生产 lock、校验并复制制品、隔离双方 HOME、严格双向和重试断言、超时及异常清理。它不调用 Rust Runner，不替代 Agent24 接线验收；未执行真实 relay 前不记录运行通过。

### 后续实时更新：#620 合并、#621/#622 与第二轮准备

Agent24 main 已推进到 `65a5c5115482522539496bc5c5cd8cdbec9a2f6e`，对应 [main CI 36821229359](https://github.com/iDoris-ai/Agent24/actions/runs/36821229359) 通过。[#620](https://github.com/iDoris-ai/Agent24/pull/620) 已在 `b00b51d7808df46519931b036514f051e3328467` 合并：文档已删口令夹具原文，拉取前空历史和非法发送错误码已改为严格断言。前节针对 `a9c4bd63` 的这两项发现已解决；缺制品变量仍返回、B 无超时、relay 异常清理及双向范围仍需按最新源码补齐。F1 的 history/daemon 表述仍建议改为本节开头确认的精确定义。

[#621 COMM-1b](https://github.com/iDoris-ai/Agent24/pull/621) head `f6d055ff5e75df00d29d03913430e83f5b172acb` 当前 OPEN，暂无正式 review；七项检查通过。其 [Hyphae lock verify 36815881473](https://github.com/iDoris-ai/Agent24/actions/runs/36815881473) 真实执行锁定源码、Go 配方与 Linux hash 比对并通过。它已补写锁及凭据接口的候选实现，尚未合入；不能继续把“没有 Linux 构建/hash CI”作为当前候选缺口，也不能把它算作已进 main。

[#622 COMM-2a](https://github.com/iDoris-ai/Agent24/pull/622) head `13604363992a97ba82d11d6e20447f3e4f504584` 为 #621 分支上的草稿，已有正式 `agent24 comm identity/contact/relay` 和 REST 接线。旧 main 缺 CLI 的观察继续成立，但候选已提供实现；#621 合入后再 rebase/change base、核对差异及审批。

建议对应仓库现在基于 #622 开发 COMM-4a 草稿，COMM-3 收发路由可并行。合并仍按 621→622→各独立后续 PR；避免把整条依赖链作为一个大 PR。COMM-5b 的 zero-run 依赖 COMM-3 和 COMM-4a，两项必须一起就绪，不能仅托管 daemon 就宣告第二轮通过。当前旧 lock 可用于基础监管实现；新 G2 流和命令必须等 Hyphae 独立改动合入、更新 source/hash 后采用。

COMM-4a 的具体接点：#622 `CommState::ready` 当前共享 runner/password_store/home，监管器应复用同一状态实例；路由构建只发生一次，stop/关机需要在 agent24d 中保留监管句柄。不能为 daemon 生命周期一直持有 keystore 写锁；identity/relay 变化需协调停机/重启，并保留相同通信 HOME。启动前读取配置 relay 和默认身份，禁止默认公共 relay 回落；固定关闭 notify/auto-reply。当前基线只报告 unknown/incomplete；G2 消费端另收口 EOF、generation 与失败状态。#622 `comm_routes.rs` 仍留有过时的路由碰撞说明，但同 PR 已修改 RESERVED_KERNEL_SEGMENTS；建议同步文档，并落实 COMM-0 要求的相对二进制路径拒绝。

第二轮证据：实际 `agent24 comm` / REST 驱动 daemon 启停与配置重启；入站可在 history 读取；断线、进程退出/kill、错误口令与孤儿 PID 复用有明确状态；六类入站的 run/model/module 计数为零，正对照实际增加。125 条与重启零新增继续作为完整 CLI 联调门槛保留。Hyphae 的同 hash 独立复测及 G2 runtime 均未最终验收，不记通过。

已基于这些新事实再次准备并尝试发布 #601 回复；工具仍在执行前拒绝 `MCP tool call requires approval, but approval policy is never`。完整回复已留存于本地交付目录，尚未送达，不能将准备文档记为已完成对仓沟通。原七项及 G2 的代码交付继续推进。

### 固定旧版本代码与交付边界

Hyphae 当前固定 main 为 `a4aa606eb81d5c040d94c51cdf94553e646d8674`。[Agent24 COMM-0 #612](https://github.com/iDoris-ai/Agent24/pull/612) 已合并，采用统一 Rust 通信服务和 `/api/v1/comm/*`，CLI/UI 共用配置与状态；[COMM-1a #614](https://github.com/iDoris-ai/Agent24/pull/614) 已在 `77655f48` 合并，最终双平台 Rust CI 全绿。其 runner 和环境扫描修复已进入 main；CI 尚未构建锁定版本 Hyphae，真实二进制测试仍可跳过，这部分交付仍待补齐。

Hyphae 已核验 [运行反馈](https://github.com/iDoris-ai/Agent24/pull/601#issuecomment-5923235533)。G7 会话历史 JSON、G9 文件正文已本地实现并通过专项测试，G8 keystore 完整并发写保护及专项/race 回归已通过；尚未发布，具体契约与验收见 [联调缺口](../agent/em1-comm-followups.md)。daemon 互斥 `6765d1d` 已通过特殊锁路径、专项/race 与 Linux 编译；只读口令校验 `7fa3e7bc`、存储信息 JSON `7dadc7a9`、联系人输入错误 `574f049b` 已本地提交并通过对应专项/race。补收状态按 COMM-0 G2 请求的 JSON-lines [设计](../agent/em1-daemon-status-candidate.md) 继续实现；stdout 仅完整状态信封，诊断在 stderr，未提供状态文件替代。消费端仍待对应仓库实现与验收。不要提前调用尚未发布的命令。

当前 Agent24 main `c9f5f9cab1c208b09f7ebf9d13a3e1481adcaf12` 的启动入口已默认停止 F4b 入站执行分派；直接 `InboundBridge.handle` 仍保留旧执行逻辑。联合验收须从正式入口验证普通消息、answer、未知版本的 runs/模型/模块计数为零，不能仅根据配置或源码判断通过。

Agent24 提案 [#601](https://github.com/iDoris-ai/Agent24/pull/601) 在 `67ddbce` 已获批准和全绿 CI；用户已授权合并，本会话工具拒绝了合并调用。勘误 `9bfb0df` 尚在独立本地 worktree，待 #601 合入后另提文档 PR。Hyphae 接口变化合入后须更新 Agent24 的源码/构建配方/hash lock，再固定联合测试版本。只读口令命令实际为 `identity check-password --password-stdin`；新锁采用时需同步 G6 的非法联系人输入断言由 exit 4 改为 1。当前 `cmd/hyphae/main.go` 已有可注入的 `main.version`；若 release 配方加入版本 ldflags，必须同时更新 binary hash，不能沿用当前无版本注入的配方 hash。

### 当前实现出口与对仓下一批

再次按 `c9f5f9c` 核对：`agent24-comm/src/lib.rs` 只公开 binary/password/runner；`HyphaeRunner` 只有 command/run，没有 COMM-0 中的 `KeystoreWriteLock`、`keystore_lock` 或 `run_keystore_write`。`agent24d/Cargo.toml` 尚未依赖该 crate，正式 CLI 的 Command 枚举也没有 Comm。故 #614 只交付运行器的基础层，不能记为正式 CLI 已接通。Hyphae G8 的文件事务保护覆盖本仓写入，但不能替代 Agent24 导入流程的停机/调用序列监管。

| 对仓顺序 | 必须交付 | 验收出口 |
|---|---|---|
| COMM-1a 收尾 | 锁定源码/Go/平台/完整配方的真实构建/hash CI；补写调用与 import 的监管接口 | 未设置真实二进制不得静默跳过；篡改/hash 不一致失败；并发写不丢身份 |
| COMM-1b → 2a | 凭据存储、Pending→Salt；接入 agent24d 和实际 `agent24 comm` identity/contact/relay | 实际 CLI create/list/use/contact/relay 往返；错误密码无秘密输出；未配置 relay 不回落公共网络 |
| COMM-2b、3、4a | 停机导入、send/history/outbox、daemon 监管；显式关闭 auto-reply/notify | 源数据不变；原 event_id 重试；错误部分 data 保留；取消/退出/恢复和配置重启可验证 |
| COMM-4b、5b | 消费 G2 JSON-lines；进程/probe/扫描三种证据分开；zero-run 正负对照 | 失败/EOF 不显示健康；普通消息、查询、通知和回执不启动 run；计数正对照实际增加 |
| COMM-6 → 7 | CLI/UI 共用同一服务、配置和状态，真实双仓联调 | UI 不另起 Nostr 栈；入队/relay 接受/未确认分别呈现；125 条积压及重启零新增 |

Hyphae 七项及 G2 合入并固定新制品之后再启用新命令；上述表是由用户推动的对应仓库交付，不表示已派本会话 Luna 修改 Agent24。后续 T01-E、高层执行和四仓验收仍按原依赖推进。

以下是 2026-09-30 的固定版本核查与约定，旧待审描述不代表当前队列。

状态：Hyphae 侧协作提案，待 Agent24 确认。2026-09-30 固定的远端 main 审阅与隔离验证基线：Agent24 `7009294834b2251beac438f3190aae073742c5dd`。本地用户 checkout 未更新；源码核查与实际模块挂载在独立 detached worktree 完成。

## 已有接口

`packages/nostr-bridge/src/speaker.ts` 已通过子进程调用通信 CLI；`config.ts` 支持 `A24_SPEAKER_BIN`，默认仍为 `agent-speaker`。正式 Agent24 CLI 位于 `rust/apps/agent24-cli/src/main.rs`，当前没有统一通信子命令。

继续使用已存在的 `identity`、`profile publish/discover`、`agent msg`、`history inbox --as`、`storage outbox`、`daemon` 能力及 `--json` 输出。入站必须保留完整发送者公钥和事件 ID，禁止依赖显示用截断名称。

### 本轮可接线接口

这些接口分批进入 main；下表同时含已合并项和仅在组合分支验收的项，正式接线按后文交付门槛选择。历史完整组合开发基线为 `integration/em1-cli-acceptance` / `f46744aa519937ed38832e75395591b7200d9520`；Go 1.25.0、隔离 HOME 下，全量 integration、vet、构建和 smoke 脚本均通过。正式打包版本须在合并后重新固定。它是依赖组合分支，各项修改仍由单独小 PR 评审。

| 能力 | Hyphae 接口与状态 |
|---|---|
| 身份/联系人 | PR [#45](https://github.com/iDoris-ai/Hyphae/pull/45)：create/list/use、contact add/list 的 JSON 已验收；只输出公开身份字段 |
| relay 配置 | PR [#50](https://github.com/iDoris-ai/Hyphae/pull/50)：`relay set --relay URL` 可重复、完整替换；`relay list` 返回 relays/source；`relay info [URL] --timeout 5` 返回 url/connected |
| 配置优先级 | 显式 --relay > `~/.hyphae/relays.json` > 既有默认；坏配置报错，不静默换公共 relay。已入队事件保持原地址 |
| 消息可靠性 | 重试事务 #48、历史明文 #49、发布前可靠入队 [#54](https://github.com/iDoris-ai/Hyphae/pull/54) 已验收；`published_to=0` 且 `queued_for_retry=true` 表示已提交待发 |
| 待发管理 | [#53](https://github.com/iDoris-ai/Hyphae/pull/53)：list 为安全数组，clear 返回 removed/remaining；[#56](https://github.com/iDoris-ai/Hyphae/pull/56)：retry JSON 已验收 |
| 收件 | 原子首次收件 #51、daemon 接线 [#55](https://github.com/iDoris-ai/Hyphae/pull/55)、inbox 单次查询 #58、分页 #60/#65 已验收；[#66](https://github.com/iDoris-ai/Hyphae/pull/66) 以真实二进制验证 125 条离线积压和重启去重 |
| 加密身份 | [#63](https://github.com/iDoris-ai/Hyphae/pull/63)：msg/inbox/daemon 的 `--password-stdin` 已验收；[#64](https://github.com/iDoris-ai/Hyphae/pull/64)：创建身份的 stdin 已验收，支持首次加密创建与向加密库追加 |
| 生命周期 | [#59](https://github.com/iDoris-ai/Hyphae/pull/59)：SIGINT/SIGTERM 取消当前网络等待；[#61](https://github.com/iDoris-ai/Hyphae/pull/61) 覆盖真实二进制退出与离线重试 |
| 空历史 | [#67](https://github.com/iDoris-ai/Hyphae/pull/67)：已有身份但没有消息时，history stats 返回四项零值；尚无身份时应先创建或选择身份 |

表内单次管理/收发命令的机器模式成功在 stdout 输出一份 `{"ok":true,"data":...}`；错误在 stderr 输出错误信封，退出码沿用 1 用户输入、2 网络、3 身份解锁、4 其他、5 写冲突。UI 不解析人工提示文字。`connected=true` 只表示一次 WebSocket 握手成功，不能当持续在线、已订阅或已送达。各项字段及补收门槛见规划 PR [#39](https://github.com/iDoris-ai/Hyphae/pull/39) 的 CLI 通信契约。

daemon 是长驻进程，当前输出运行日志，并未提供 JSON 消息流或健康状态 API。Agent24 管理其启动/退出和日志展示，通过单次 history JSON 查询获取持久化消息。进程存在不等于 relay 连通，relay 探测成功也不等于全部历史同步完成；暂不生成这些未提供证据的状态。

发送错误的信封可含 `data`：沿用 event_id、published_to、queued_for_retry，并增加 history_stored、superseded、queue_state_unknown。Agent24 即使收到非零退出码也要读取这些字段；relay 已接受而本地记账失败时，按原 event_id 核对，不创建新消息自动重发。队列状态未知时 UI 显示待核对。

配置在命令或 daemon 启动时解析。`relay set` 或默认身份变化不会自动重配已运行的 daemon；Agent24 应由同一进程管理入口重新启动相应实例，并继续保留旧待发记录的 relay 地址。`agent inbox` 是有 limit 的单次 relay 查询；持续收件由 daemon 写入本地历史，UI 从 history 读取，不把一次 inbox 返回当成所有历史已同步。

### 主线交付与接线次序（2026-09-30）

2026-09-30 本轮核对，Hyphae main `1948aadc551e360176711f9c50172ed6edccd253` 已包含身份/联系人 JSON、relay 配置、outbox list/clear/retry JSON、发送前持久化、可靠自动回复、inbox 查询和 daemon 首次收件登记、历史分页补收、取消恢复，以及 msg/inbox/daemon/create 的 stdin 凭据通道；最低构建版本为 Go 1.26。#66/#67 已合并，固定 main 的默认/integration 全量测试、vet、build 和 smoke 通过，三个真实 relay 用例实际执行；详情见 [验收记录](../agent/em1-cli-acceptance.md)。

本机已验证的 macOS arm64 二进制使用 Go 1.27.1，SHA-256 为 `a7bb4a83b5d6be0a939a4cd92a853a2672f97012c48d704a9a3a718b9e6d806b`，尚未安装到生产环境。其他平台按同一固定源码构建并记录自身 hash，不把本机 hash 当跨平台产物摘要。

Agent24 现在可用上述固定 main 开发第 1、2 项适配器，并用实际 JSON 建立错误/公开字段契约测试；Hyphae 侧完整 CLI 版本已经验收。管理 UI 可先做服务接口与状态设计，整段验收仍等 Agent24 CLI 接线。该本仓结果不替代 Agent24 的实际 subprocess、配置共享、普通入站不启动 run 和 UI 验收。

上述 Agent24 远端版本的 Rust CLI 仍未提供通信命令；bridge 仍为 `f4/1`、内存 seen 集合和默认 `agent-speaker` 二进制。`agent24-models/src/router.rs` 尚无 `IDORIS_URL`/`idoris-local`/`idoris-any` 接线。它们是当前实现缺口，分别由 T20 和 T10/T11 推进，不能因 iDoris 自身服务已就绪而记为已完成。

本轮从早期审阅基线 `879d77e` 增量核查到 `7009294`：语音面板安全修复与附着模块修复，通信 CLI、基础消息 UI 和 iDoris 接线结论没有变化。Rust CLI 仍只有 Chat/Models/Service/Daemon/Tui/Os/Mcp；桌面 Chat 调用本地 `/api/v1/chat`，尚无 Nostr 联系人、relay 或收件管理。bridge 的白名单限制和现有 run 审批不能替代新协议的授权绑定，内存 seen 也不能证明跨重启执行去重。

模型路由仍按 Local/Lora/Remote 与 Any/LocalOnly 选择自身 provider；未接通 iDoris 隐私请求头、实际落点响应头及预算核销。自身 loopback 地址不能证明未来 iDoris 的实际模型落点；provider 缺 usage 时默认零值、`cost_usd=0` 也不能作为实际预算结算证据。对应源码固定在 [CLI](https://github.com/iDoris-ai/Agent24/blob/7009294834b2251beac438f3190aae073742c5dd/rust/apps/agent24-cli/src/main.rs)、[入站 bridge](https://github.com/iDoris-ai/Agent24/blob/7009294834b2251beac438f3190aae073742c5dd/packages/nostr-bridge/src/inbound.ts) 和 [模型 router](https://github.com/iDoris-ai/Agent24/blob/7009294834b2251beac438f3190aae073742c5dd/rust/crates/agent24-models/src/router.rs)。

真实 Sin90 外部进程挂载、API 代理和事件转发已在本基线选定黑盒测试中通过，证据见 [Sin90 协作文档](Sin90.md)。该基础机制验证不解除 T12/T13 的授权、停用及恢复门槛。

Agent24 可按下表准备独立小 PR，由对应仓库推进并回填实现链接：

| 顺序 | Agent24 交付 | 验收门槛 |
|---|---|---|
| 1 | 统一 CLI 适配器、二进制定位、身份/联系人/relay 管理 | 用参数数组调用；stdout/stderr 分开处理；错误码和公开字段可验证；配置旧名称仍可用 |
| 2 | 发送、history、outbox 与 daemon 生命周期 | 使用固定的完整 Hyphae CLI 版本；断线重试保留 event_id；重启补收；普通消息不启动 run |
| 3 | 管理 UI，再接消息/历史/待发 UI | 与 CLI 共用服务、配置和状态；入队、relay 接受、对端回执分别展示 |
| 4 | 高层执行协议 | 等 T01 契约冻结；授权、执行登记和结果回执分别持久化；四仓联调后验收 |

每项回填 Agent24 PR、双方 commit、使用的二进制版本和验收命令。T20、T21/T22、T19 分别记录 CLI、UI、四仓闭环结果；一个阶段通过不替代后续阶段。

### 调用与 UI 状态映射

统一以进程参数数组调用固定版本的 Hyphae，避免拼接 shell。加密 msg/inbox/daemon 使用 `--password-stdin`，通过专用 stdin 写入密码后关闭管道；不把密码写进参数、日志或持久配置。上限 4096 字节，只移除一组尾随 LF/CRLF，保留密码空格。缺凭据/错误密码返回身份错误，UI 提示解锁后再操作。`inbox --decrypt=false` 不读密码，也不产生解密后的历史。

上述解锁支持仅覆盖列出的命令。当前 `profile publish` 的 `--password-stdin` 是本 PR 候选；合入并按固定版本验收后，Agent24 才可用加密身份 headless 发布现有 profile。它仍是 kind 30078 的公开资料发布，不代表 T08 新 behavior 协议或跨仓高层契约已完成。基础通信阶段不要为了注册成功改建未加密身份。

| CLI 事实 | UI 可显示的状态/动作 |
|---|---|
| 发送 `published_to > 0`，或 retry `sent=true` | relay 已接受；没有对端回执时不显示已送达 |
| 发送 `queued_for_retry=true`，或 retry `queued=true` | 已保存待发；显示重试次数与原 relay |
| `queue_state_unknown=true` | 待核对；保留 event_id，不生成新消息盲目重发 |
| `superseded=true` / 退出码 5 | 状态已被并发操作修改；重新读取 outbox |
| retry `history_stored=false` | 本次未确认写入历史；不能推断旧历史不存在 |
| 清理 outbox | 先展示范围和数量，经用户确认再传 `--yes`；清理不撤回 relay 已接收事件 |

`storage outbox retry --id EVENT_ID` 返回 event_id/attempted/sent/queued/marked_failed/history_stored/superseded/queue_state_unknown。全部 relay 失败但重试结果可靠保存时，命令仍可成功，必须检查 sent/queued。`clear --failed` 还可搭配 `--min-failures`，以本次读取的候选集合清理，不删除确认期间新加入的条目。

## 双方分工

- Hyphae：保持 CLI 参数、JSON/退出码、Nostr 事件兼容；修复存储与可靠投递；提供可构建、固定依赖的客户端与 relay。
- Agent24：提供通信 CLI 入口、进程生命周期、基础 UI、Hyphae 二进制定位与版本管理。调用同一通信服务，避免 UI/CLI 各自复制身份库、outbox 或 Nostr 实现。
- 基础收发独立于模型与 Agent run。进入远端执行需显式能力范围和审批，回执不得再次触发执行。

## 待 Agent24 完成

1. 兼容 `A24_SPEAKER_BIN`，支持新 `hyphae` 名称；固定打包版本，避免 PATH 同名程序导致版本漂移。
2. 增加 CLI 通信入口，支持身份/relay 配置、发送、收件、历史、待发重试和连接状态。具体命令名先回填本文再冻结。
3. UI 分两小步：身份/联系人/relay/连接管理；基础收发/历史/待发送与失败重试。
4. 状态区分本地入队、relay 接受、对端确认、执行完成；没有对端回执时不显示“已送达”。已有接口缺字段时列清单交 Hyphae 补齐。
5. 建立 Hyphae 版本升级检查：校验发布来源与摘要、版本兼容、更新失败回滚。首次可用人工确认更新，不要求静默安装。
6. 高层任务阶段补持久化 request/run 关联、执行去重和回执重试；执行后崩溃进入待核对状态。

### 高层协议前的兼容收口

审阅基线的 `packages/nostr-bridge/src/protocol.ts` 使用 `f4/1`、say/announce/listen 和开放 intent；Hyphae 的历史方案使用 register/publish/inquire/subscribe。两者层级不同，不能把动词逐字替换，更不能因识别到 `intent=ask` 就授予执行权限。

目前 `inbound.ts` 的 `handle → process` 对白名单发件人的普通正文和任意已识别 intent 都调用 `runToCompletion`，包括 answer；seen 仅存在内存。它还没有本轮要求的“通信与执行分流、跨重启执行去重”。因此基础 CLI/UI 接线只做收发和历史展示，不应直接启用这个旧入站执行路径。以下改动由 Agent24 后续单独实现并提供验收：

- 旧普通正文和 `f4/1` 继续可读，按通信消息呈现；注册、能力查询、广播、answer/ack/report 等回执不能进入执行入口。
- 明确的新版本执行请求才可进入授权检查，至少绑定签名发件人、目标身份、request_id、能力及版本、参数摘要、有效期；开放 intent 仍可用于沟通，但不决定权限。
- 以发件人/目标/request_id 持久化去重。同 ID 不同参数拒绝；run 已开始后崩溃必须先核对执行状态，不能按收件重放直接重做。
- 返回结果与回执待发记录独立持久化；回执失败只重发回执。进程重启、重复 answer、对端自动回复均不得形成执行循环。
- T01 冻结时同时更新 Hyphae 权威协议和 Agent24 F4 契约，并提供共用正反例。具体新版本、kind、字段格式尚未冻结，本轮 CLI 改动不静默转换旧 content。

外部 OS 样例选用 [Sin90](Sin90.md)。Agent24 按现有 OS package 发现与 ME-3 外挂载流程加载；初次高层能力拟限制为获授权的只读 today 查询。这个通信授权须独立绑定请求和 run，不能由模块已挂载或 `model_access` 推断。

至少加入三个跨仓断言：普通文本/answer 入站的 run 数为零；相同授权请求重启重放的副作用计数为一；同 request_id 修改参数后拒绝且原结果保持。单靠 Hyphae 消息行数不能证明 Agent24 没有重复执行。

## 共享候选样例的消费端任务

[Agent24 契约样例交接](Agent24-contract-fixtures.md) 固定 Hyphae #96 的 70 个资料/查询样例及源文件摘要，约定消费者限长、UTF-8 字节格式、逐 id 结果和错误上下文检查。它是独立测试准备，可与 CLI 接线并行；尚未实现 Agent24 消费端，不启用生产新协议，也不解除 T01-E 门槛。

## 验收

- Agent24 CLI 驱动真实 Hyphae 二进制，经本地标准 relay 双向加密收发，无 UI/模型依赖。
- CLI 和 UI 读取同一身份、relay 配置与消息状态；断线后可恢复；普通入站不自动触发 run。
- 旧二进制名配置仍可定位；不兼容版本有可诊断错误；旧消息可读。
- 已有入口：`pnpm --filter @agent24/nostr-bridge test`、对应 `typecheck`；Rust CLI 新入口和真实二进制联调测试需在本任务补充。

待确认：CLI 命令名、安装包分发方式、版本能力声明格式、服务端接口是否继续 subprocess 或增加本地 socket。初期继续复用 subprocess + JSON，不要求提前改通信架构。

## T01 字段与恢复方案评审

[信封候选](../agent/t01-envelope-candidate.md) 逐字段说明 F4 兼容展示、查询/执行/回执分流和事件关联；[授权与恢复候选](../agent/t01-authorization-recovery-candidate.md) 给出审批、run 登记、状态修订与崩溃边界。两份都未冻结，不据此启用新生产协议。

Agent24 后续先反馈字段可实现性，特别是持久化幂等 run_id、执行器查询、结果与回执 outbox 的事务边界，以及终态 revision 冲突处理；缺口不能靠收到消息就重新运行来弥补。T01-B/D 子 schema 与共享样例收口后，再提交消费端实现和真实故障注入证据。普通消息与旧 F4 的 zero-run 分流仍是前置要求。
