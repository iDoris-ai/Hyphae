# Agent24 × Hyphae

状态：Hyphae 侧协作提案，待 Agent24 确认。基线：Agent24 `32072b0`。

## 已有接口

`packages/nostr-bridge/src/speaker.ts` 已通过子进程调用通信 CLI；`config.ts` 支持 `A24_SPEAKER_BIN`，默认仍为 `agent-speaker`。正式 Agent24 CLI 位于 `rust/apps/agent24-cli/src/main.rs`，当前没有统一通信子命令。

继续使用已存在的 `identity`、`profile publish/discover`、`agent msg`、`history inbox --as`、`storage outbox`、`daemon` 能力及 `--json` 输出。入站必须保留完整发送者公钥和事件 ID，禁止依赖显示用截断名称。

### 本轮可接线接口

这些变更仍在 PR 分支，不能把当前 main 当成已支持全部接口。最新已验收的组合开发基线为 `integration/em1-cli-reliability` / `6e64aaa`；正式打包版本须在合并后重新固定。它是依赖组合分支，各项修改仍由单独小 PR 评审。

| 能力 | Hyphae 接口与状态 |
|---|---|
| 身份/联系人 | PR [#45](https://github.com/iDoris-ai/Hyphae/pull/45)：create/list/use、contact add/list 的 JSON 已验收；只输出公开身份字段 |
| relay 配置 | PR [#50](https://github.com/iDoris-ai/Hyphae/pull/50)：`relay set --relay URL` 可重复、完整替换；`relay list` 返回 relays/source；`relay info [URL] --timeout 5` 返回 url/connected |
| 配置优先级 | 显式 --relay > `~/.hyphae/relays.json` > 既有默认；坏配置报错，不静默换公共 relay。已入队事件保持原地址 |
| 消息可靠性 | 重试事务 #48、历史明文 #49、发布前可靠入队 [#54](https://github.com/iDoris-ai/Hyphae/pull/54) 已验收；`published_to=0` 且 `queued_for_retry=true` 表示已提交待发 |
| 待发管理 | [#53](https://github.com/iDoris-ai/Hyphae/pull/53)：`storage outbox list --json` 为安全数组；`clear --failed --yes --json` 返回 removed/remaining。retry JSON 仍在实现 |
| 收件 | 原子首次收件 #51 和 daemon 接线 [#55](https://github.com/iDoris-ai/Hyphae/pull/55) 已验收；inbox 查询错误和 daemon 离线分页仍在实现 |

机器模式成功在 stdout 输出一份 `{"ok":true,"data":...}`；错误在 stderr 输出错误信封，退出码沿用 1 用户输入、2 网络、3 身份解锁、4 其他、5 写冲突。UI 不解析人工提示文字。`connected=true` 只表示一次 WebSocket 握手成功，不能当持续在线、已订阅或已送达。各项字段及补收门槛见规划 PR [#39](https://github.com/iDoris-ai/Hyphae/pull/39) 的 CLI 通信契约。

发送错误的信封可含 `data`：沿用 event_id、published_to、queued_for_retry，并增加 history_stored、superseded、queue_state_unknown。Agent24 即使收到非零退出码也要读取这些字段；relay 已接受而本地记账失败时，按原 event_id 核对，不创建新消息自动重发。队列状态未知时 UI 显示待核对。

配置在命令或 daemon 启动时解析。`relay set` 或默认身份变化不会自动重配已运行的 daemon；Agent24 应由同一进程管理入口重新启动相应实例，并继续保留旧待发记录的 relay 地址。`agent inbox` 是有 limit 的单次 relay 查询；持续收件由 daemon 写入本地历史，UI 从 history 读取，不把一次 inbox 返回当成所有历史已同步。

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

## 验收

- Agent24 CLI 驱动真实 Hyphae 二进制，经本地标准 relay 双向加密收发，无 UI/模型依赖。
- CLI 和 UI 读取同一身份、relay 配置与消息状态；断线后可恢复；普通入站不自动触发 run。
- 旧二进制名配置仍可定位；不兼容版本有可诊断错误；旧消息可读。
- 已有入口：`pnpm --filter @agent24/nostr-bridge test`、对应 `typecheck`；Rust CLI 新入口和真实二进制联调测试需在本任务补充。

待确认：CLI 命令名、安装包分发方式、版本能力声明格式、服务端接口是否继续 subprocess 或增加本地 socket。初期继续复用 subprocess + JSON，不要求提前改通信架构。
