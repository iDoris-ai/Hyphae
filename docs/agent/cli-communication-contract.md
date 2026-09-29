# E-M1-A：基础 CLI 通信契约

更新：2026-09-29。主代理设计基线，供 Luna 实现及 Agent24 接线使用。表中“待补”是目标，不代表当前二进制已经支持。基础收发复用既有 Nostr 事件；高层 behavior 不作为此阶段前置。

## 入口与责任

Hyphae 独立提供通信 CLI/daemon，Agent24 提供统一 CLI/UI 入口并调用它；AgentEar/iDoris 不参与基础文本传输。GUI 使用同一份身份/联系人/relay 配置、历史和 outbox。

`--json` 或 `HYPHAE_OUTPUT=json` 沿用现有成功信封 `{"ok":true,"data":...}` 与错误信封 `{"ok":false,"error":"...","message":"..."}`。成功写 stdout，诊断写 stderr；失败使用既有语义退出码。机器模式不混入提示文字或等待交互确认。

### 管理和收发

| 命令族 | 当前基础 | 本阶段补齐与验收 |
|---|---|---|
| `identity create/list/use` | 身份创建、列出、默认身份；list 已有 JSON | create/use 补安全 JSON；不序列化 nsec；显式 --as/--from 不改变默认身份 |
| `contact add/list` | 联系人及角色 | JSON 返回完整 npub、nickname、role；空列表为 []；保留人工输出 |
| `relay info` | WebSocket 连接测试 | 有界超时和 JSON；表述为连接结果，不冒充已订阅/已投递/持续在线 |
| `relay list/set`（待新增） | 暂无持久 relay 配置入口 | 完整替换式 set 和 list；只接受 ws/wss、拒绝用户密码和 fragment；原子写配置；显式 --relay 覆盖配置；无配置才回退既有默认 |
| `agent msg` | NIP-44 加密发送、各 relay 结果、失败入队 | 所有入队/历史写入错误可见；发布前可靠入队，重试复用事件 ID；响应准确区分 relay 接受和本地入队 |
| `history inbox/conversation` | 本地历史及完整发送者/事件 ID | 作为 UI/bridge 的稳定数据源，保持当前字段；不依赖 `agent inbox` 人工显示用昵称 |
| `storage outbox list/retry/clear` | 人工诊断与操作 | JSON 列表/重试结果/清理计数；list 不输出 EventJSON 或消息正文；clear 在 JSON 模式要求明确 --yes，确认后并发新增项保留 |
| `daemon` | 前台收件、重试、通知，可禁用自动回复 | 参数校验、离线消息补收、持久化去重；只在存储成功后记 seen/通知；连接失败可诊断；生命周期由调用方监督 |

`relay set` 作为完整配置写入，不自动探测或连接新 relay；只读 `list` 不探网。relay 参数优先级为显式 `--relay` > 本地配置 > 既有默认。已入队事件继续使用入队时记录的 relay，避免修改配置后将待发私有消息投向新的公开 relay。

## 状态语义

- `published_to > 0`：至少一个 relay 接受该事件；不证明收件人读到或执行。
- `queued_for_retry=true`：待发状态已经可靠保存。磁盘写入失败不能报告为已入队。
- 所有 relay 失败但可靠入队：可报告成功完成“提交发送请求”，同时保留 published_to=0 和每 relay 错误，消费者不得显示已送达。
- relay 接受后，本地状态/历史写入失败：返回可诊断错误，说明可能已发布；不得伪装成完全未发送，也不得生成新事件盲目重发。
- `storage outbox retry` 的 command success 表示返回了一次重试结果；结果字段明确 sent/published、是否仍待发和 bookkeeping error。错误路径不输出虚假的 Sent。
- 没有对端应用回执时，不提供“已送达/已读/已执行”的推测状态。这些字段在高层协议阶段扩展。

## 安全和兼容

1. 保留现有 CLI 参数、JSON 字段与旧 kind 30078 的读取，新增字段保持兼容。
2. JSON 只返回必要公开信息。密钥、密码、完整 outbox 序列化事件不出管理输出；错误经过现有脱敏。
3. 普通收件不启动 Agent24 run；执行授权在后续 bridge 层判断。daemon 的自动回复默认关闭，回执/自动回复不能触发循环。
4. outbox 的每次生产读改写在跨进程事务内读最新值；网络请求在锁外。持久化失败、并发删除和过期快照都不能复活已删除项。
5. 补收在 relay 的可用历史和保留策略范围内进行；不能承诺 relay 已删除的消息可恢复。验收至少覆盖超过旧 limit=10 的离线积压和重启重复投递。

## A 段验收

使用临时目录、Alice/Bob 两身份和真实本地 khatru relay，构建实际二进制，机器判断 JSON 与退出码：

- 创建身份/联系人 → 选择 relay → 双向加密收发 → 历史核对完整正文、身份与事件 ID。
- 停 relay → 发送并确认持久入队 → 重启 relay → 重试并确认同一事件 ID、准确发送状态。
- 停收件 daemon → 发送超过十条消息 → 重启补收 → 再重启，消息不丢、不重复通知。
- 只读管理输出无私钥/密文正文；空结果仍是合法 JSON；错误输入、磁盘故障、不可达 relay 均可诊断。
- Agent24 拿同一二进制验证适配；跨仓未接线前只记录 Hyphae 侧 A 段通过。

基础 UI 的对接与待确认项见上游协作 PR 的 `docs/cooperation/Agent24.md`。本契约不要求将 UI 实现在 Hyphae 中。
