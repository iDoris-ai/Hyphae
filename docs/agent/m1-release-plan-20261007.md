# M1 收尾与下一正式版计划

结论：实时 TUI 收件已随 #135 合入主干；完整加密群聊与 TUI 离线发送是当前 P0，二者验收完成后才关闭 M1。

## 版本与里程碑

用户于 2026-10-07 要求优先交付这两个闭环。下一版暂定 `v0.26.2`；若用户确认采用 `v0.27.0`，只调整发布编号，不降低验收门槛。正式版本 `v0.26.1` 已发布，其 tag/产物保持不变；后续合并不回填旧版本。

| 能力 | 里程碑 | 当前事实与出口 |
| --- | --- | --- |
| 实时点对点 TUI 收件 | M1 | [#135](https://github.com/iDoris-ai/Hyphae/pull/135) 已合；须在最终组合版本再跑三对双向收发、重连与历史去重。 |
| 完整加密群聊 | M1 | [#136](https://github.com/iDoris-ai/Hyphae/pull/136) 是协议检查点，尚待 review 修复；state/history 有未发布的本地工作，fanout、路由、CLI/TUI 与真实三人验收尚未交付。 |
| TUI 离线发送、重开恢复、自动重试 | M1 | CLI/daemon 已有 durable outbox；TUI 尚未接通。必须不依赖独立 daemon，并重试原签名 event。 |
| 自部署 relay、身份/profile 注册发现 | M1.5 | 本地单 relay 和三身份链路已有；保留旧路线图中独立 fork 的基础设施待办。 |
| 行为信封及基础行为 | M2 | 修复、兼容和联调已有部分基础，不代表整个 M2 完成；M1 闭环优先。 |
| 任务委派、协商、取消、验收与恢复 | M3 / E-M3 | 后续能力，不纳入本次聊天发布。 |
| 支付相关网络/协议基础 | M2.5 | schema、fee 等基础不等于真实资金执行。 |
| 真支付、信誉、授权与对账 | M5 / E-M5 | 按后续里程碑推进，先测试网验收，不提前接真实资金。 |

沿用原生 M1/M1.5/M2/M2.5/M3/M4/M5 与生态 E-M 编号，不创造 M6～M8，也不将聊天收尾称为生态 E-M1 全部完成。

## 技术边界与最小设计

### TUI 离线发送

复用现有 `SendQueuedAgentMessage`、`LoadOutbox`、`GetPendingOutbox` 和 `AttemptSend`，不建第二套队列。签名事件、唯一 event ID 和目标 relay 先 durable 入队；入队失败必须报告失败，不显示 queued。重试使用原密文/签名/event ID，不能重新签名生成重复消息。

TUI 自有一个可取消的 outbox worker；初次发送与周期重试串行调度，网络/磁盘操作不阻塞 Bubble Tea 更新线程。仅处理当前身份的条目，复用跨进程锁和 QueueID/attempt 检查；与 daemon 同时运行不得覆盖新队列状态。发送状态至少区分 `queued`、`relay_accepted`、`failed`；relay ACK 不是对方收到、展示或已读。

关闭时取消并等待自身 worker，不结束外部 daemon 或其他 TUI。队列跨 TUI 重开恢复；失败条目和最后错误可见，不因退出、断网或加载错误被误删。密钥只在操作期间使用，不加入 UI 状态、诊断日志或测试 artifact。存储失败、损坏队列、身份不匹配和关闭竞争必须有失败路径测试。

### 加密群聊

遵循 [#136 群聊设计](https://github.com/iDoris-ai/Hyphae/pull/136) 的固定 roster、显式邀请/接受/激活和 creator authority。不新引入共享群密钥：每条逻辑消息逐收件人 NIP-44 加密、签名，单一 `p` 标签，独立 event ID/密文，共享随机 logical ID。部分发送逐收件人报告；重试复用同一个收件人的原 event。

状态层消费每个已验证事件时仍须检查有效 opaque 值、签名者 authority、当前收件身份、creator/group/invite/roster hash 与状态迁移。协议编解码通过不能替代这些每次调用的检查。状态/历史/去重事务化，成功持久化后才通知界面；重放不得倒退或重复。

在现有单 inbox watcher 的 DM 持久化前路由 typed 群事件，不创建第二套订阅/解密，不让群协议进入 DM 历史或 auto-reply。CLI 支持明确接受/拒绝、发送、退出；独立群 TUI 展示发言人、正文及发送状态，安全处理终端控制字符。成员变化须重新邀请，不假称本地修改元数据就同步了全群。

本次“完整”指固定成员、明确同意、三用户加密收发、可靠重试、历史与 TUI 闭环；动态成员治理、共享密钥轮换、远端撤权和历史撤回不在本次范围。本机 leave 只停止本机收发/显示，不承诺删除他端副本。

## 分工与合并顺序

1. 本地 Luna A：#136 review 修复 → 独立 state/history 检查点 → fanout 与统一收件路由 → CLI/群 TUI。各检查点单独 PR，未完成的下游保持 draft，不用协议 PR 充当产品交付。
2. 本地 Luna B：TUI durable outbox 接入、状态展示、恢复重试及并发/退出测试；先独立完成点对点，群发送复用同一可靠性语义。
3. 本地 Luna C：真实本地 relay、隔离身份与 PTY 验收工装；先 offline，群入口存在后跑三人收发。缺实现应明确报未通过，不能用 mock、管理命令或吞错误的脚本假 PASS。

协调者负责技术契约、文档标准与基础验收，不代替 PR-Daemon 做 PR 级 review。顺序为 #135（已合）→ 协议/状态依赖 → 群应用；offline 可独立并行。联调 CI #134 独立收尾，合入后最终聊天候选必须通过其最新主干组合检查。

每次最新 head 均需 PR-Daemon 批准、必需 CI 通过、可正常合并；request changes 交原 Luna 修复后重新请求。禁止用旧批准替代新 head，禁止绕过分支保护。全流程只在本机开发；远程只使用 GitHub 托管仓库及 CI，不调用其他机器的 Codex。

## 发布验收与证据标准

- 三份隔离身份/profile 在持久化本地 relay 注册发现；三对点对点聊天双向显示，不依赖外部 daemon，重启历史保留、重放恰一次。
- offline：断 relay → TUI 发送唯一正文 → 明确 queued → 关闭重开 TUI → 恢复同数据 relay → 自动重试 → 接收 TUI 显示恰一次；验证同 event ID，不能将 queued/ACK 当作送达。
- group：邀请未接受前不入群；两名受邀者明确接受后激活；三端保持 TUI 打开交替发送，展示正确且恰一次；重启、重放、某收件人暂离线、部分 fanout 恢复均通过。
- 安全负例：篡改 roster/hash、错邀请对象/creator、伪造 authority、未知 sender、跨群/跨邀请重放、坏签名/密文、未知版本/大小写字段、零值验证对象均不落 DM/群历史、不改变状态。对应守卫移除时定向测试必须失败。
- 基础 gate：定向单元/集成、race、vet、diff 与 pre-PR checker；最终组合的 Linux x64 与 Darwin ARM64 CI 通过。PTY 工装未在某平台运行不得声称该平台已做 UI 验收。
- 发布记录必须给出精确 source/tag、产物 SHA-256、CI/run、每项实际结果与未覆盖边界；不公开密钥、私人 HOME、明文日志。正式 release 只在以上出口完成后创建，不预打 tag。

本文件是开发发布计划，不是已经完成的验收报告。Agent24 production lock 升级仍由 Agent24 自己评审验收；本次不修改它。
