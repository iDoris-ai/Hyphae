# Agent24 × Hyphae

## 2026-10-01 接线更新

Hyphae 当前固定 main 为 `a4aa606eb81d5c040d94c51cdf94553e646d8674`。[Agent24 COMM-0 #612](https://github.com/iDoris-ai/Agent24/pull/612) 已合并，采用统一 Rust 通信服务和 `/api/v1/comm/*`，CLI/UI 共用配置与状态；[COMM-1a #614](https://github.com/iDoris-ai/Agent24/pull/614) 已在 `77655f48` 合并，最终双平台 Rust CI 全绿。其 runner 和环境扫描修复已进入 main；CI 尚未构建锁定版本 Hyphae，真实二进制测试仍可跳过，这部分交付仍待补齐。

Hyphae 已核验 [运行反馈](https://github.com/iDoris-ai/Agent24/pull/601#issuecomment-5923235533)。G7 会话历史 JSON、G9 文件正文已本地实现并通过专项测试，G8 keystore 完整并发写保护及专项/race 回归已通过；尚未发布，具体契约与验收见 [联调缺口](../agent/em1-comm-followups.md)。daemon 互斥已本地实现并通过专项/race，特殊锁路径正在补修；只读口令校验已派发，补收状态契约正在设计。不要提前调用尚未发布的命令。

当前 Agent24 main `c9f5f9cab1c208b09f7ebf9d13a3e1481adcaf12` 的启动入口已默认停止 F4b 入站执行分派；直接 `InboundBridge.handle` 仍保留旧执行逻辑。联合验收须从正式入口验证普通消息、answer、未知版本的 runs/模型/模块计数为零，不能仅根据配置或源码判断通过。

Agent24 提案 [#601](https://github.com/iDoris-ai/Agent24/pull/601) 在 `67ddbce` 已获批准和全绿 CI；用户已授权合并，本会话工具拒绝了合并调用。勘误 `9bfb0df` 尚在独立本地 worktree，待 #601 合入后另提文档 PR。Hyphae 接口变化合入后须更新 Agent24 的源码/构建配方/hash lock，再固定联合测试版本。

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
