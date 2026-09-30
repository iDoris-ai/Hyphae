# T01-B：信封字段与消息分流候选

状态：root 设计提案，尚未冻结。基线 Hyphae `e753e6f`、Agent24 `879d77e`。传输候选见 [PR #88](https://github.com/iDoris-ai/Hyphae/pull/88)，JSON 输入策略与共享样例见 [PR #91](https://github.com/iDoris-ai/Hyphae/pull/91)。本文固定供下一步 schema 评审使用的候选字段；不是已实现的 API，也不解除 T07。

## 行为与类型

外层 `c` 与正文 `protocol` 均为 `hyphae-behavior/1`；两者不一致拒绝。类型是闭合集合，不能用开放 intent 推断。下表的编码和路由沿用 #88 候选；只有 execute 类型可交给授权检查，其余类型启动 run 的数量必须为零。

| 外层 b | 正文 type | 编码与目标 | 关联 | 内容用途 |
|---|---|---|---|---|
| register | declaration | json；无 p | 无 e | 允许公开的资料和能力声明 |
| publish | notification | json；无 p | 无 e | 允许公开的广播正文 |
| inquire | query | nip44；唯一 p | 无 e | 查询已声明能力；不调用该能力 |
| inquire | response | nip44；唯一 p | 唯一 e | 返回能力查询结果或明确错误 |
| execute | execution_request | nip44；唯一 p | 无 e | 单步执行请求；仍需 Agent24 授权 |
| receipt | execution_receipt | nip44；唯一 p | 唯一 e | 已登记执行请求的状态或结果 |

E-M1 的 subscribe 是本地订阅操作：设置 relay filter、读取和取消订阅。它不发布一个能让远端持续执行的 subscribe 请求。通知走 notification；远程持续触发、自动匹配和协商仍由后续里程碑设计。未知 b/type 或不在表中的组合拒绝，不能降级为执行。

## 通用字段与字段存在性

正文不重复发送者字段。身份只来自已验证的 Nostr 签名作者；正文不能声称另一个 author。公钥、事件 ID、请求 ID 都是大小写敏感的规范字符串，不接受 npub、显示名或自动转换。

| 字段 | 候选类型与约束 | 存在规则 |
|---|---|---|
| protocol | 字符串，恰为 `hyphae-behavior/1` | 全部类型必填 |
| type | 上表枚举字符串 | 全部类型必填 |
| issued_at | 非负安全整数，Unix 秒 | 全部类型必填；重投请求不改变 |
| expires_at | 非负安全整数，Unix 秒，严格大于 issued_at | declaration/notification/query/execution_request 必填；response/receipt 禁止 |
| request_id | 32 个小写十六进制字符 | query/response/execution_request/receipt 必填；公开类型禁止 |
| to | 64 个小写十六进制字符，与唯一 p 完全一致 | 点对点类型必填；公开类型禁止 |
| request_event | 64 个小写十六进制字符，与唯一 e 完全一致 | response/receipt 必填；其他类型禁止 |
| payload | object；使用该消息类型的封闭 schema | 全部类型必填 |

表中的 receipt 简写指 execution_receipt。缺少必填、禁止字段出现、未知顶层字段、错误类型和 null 均拒绝；不填默认值或忽略拼写错误。payload 内业务参数可以按能力 schema 使用 null，但不改变顶层字段规则。

request_id 由发送方为一个逻辑请求生成 16 字节安全随机数，再编码为小写 hex；重投保留该值。query 与 execution_request 使用同一 ID 空间，接收登记同时绑定消息类型，不能把已登记的 query 升格为执行。授权记录的键包含签名作者、目标、request_id；显示用 thread_id 不替代该键。

issued_at 和 expires_at 是不可变的请求内容。Nostr created_at 可以在重新签名投递时变化；它不延长授权。格式校验只证明字段合法，时钟、过期和重放判定见 [恢复候选](t01-authorization-recovery-candidate.md)。响应和回执不沿用请求的过期门槛，否则离线恢复会丢掉已经完成的结果；接收方仍须核对其签名、关联和状态修订。

## payload 子 schema 的拆分

| 类型 | 下一步 schema 要固定的内容 | 当前边界 |
|---|---|---|
| declaration | simple/tagged/structured 三种模式、公开字段白名单、能力 ID/版本、有效期及撤销表示 | 新声明不是现有 profile 的静默转换；最新失效/撤销记录不能让旧能力复活 |
| notification | 公开正文、主题及允许公开的附件引用 | 不携带私有参数、模块内部工具名或授权令牌 |
| query | 能力选择条件与明确的查询范围 | 查询“是否具备能力”不试运行工具、模型或模块 |
| response | 查询成功的能力描述，或结构化错误 | 不使用执行状态；错误不生成 execution_request |
| execution_request | 能力 ID/精确版本、params、权限范围、模块版本、隐私/实际落点要求、预算上限和单位 | 全部影响执行与授权的字段须进入摘要；模型/模块字段等待 T01-D 收口 |
| execution_receipt | 完整状态快照、receipt_seq、请求摘要、run_id、结果或错误、实际模型及用量来源 | 状态字段见恢复候选；结果超出正文上限时明确失败，E-M1 不自行扩展大附件协议 |

这一表是子 schema 的交付清单，不是允许实现自行补字段的占位接口。T01-B 后续须提交每种类型的闭合 JSON Schema 与共享正反例；执行 payload 在 T01-D 定义完之前不能被生产入口接受。不同类型的 payload 不通过自由 intent 互换。

声明选择先验签并检查 schema，再按同作者的 issued_at 取最新；同秒冲突以事件 ID 小写 hex 的字典序较小者胜出。这个排序候选也适用于已失效或撤销的声明，不能先过滤失效记录再回退到旧能力。具体撤销字段、最大生命周期及未来时钟记录的处理须随 declaration schema 固定；尚未完成，不能开启新声明发现路径。

## 关联与错误

发送方查询本地已登记的原请求，核对 response/receipt 的签名作者为原目标、to 为原作者，request_id/type 与原请求一致，request_event 属于该请求已记录的合法投递事件 ID。正文与 tags 互相一致仍不足以证明关联：第三方复制 ID、未知 e 或把查询回执关联到执行请求都拒绝。

错误至少分为 BODY_SCHEMA、UNSUPPORTED_PROTOCOL、UNSUPPORTED_MESSAGE、ROUTE_MISMATCH、CORRELATION_MISMATCH、EXPIRED、REQUEST_CONFLICT、UNAUTHORIZED、BUDGET_REJECTED、MODEL_UNAVAILABLE、EXECUTION_FAILED、EXECUTION_UNKNOWN。JSON 输入错误沿用 #91 的分层错误码；传输失败独立记录，不能覆盖执行终态。最终错误信封和是否发送拒绝回执仍待 schema 与授权审查收口；未认证的坏输入不自动回复或触发模型。

## Agent24 f4/1 兼容映射

固定源码：[protocol.ts@879d77e](https://github.com/iDoris-ai/Agent24/blob/879d77eee5eafac3e45641ae4cde8762481b1bc2/packages/nostr-bridge/src/protocol.ts)。下表是待实现的兼容展示规则，不是自动协议转换。目标是旧输入全部保留原格式、启动 run 为零；当前 Agent24 inbound 尚未满足这一分流要求。

| F4 字段/入口 | 兼容处理 | 新执行契约中的边界 |
|---|---|---|
| version=f4/1 | 保留为旧通信格式 | 不改写为新 protocol |
| say/announce/listen | 分别作为旧点对点、广播、订阅入口展示 | 不按动词替换 b/type；listen 不能授予远端执行权限 |
| intent，包括 ask/answer/ack/report 及任意字符串 | 展示原 intent | 不能据 intent 推断 execution_request 或 receipt |
| thread_id | 保留原会话关联；源码使用 randomUUID 默认生成 | 不改成 request_id，不与执行去重键混用 |
| reply_to | 保留旧事件引用，可供通信展示 | 不凭它建立新 request_event/回执登记 |
| topic、tags | 沿用旧路由/展示数据 | 不作为能力版本或权限声明 |
| payload | 沿用旧 free-form 内容及现有 scrub 规则 | 不当作符合新执行 schema 的 params |
| expires_at（可缺省） | 有值时显示旧过期信息 | 不从缺省推断有效授权或新请求有效期 |
| status=ok/working/failed、error | 展示旧状态/错误 | 不更新新执行状态，不补 run_id/receipt_seq |

Agent24 CLI/UI 的旧消息接入应先完成展示分流，再接新契约。现有 inbound 自动调用 run 的路径不能因兼容映射而继续用于普通消息或旧 answer。

## 收口验收

下一项共享样例须覆盖全部合法 b/type、非法交叉组合、字段缺省/null/未知字段、错误目标、复制 request_id 的第三方响应、未知关联事件、query 升格执行、旧 F4 任意 intent，以及声明同秒冲突与失效不回退。Go 与 Agent24 消费同一份 fixtures 后分别记录版本和命令；测试参考、schema 通过和真实入口通过是不同证据。T01-E 完成后才同步权威协议、速查和跨仓字段声明。
