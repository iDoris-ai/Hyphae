# M1 outbox failure-result / recovery-witness schema supplement

状态：**提案，等待 coordinator 独立评审；未获 schema 或实现验收**（2026-10-11）。本补充仅收敛 [rev7 契约](m1-group-fanout-contract-20261008.md) §3.1–3.5、§5 的 JSON 表达与 F2b 拆片，不重开跨存储设计。rev7 仍为语义依据；下面标记 P 的选择须逐项接受后才能据此实现。S7、受影响 S8/S9 持续 BLOCKED，直到 F1–F4 所有实现、独立评审、合入和验收 gates 关闭；不宣称 M1 完成。

本次精确审计基线为 main `1df9c6707c4df956bfc8868509bf33c1925bac2d`，开始时 HEAD 相同且工作树干净。该对象含 F1 #169（`be6d0a7`）及 F2a #170（`48e81dd`），**合入不等于完整 F1–F4 验收**。旧契约/计划中的 `699b36f…` 是其历史审计基线，不是本补充的源码状态。检查了 `outbox_evidence.go`、`outbox_group.go`、`outbox.go`、`groupchat/fanout*.go` 及相关现有测试：F2a 只接受 legacy schema，明确拒绝所有 protocol/result/witness；F1 有 reservation/start、revision/accounting primitives，但旧无 token handler、typed writer、裸 requeue 仍存在，不能充当 rev7 fence/witness。现有 route 修复和 failed→ACK 测试是旧路径行为，不授权未来严格路径沿用。

## 1. 通用 raw 规则与封闭成员表

下文每个对象的表就是**完整 allowlist**，并非“至少这些字段”。所有字段名大小写精确，先 JSON 解码字段名再比较；`version` 与 `vers\u0069on` 是同名。任何对象层重复成员，即使值相同，也拒绝；未知/额外成员也拒绝，绝不忽略后继续。除本节 relays 兼容例外外，拒绝显式 null、错误类型、缺必需字段、尾随 JSON token、非法 UTF-8、整数溢出；可选对象只有省略表示无记录，`{}` / null 不表示缺席。JSON 文本合法但 schema 非法的样例仍是 negative。

整数必须是无小数/指数的 JSON 整数 token（`1.0`、`1e0`、字符串、bool 均拒绝）。记 `I64=9223372036854775807`，`C=min(I64, Go math.MaxInt)`：已有 outbox 与 report 的 int 计数范围保留为 `[0,C]`，时间/revision/generation 为 `[0,I64]`；不得经 float64 转换。需要 +1 的实际写入在上限 fail closed，不溢出/饱和。时间为 Unix 秒，0 合法而非 missing；不从 wall clock 倒退推断损坏，S 时间使用已提交 `last_attempt_at=max(old,now)`。

| 对象 | 必需成员 | 可选成员 |
| --- | --- | --- |
| outer | `entries`（对象数组，可空） | 无 |
| DM entry | `id,event_json,recipient_npub,relays,retry_count,max_retries,last_attempt,created_at,status` | `route,queue_id`；禁止群 metadata |
| group entry | DM 必需成员 + `route,queue_id` | `fanout_protocol,failure_result,recovery_witness`，按下述条件 |
| signed event（event_json 解码后的对象） | `id,pubkey,created_at,kind,tags,content,sig` | 无；event extra members 也拒绝 |

entry 字符串/计数/事件验证沿用 rev7 §3.2 和 F2a。群 route 必须显式 `"group"`，QueueID 非空；status 仅 `group_pending/group_failed`；`group_failed` below-limit 损坏，`retry_count>=max_retries` 的 pending 是耗尽。签名/ID、kind=30078、应用 discriminator 和恰一个匹配 recipient 的 p 必须有效；sender 对应 key.local_npub。普通 DM 空/缺 route/QueueID 例外只在无 group status 且不与任何 fanout EventID 碰撞时成立；显式 null 仍拒绝。任何操作先全 route/status 枚举 outer ID、signed EventID 和 QueueID；身份不可可靠提取时整文件 hold，不能先过滤 DM/failed。

`fanout_protocol` 出现时只能为整数 1。严格合法 legacy group 可单独缺它；有 result/witness 必须有 protocol=1，DM 不得携带它们。未知版本不能剥掉后当 legacy。升级补 protocol 不能补 Route/QueueID、假造历史 token、result 或 witness。

`relays` 是 required 字符串数组或 legacy null；[] 与 null 都运行时选择 defaultRelays。旧 DM/group null（包括补 protocol 后）持续合法。数组中的 null/非字符串、缺 relays 均拒绝；新建默认选择写 []，不将 runtime 默认地址持久化到旧条目。

**P1 — enc 与额外成员的明确选择**：reader 接受重复的 `enc` tags，只要每个都有非空第二个字符串且第二值都是 `nip44`；完全相同的重复也接受。`["enc","nip44","opaque"]` 的额外字符串位置接受并保留，仅第二位置解释加密算法；不同后缀不改变该解释。一个成员的 enc、空算法、future/conflicting 算法、任何非字符串 tag member 拒绝。群至少一个 enc=nip44；DM 可无 enc（legacy 明文），有则同样检查。此兼容选择依据 rev7 §3.2 的无歧义加密检查及当前 `outboxTagValue` len>=2/同值重复行为；§1 构造规则仍让新 event 写一个规范两成员 enc。它不放宽 p 的恰一个约束或 `ValidateAgentMessageEvent` 对 c/v/d 等 discriminator 的既有规则，不允许去重/删后缀后重签。JSON 对象 extra members 继续全部拒绝，依据 rev7 每层 unknown-key 规则；tag 数组后缀与 JSON 对象成员不同。此 reader 兼容性待独立评审，不能把生成侧规范误称为已批准的重复 enc 禁令。

## 2. 完整 key、AttemptToken 与摘要

**P2 — wire 布局选择**：`fanout_key` 含完整五列主键（Go FanoutKey 的四列 + recipient），不靠 EventID 找 row。固定 key allowlist 全 required、非 null：

| 成员 | 类型/范围 |
| --- | --- |
| `local_npub,recipient_npub` | canonical npub 字符串；ParsePublicKey 后 EncodeNpub 与原值相同，不在 reader 中替换；分别绑定 signed sender、唯一 p 和 entry recipient |
| `group_id,send_key` | 32 位小写 hex opaque ID；send_key 在 message 为 logical_id，其余为 invite_id |
| `envelope_type` | 字符串枚举 `message,invite,accept,decline,activate,cancel` |

AttemptToken allowlist 全 required：`fanout_key`（上述对象）、`event_id`（64 位小写 hex，匹配 entry.id/signed ID）、`queue_id`（非空字符串，精确匹配 entry.queue_id）、`attempt_generation`（整数 `[1,I64]`）。QueueID reader 只要求非空有效 JSON 字符串，逐值精确比较、不 trim、不强迫 legacy ID 为 hex；新 writer 沿用 16 random bytes→32 位小写 hex 并做全文件唯一性检查。R 才 +generation，generation 不等于 attempts 或 retry_count；legacy generation=0/idle 可以存在于 row，不能构造 generation=0 的发布结果。

row_revision **不在 token 内**：R/S/T4 正常改变 revision，不改变既有 token 身份。result/witness 的 expected revision 是写入/重放前 row 的 CAS 前像；锁内重新读取，不接受 caller 的过期值。这是 rev7 §3.1/§3.3 的具体表达，不新增 state/revision 弱化。

**P3 — 摘要与 ID 编码选择**：所有 digest 为 SHA-256 小写 hex（64 字符，无 `sha256:` 前缀）。三种摘要输入明确分开：

- `event_json_digest`：entry.event_json 字符串解码后的 UTF-8 bytes（亦是冻结 SQLite event_json），不包括外层引号，不重新序列化 event。
- `old_entry_digest`：替换前严格校验的旧 entry 原始对象 bytes，从 `{` 到配对 `}`，含对象内部全部空白/null/metadata，不含数组分隔空白。
- SQLite failure receipt digest / recovery digest：对应字段的原始 **value fragment**，从冒号之后到逗号/父对象右括号之前，含值前后空白；与 F2a `Fields` 保存的范围一致。解析成功后的 raw fragment 直接哈希，禁止 canonicalize 后哈希。

`result_id` 由 token 唯一确定：SHA-256(UTF-8 的 compact JSON array `["hyphae.failure-result.v1",local_npub,group_id,envelope_type,send_key,recipient_npub,event_id,queue_id,attempt_generation]`)，无空白、无尾换行，JSON 字符串按标准转义，最后一项为十进制整数；这些 token 字符串采用上述规范字符，QueueID 中所有 U+0000–U+001F 控制字符（包括换行/tab，不用短转义）使用小写 `\u00xx`，引号/反斜线用 `\"`/`\\`，其他 Unicode 直接 UTF-8，`/` 不转义。ID 用语义 tuple 稳定寻址，receipt 用原始 fragment 检测改写，两者不能互代。`recovery_id` 为每次真实 D6 随机 16 bytes→32 位小写 hex；重复调用先检查已有 witness/receipt，不能生成新 ID 重置预算。依据 rev7 的 token 唯一结果、随机 recovery ID、精确 receipt 和原始旧证据要求；ID/digest 编码是新增提案，碰撞/不同 payload 同 ID 一律 hold。

## 3. failure_result v1

出现时下表每项 mandatory、非 null，无其他成员。对象只能由有效当前 P 失败的 O3 写入，不能记录校验失败、R/S crash、成功或迟到回调。

| 成员 | JSON 类型/范围与含义 |
| --- | --- |
| `version` | 整数，恰 1 |
| `token` | §2 AttemptToken 对象 |
| `result_id` | §2 token 派生 64 位 hex；必须重算相等 |
| `expected_row_revision` | 整数 `[0,I64-1]`，O3/T4 前的当前 started row revision |
| `attempts` | 整数 `[1,C]`，S 已提交的累计 publish starts（含 legacy base），不是失败预算 |
| `last_attempt_at` | 整数 `[0,I64]`，S 已提交的单调时间快照 |
| `relay_count` | 整数 `[0,C]`，S 的 len(targets)；零目标不伪造 ACK |
| `prior_retry_count` | 整数 `[0,C-1]`，当前 QueueID 的 O3 前失败数 |
| `retry_count` | 整数 `[1,C]`，恰 prior_retry_count+1 |
| `status` | 字符串 `group_pending` 或 `group_failed`，按预算推导 |
| `issue` | 字符串，枚举 `send_failed,retry_exhausted`；count<max 恰 send_failed，count>=max 恰 retry_exhausted；**P4/B-schema-1 条件修订提案，待独立评审** |

写入前：row.state=queued、phase=started、完整 token/recipient/frozen event/max_retries 匹配、generation 尚无结果、revision 相等；current entry active pending 且 prior_retry_count<max_retries。O3 在同一个 JSON 原子提交写 result + entry.retry_count=result.retry_count + entry.status=result.status + entry.last_attempt=result.last_attempt_at；新 count<max_retries 则 status=group_pending/issue=send_failed，否则 status=group_failed/issue=retry_exhausted（P4 条件修订提案）。新记录的 count 最大为 max_retries，不把 legacy 已超预算条目当可再发；legacy count>max 仍按 rev7 exhausted 处理。

未消费重放：当前 SQLite token/queued/started、expected revision、attempts、last_attempt_at、relay_count 和旧预算都匹配 S 前像（旧预算若 legacy unknown 必须在 R 前严格初始化，不能此时猜测）；JSON entry 与 result 后像相等。T4 不增加 attempts/generation/RetryCount，投影预算、phase=failed、state（耗尽为 failed，否则 queued）、issue、last_result_generation、完整 token+raw digest receipt，revision +1。任何不一致 hold，不先写再 callback fence。

已消费检测先查完整 token+raw digest receipt；匹配是 no-op，即使后续 R/S/T3 改了 generation/revision/attempts/targets。只比较 immutable attribution，不把 result 的旧 S 快照与后来的 S 混比。last_result_generation 单独相等或大于不足以证明消费；同 token/ID 不同 digest、仅水位但没有精确 receipt、无法解释的未来 token 是 bookkeeping_conflict。result 的 token.queue_id 始终匹配所在 entry，D6 不把旧 epoch result 复制到新 entry。未消费 result 禁止任何新 R/S/D6/O2/row 修改（stale callback 只读返回的例外见 §5）。已消费 result 保留至下次经验证 O3/D6 替换或授权 O2 删除。

**P4 / B-schema-1 — 耗尽 issue 的最小条件修订提案（coordinator 提议，等待独立评审）**：O3 result.retry_count<entry.max_retries 时，status=group_pending、issue=send_failed；result.retry_count>=entry.max_retries 时，status=group_failed、issue=retry_exhausted。实际 O3 prior<max 且只+1，因此新耗尽结果落在恰好等于 max 的边界；旧 legacy 超限仍按原 exhaustion 分类。T4 始终精确投影 O3 的 status/issue，无另一次补偿迁移。此选择保留 rev7 §2 Carol 耗尽验收，也保留非耗尽 current-O3 send_failed。

原 rev7 有真实矛盾：§2「验收」要求“Carol 重试耗尽 → Carol 行 failed、issue retry_exhausted”，§3.5 写“当前 O3 的 send_failed”，§3.3 要求 T4 投影 O3 issue。反例是第 M 次真实 P 失败，O3 写 group_failed，T4 提交后立即 LoadFanoutReport；无条件 send_failed 不满足 §2，直接 retry_exhausted 不满足 §3.5 无条件文字。不能靠事后再改 failed row 补验收。

**待接受的 rev7 §3.5 精确替换文本**（本任务保留两份 docs 上限，因此在这里给出可直接评审的修订，不更改第三份 contract）：将末段的「当前 O3 的 send_failed」替换为「当前 O3 在 result.retry_count < MaxRetries 时的 send_failed，或在 result.retry_count >= MaxRetries 时的 retry_exhausted；T4 精确投影该 O3 的 status/issue」。§3.1/§3.3 的投影规则读作此条件映射，不改变计数/锁/CAS/屏障或 §2 验收；§3.5 同队列耗尽/queue_missing 分类的既有 Issue 分工保持。补充中的该条件 mapping 是显式契约修订提案，不能声称原 rev7 已无矛盾。

B-schema-1 仍是**独立评审/原契约修订待接受的 blocker**，而非已经解决或已冻结：coordinator 已确认矛盾并提出上述候选，但未以独立 review PASS 关闭它。裁定前 parser 可 inert 实现提案并提交评审，完整 F2b schema freeze、F3 exhausted T4 及 gates 不得关闭；不得先改 writer。queue_missing 和 operation diagnostics 不受这项修订影响。

## 4. recovery_witness v1

下表 common 全 mandatory、非 null，无其他 common 成员；conditional 成员仅按 reason 出现，禁用组必须省略（null 不等于省略）。

| common 成员 | JSON 类型/范围与含义 |
| --- | --- |
| `version` | 整数，恰 1 |
| `recovery_id` | §2 随机 32 位小写 hex |
| `fanout_key` | §2 完整五列 key 对象 |
| `event_id,event_json_digest` | 各 64 位小写 hex，分别绑定 entry/signed/frozen ID 和逐字节冻结 event |
| `recipient_npub` | canonical npub，必须等于 key recipient、entry recipient、signed p |
| `max_retries` | 整数 `[1,C]`，等于 entry 与冻结 row 的 max_retries |
| `expected_row_revision` | 整数 `[0,I64-1]`，已提交 failed row 的 D6 前像 |
| `expected_attempt_generation` | 整数 `[0,I64]`，同一 failed row 水位；不是新 attempt token |
| `old_queue_id` | 字符串，必需；仅 legacy failed 从未有 QueueID 且 reason=missing 可为 "" |
| `new_queue_id` | 非空字符串，等于新 entry.queue_id，与 old_queue_id 不同且全文件无碰撞 |
| `reason` | 字符串枚举 `missing,exhausted_pending,exhausted_failed` |
| `authorized_at` | 整数 `[0,I64]`，显式 D6 持锁授权时间 |

| reason | 必需 conditional 成员 | 必须省略 |
| --- | --- | --- |
| `missing` | `observed_absent`：JSON bool 恰 true；全 route/EventID 持锁扫描为零 | `old_entry_digest,old_status,old_retry_count` |
| `exhausted_pending` | `old_entry_digest`：64 hex；`old_status`：恰 `group_pending`；`old_retry_count`：整数 `[0,C]` 且 >=max_retries | `observed_absent` |
| `exhausted_failed` | 同上，但 old_status 恰 `group_failed` | `observed_absent` |

**P5 — witness 表达选择**：使用上述名字（包括 expected_attempt_generation），recipient 同时在 key 与独立声明中强制相等；不另写 sender、state、old_max_retries 或 old_last_attempt。state=failed 来自锁内 row 验证，旧 max_retries 必须与冻结 row 相等，旧完整摘要覆盖旧 counter/time/null/metadata。missing 记录当前缺失，不提供“不存在的旧条目”摘要，不虚构历史 RetryCount。authorized_at 不要求大于旧 S 时间；依据 rev7 §3.4 原子来源证明和 §3.1 时间规则，不增加跨时钟假设。

D6 必须先通过 §5 优先级及恢复屏障，且有显式目标 failed recipient 授权。exhausted 只能是唯一同 old_queue_id 的条目，status/counters/recipient/event/max 全通过检查；旧摘要在替换前取，包括 relays:null。新 entry 与 witness 同次提交，status=group_pending、retry_count=0、last_attempt=0、fanout_protocol=1、冻结 event/recipient/max 不变；默认 relays 写 []。**P6 — 新 epoch created_at 选择**：新 entry.created_at=authorized_at（队列创建时间，沿用 existing enqueue 时间语义）；SQLite fanout.created_at 与 signed event.created_at 永远不变，不能要求三者相等。

未消费 witness 验证 failed row/key/event/digest/recipient/max、expected revision/generation、old_queue_id 精确相等；新 entry 仍 active pending 且 retry_count=0/last_attempt=0，无 failure_result，created_at=authorized_at。旧条目此时已原子移除，不要求再次存在；旧 evidence 是受信任本地存储的持锁观察凭据，不是抵御恶意文件编辑的签名。T2' 采纳 new_queue_id，记录 recovery_id+raw fragment digest，清 Issue、phase=idle、新 retry_queue_id/count=0，保留 attempts/generation/last_attempt_at/relay_count/last_result_generation/last_failure_receipt；activation hook 与此同 SQLite 事务。

已消费只凭同 new QueueID + last_recovery_id + last_recovery_digest 和 immutable attribution 判断；row 后来 queued/failed/accepted 都可，不能再要求 expected revision/generation 是当前值或预算仍为 0。后续 O3 可与已消费 witness 同 entry 共存（failure token 指新 QueueID，witness expected generation 是旧快照）；两个**未消费**记录不能共存，因未消费 witness 阻止 publish。已消费 witness 不在 O3/R/S/T3 改写，保留至下次验证 D6 替换或精确 O2。D6 新 entry 不复制旧 failure_result/witness；历史水位在 SQLite 留存。任意同 ID/不同摘要、same old/new QueueID、reason/conditional 不一致、未经消费的 witness 被其他写入覆盖均 hold。

## 5. hold、writer 与 D14

F2b 是 inert parser/schema 子片；reader 不能通过 JSON 单独证明 SQLite revision/receipt 或“曾经显式 retry”。结构、自身字段一致性由 parser 检查，跨存储前像/消费情况由 F3/F4 共享锁内 validator 检查，不能把结构合法等同授权。

沿用 rev7 §3.5 first-match：不可枚举 file corrupt → fanout corrupt → 全 route EventID duplicate → 唯一 entry schema/QueueID collision corrupt → 确无匹配 row 的 orphan → frozen intent / queued或accepted QueueID conflict → bookkeeping conflict → 未消费凭据投影 → 正常状态。能归属 EventID 的坏条目参与 duplicate 优先级，不能因 parser 一次返回 error 就丢掉其原始身份；不可归属才 file hold。

**D14 不可绕过**：queued/accepted row 的唯一条目 QueueID 不等于持久 QueueID，即使 entry exhausted、携带 witness、relays:null 或看似新建，必须报告 queue_conflict 并 hold，两库、文件 bytes、时间、Issue 都不动，零 publish/删除。禁止 queued→failed→queued 洗冲突。只有 failed row 的有效 D6 witness 可暂容新 QueueID 后验证 T2'。回调先持锁只读核对 token/phase；stale/已完成立即 stale_attempt，不触发别的 generation 的屏障写入；真实当前操作才运行屏障。

reader 在稳定 sibling 排他锁下读取完整 snapshot，任何 normalization/auto-ID/写 API 之前严格验证。writer 只能在同锁及 SQLite BEGIN IMMEDIATE 顺序下执行已授权字段修改，temp fsync→rename→directory fsync；无法保留 raw 证据则失败。异常无法枚举时整个群文件操作 hold；可定位 hold 条目也不得被 typed 整文件重写抹掉。未改 entry 原始对象 bytes 不变；被改 entry 的所有未授权字段 value fragments 不变，特别是 relays 的 null/周围空白、event_json 的外层字符串片段及解码后 event bytes、已消费 result/witness 的完整 value fragments。可以重建授权的外层 envelope/分隔布局，但不能因此更改 receipt hash 范围；纯读/no-op/hold 连文件 mtime 都不刷新。

O3 的 RetryCount/status/LastAttempt/result 同生同灭；D6 的旧 entry 移除/新 entry/witness 同生同灭。写 commit-uncertain 仍持锁严格重读并确认目录持久性；无法确认则 bookkeeping_pending，不能重写一次试试、调用 P 或放行新 R。确认只留下旧 snapshot 无 receipt 时结果未知，不猜失败预算。未消费凭据挡住 generic clear/cleanup/DM 整文件 writer；已消费也只可在授权更新范围内保留，不能借“清理”删群 failed 证据。O2 仅 durable ACK=1、accepted_at>0、exact QueueID/冻结字段、唯一合法 entry、无未消费凭据时删这一 entry；冲突优先于 cleanup_pending。部署控制仍归 rev7 §3.6.1/F3/V7；合作锁/marker 不能排除任意旧 binary。

## 6. 共享 raw 示例与验收输入

以下为**拟议 schema 的共享文本 fixtures**，JSON 块可直接解析，禁止先 struct marshal 再作为 raw 测试输入。公钥来自 repo 已捕获的临时测试身份，不读取用户身份/私钥/真实 HOME。P-O3 是派生说明样例，不伪称 source capture 或本次已运行的 O3；原始 legacy source bytes 及 SHA256SUMS 继续以 `internal/messaging/testdata/raw-outbox` 为准。以下 schema 正例在本基线 F2a 仍会因为 metadata unsupported 被拒绝，这是预期缺口。

### P-O3：O3 已提交，T4 前崩溃

context：冻结 key 如 token；row queued/started，revision=15、generation=7、attempts=4、last_attempt_at=1791539070、relay_count=2、retry_queue_id=原 QueueID、retry_count=0、max_retries=10，last_result_generation<7，无本 token receipt。result 的 raw fragment 包括冒号后换行缩进及 `}` 后换行缩进。屏障应仅投影 T4 一次，预算 1/10，relays:null/event bytes 保留；后续 S 改 revision/targets 后再次读此已消费 result 仍 no-op。

```json
{
  "entries": [
    {
      "queue_id": "2fc72e7f8539e9eff9120c979198790b",
      "route": "group",
      "id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
      "event_json": "{\"kind\":30078,\"id\":\"3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906\",\"pubkey\":\"040f5835fd1b2e604e31d9bc3fe0b09e072762081350411a02c73c4a82fe745a\",\"created_at\":1791539064,\"tags\":[[\"p\",\"50780c4ce82ef325d6f45f53a30ef58deb07dfa9a95be290fdb460cbe4756f6e\"],[\"c\",\"agent\"],[\"z\",\"zstd\"],[\"v\",\"v1\"],[\"d\",\"agent-message:0123456789abcdef\"],[\"enc\",\"nip44\"]],\"content\":\"KLUv/UQAXADhCgBBdVBkUUphTmNaZGhtUElweGNhcWdhNDZPWDc3Qlc4VTlxMUxZckNhaXFCb1psdkFSN25rbjdXL0d1TVFJUXhVSGxZZ2VMQ2w1eE92NWpNSjdna0dtWXpqVUtCVVU0d285R2pnUmlteTZHMXVFYXdaeEIvczh2bEgxSHFDdU50ZEQwdTd4aVJGOG1aRzlQdnIrSWdSc1lva1JKYTVEU01QQUNWLzY0WUptYWltSVJOOUtBR1laaDRzYVBIOHZKUHorbXh6UmpUMGhzNjBvSEVHcHhMQ2NEQ0d0eVhscjVQMlFtLzN4Q2E5TUZNNDNsenhweUNSbUNXWW9QNTBoRGprUnBlT1BMYVVNazhQc1Q5U1lKMFZQODZ4Qkx6KzU2am1yaW9sci9MM1NOWkhqSWozbFV2bGhJR01GVGVSQ0F5bWpWQ04yMTZaQ0V1WVlESGNCTnJxSVdEMVBBPT2t+kXS\",\"sig\":\"e6323c249ac7bd6787d1a4c8165cff1047905f317eed027d1b7b1f5e249ced23e99bf377ef3a824bbf7d81542236fec38bda9faa0591b28486990aa1167682cc\"}",
      "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
      "relays": null,
      "retry_count": 1,
      "max_retries": 10,
      "last_attempt": 1791539070,
      "created_at": 1791539064,
      "status": "group_pending",
      "fanout_protocol": 1,
      "failure_result": {
        "version": 1,
        "token": {
          "fanout_key": {
            "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
            "group_id": "82bd2972cccd0403b59cdaa4412c6664",
            "envelope_type": "message",
            "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
            "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
          },
          "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
          "queue_id": "2fc72e7f8539e9eff9120c979198790b",
          "attempt_generation": 7
        },
        "result_id": "2397fad13c91fc597e59c7c97d4e8658dd3080187b0ccc9790b0d1f1cf967346",
        "expected_row_revision": 15,
        "attempts": 4,
        "last_attempt_at": 1791539070,
        "relay_count": 2,
        "prior_retry_count": 0,
        "retry_count": 1,
        "status": "group_pending",
        "issue": "send_failed"
      }
    }
  ]
}
```

### P-BUDGET-9 / P-BUDGET-10 / N-BUDGET-10：临界值与 crash/replay

以下为完整 raw failure_result 对象；各自替换 P-O3 的 failure_result，outer retry_count/status/last_attempt 同步取该对象后像，max_retries仍10，event/QueueID/null原片段不变；不是三个结果同时写同一entry。各自 context 的 row queued/started，generation、revision、attempts、S时间/targets取该对象，SQLite旧预算为prior_retry_count，last_result_generation低于token且未有本token receipt。

P-BUDGET-9：8→9<10，T4只投影 queued/send_failed、failure budget=9，generation/attempts不再加；O3后kill、T4前rollback、restart屏障及T4后再次kill均只能消费一次。

```json
{
  "version": 1,
  "token": {
    "fanout_key": {
      "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
      "group_id": "82bd2972cccd0403b59cdaa4412c6664",
      "envelope_type": "message",
      "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
      "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
    },
    "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
    "queue_id": "2fc72e7f8539e9eff9120c979198790b",
    "attempt_generation": 11
  },
  "result_id": "bd9f237f2f0209f7b136be137bb72fc4359c2f2177020caa1906a7d3eb29107e",
  "expected_row_revision": 28,
  "attempts": 9,
  "last_attempt_at": 1791539090,
  "relay_count": 2,
  "prior_retry_count": 8,
  "retry_count": 9,
  "status": "group_pending",
  "issue": "send_failed"
}
```

P-BUDGET-10：9→10==10，拟议 P4 映射为 failed/retry_exhausted；O3后kill重放一次，不能再R/P，用户显式D6才可新epoch。T4后重复屏障不得改变revision/issue或再加预算；后续D6 receipt保留在SQLite历史中，旧epoch结果不能写回新QueueID。

```json
{
  "version": 1,
  "token": {
    "fanout_key": {
      "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
      "group_id": "82bd2972cccd0403b59cdaa4412c6664",
      "envelope_type": "message",
      "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
      "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
    },
    "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
    "queue_id": "2fc72e7f8539e9eff9120c979198790b",
    "attempt_generation": 12
  },
  "result_id": "8ca91560ff84fac8818929a7d9db1e48950e23758a7baab22c66a2b619a1d408",
  "expected_row_revision": 30,
  "attempts": 10,
  "last_attempt_at": 1791539100,
  "relay_count": 2,
  "prior_retry_count": 9,
  "retry_count": 10,
  "status": "group_failed",
  "issue": "retry_exhausted"
}
```

N-BUDGET-10：只把上述完整对象的 issue 换成 send_failed，status/count仍耗尽；按条件修订提案 schema/一致性验证拒绝，不能T4“帮忙改成正确Issue”。

```json
{
  "version": 1,
  "token": {
    "fanout_key": {
      "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
      "group_id": "82bd2972cccd0403b59cdaa4412c6664",
      "envelope_type": "message",
      "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
      "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
    },
    "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
    "queue_id": "2fc72e7f8539e9eff9120c979198790b",
    "attempt_generation": 12
  },
  "result_id": "8ca91560ff84fac8818929a7d9db1e48950e23758a7baab22c66a2b619a1d408",
  "expected_row_revision": 30,
  "attempts": 10,
  "last_attempt_at": 1791539100,
  "relay_count": 2,
  "prior_retry_count": 9,
  "retry_count": 10,
  "status": "group_failed",
  "issue": "send_failed"
}
```

另一个raw negative在P-BUDGET-10原文本删除expected_row_revision，或仅将其30改为29但保持row30；前者schema拒绝，后者未消费bookkeeping_conflict。已经消费时，保持receipt原始value fragment不变而把当前row revision/targets推进是正例；若对已消费result只加空白使raw digest改变，则negative，不能因语义相等跳过精确receipt。commit-uncertain重读出现完整P-BUDGET-10仅在目录持久性确认后T4；只留下prior=9旧snapshot则outcome unknown、预算保持9，不能伪造这份result。

### P-MISSING：D6 已提交，T2' 前崩溃

context：row 为同 key/event 的 failed，revision=20、generation=7、旧 QueueID 如 witness；D6 开始时全 route 扫描无此 EventID，无 pending result，明确授权时间 1791539080。下列 new entry 的结构与归属合法；重放只采纳此 QueueID 一次，不生成第二个 epoch。

```json
{
  "entries": [
    {
      "queue_id": "00112233445566778899aabbccddeeff",
      "route": "group",
      "id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
      "event_json": "{\"kind\":30078,\"id\":\"3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906\",\"pubkey\":\"040f5835fd1b2e604e31d9bc3fe0b09e072762081350411a02c73c4a82fe745a\",\"created_at\":1791539064,\"tags\":[[\"p\",\"50780c4ce82ef325d6f45f53a30ef58deb07dfa9a95be290fdb460cbe4756f6e\"],[\"c\",\"agent\"],[\"z\",\"zstd\"],[\"v\",\"v1\"],[\"d\",\"agent-message:0123456789abcdef\"],[\"enc\",\"nip44\"]],\"content\":\"KLUv/UQAXADhCgBBdVBkUUphTmNaZGhtUElweGNhcWdhNDZPWDc3Qlc4VTlxMUxZckNhaXFCb1psdkFSN25rbjdXL0d1TVFJUXhVSGxZZ2VMQ2w1eE92NWpNSjdna0dtWXpqVUtCVVU0d285R2pnUmlteTZHMXVFYXdaeEIvczh2bEgxSHFDdU50ZEQwdTd4aVJGOG1aRzlQdnIrSWdSc1lva1JKYTVEU01QQUNWLzY0WUptYWltSVJOOUtBR1laaDRzYVBIOHZKUHorbXh6UmpUMGhzNjBvSEVHcHhMQ2NEQ0d0eVhscjVQMlFtLzN4Q2E5TUZNNDNsenhweUNSbUNXWW9QNTBoRGprUnBlT1BMYVVNazhQc1Q5U1lKMFZQODZ4Qkx6KzU2am1yaW9sci9MM1NOWkhqSWozbFV2bGhJR01GVGVSQ0F5bWpWQ04yMTZaQ0V1WVlESGNCTnJxSVdEMVBBPT2t+kXS\",\"sig\":\"e6323c249ac7bd6787d1a4c8165cff1047905f317eed027d1b7b1f5e249ced23e99bf377ef3a824bbf7d81542236fec38bda9faa0591b28486990aa1167682cc\"}",
      "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
      "relays": [],
      "retry_count": 0,
      "max_retries": 10,
      "last_attempt": 0,
      "created_at": 1791539080,
      "status": "group_pending",
      "fanout_protocol": 1,
      "recovery_witness": {
        "version": 1,
        "recovery_id": "102132435465768798a9bacbdcedfe0f",
        "fanout_key": {
          "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
          "group_id": "82bd2972cccd0403b59cdaa4412c6664",
          "envelope_type": "message",
          "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
          "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
        },
        "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
        "event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",
        "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
        "max_retries": 10,
        "expected_row_revision": 20,
        "expected_attempt_generation": 7,
        "old_queue_id": "2fc72e7f8539e9eff9120c979198790b",
        "new_queue_id": "00112233445566778899aabbccddeeff",
        "reason": "missing",
        "observed_absent": true,
        "authorized_at": 1791539080
      }
    }
  ]
}
```

### P-EXHAUSTED-PENDING / P-EXHAUSTED-FAILED：真实旧前像与条件 witness

这两个 old entry JSON 块**本身就是 old_entry_digest 的精确输入**：UTF-8 从 `{` 到 `}`，每级两空格缩进，块末 `}` 后换行不计。是 source fixture 的明确派生耗尽前像（无 protocol 的合法 legacy），不能与 source SHA256SUMS 混称。配对 witness 嵌入 P-MISSING 的新 entry，替换其中 witness；context 保持 failed revision=20/generation=7、max=10，D6 持锁扫描唯一同旧 QueueID 的该前像并原子移除。old count>10 派生变体也合法 exhausted，摘要必须随 bytes 重算。

旧 exhausted pending：

```json
{
  "queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "route": "group",
  "id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
  "event_json": "{\"kind\":30078,\"id\":\"3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906\",\"pubkey\":\"040f5835fd1b2e604e31d9bc3fe0b09e072762081350411a02c73c4a82fe745a\",\"created_at\":1791539064,\"tags\":[[\"p\",\"50780c4ce82ef325d6f45f53a30ef58deb07dfa9a95be290fdb460cbe4756f6e\"],[\"c\",\"agent\"],[\"z\",\"zstd\"],[\"v\",\"v1\"],[\"d\",\"agent-message:0123456789abcdef\"],[\"enc\",\"nip44\"]],\"content\":\"KLUv/UQAXADhCgBBdVBkUUphTmNaZGhtUElweGNhcWdhNDZPWDc3Qlc4VTlxMUxZckNhaXFCb1psdkFSN25rbjdXL0d1TVFJUXhVSGxZZ2VMQ2w1eE92NWpNSjdna0dtWXpqVUtCVVU0d285R2pnUmlteTZHMXVFYXdaeEIvczh2bEgxSHFDdU50ZEQwdTd4aVJGOG1aRzlQdnIrSWdSc1lva1JKYTVEU01QQUNWLzY0WUptYWltSVJOOUtBR1laaDRzYVBIOHZKUHorbXh6UmpUMGhzNjBvSEVHcHhMQ2NEQ0d0eVhscjVQMlFtLzN4Q2E5TUZNNDNsenhweUNSbUNXWW9QNTBoRGprUnBlT1BMYVVNazhQc1Q5U1lKMFZQODZ4Qkx6KzU2am1yaW9sci9MM1NOWkhqSWozbFV2bGhJR01GVGVSQ0F5bWpWQ04yMTZaQ0V1WVlESGNCTnJxSVdEMVBBPT2t+kXS\",\"sig\":\"e6323c249ac7bd6787d1a4c8165cff1047905f317eed027d1b7b1f5e249ced23e99bf377ef3a824bbf7d81542236fec38bda9faa0591b28486990aa1167682cc\"}",
  "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
  "relays": null,
  "retry_count": 10,
  "max_retries": 10,
  "last_attempt": 1791539070,
  "created_at": 1791539064,
  "status": "group_pending"
}
```

对应完整 witness：

```json
{
  "version": 1,
  "recovery_id": "2031425364758697a8b9cadbecfd0e1f",
  "fanout_key": {
    "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
    "group_id": "82bd2972cccd0403b59cdaa4412c6664",
    "envelope_type": "message",
    "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
    "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
  },
  "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
  "event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",
  "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
  "max_retries": 10,
  "expected_row_revision": 20,
  "expected_attempt_generation": 7,
  "old_queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "new_queue_id": "00112233445566778899aabbccddeeff",
  "reason": "exhausted_pending",
  "authorized_at": 1791539080,
  "old_entry_digest": "eade2d18cf3f488be9c4e79f7d138e8838086cffc597817b9ce14021bb7d1d9e",
  "old_status": "group_pending",
  "old_retry_count": 10
}
```

旧 exhausted failed：

```json
{
  "queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "route": "group",
  "id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
  "event_json": "{\"kind\":30078,\"id\":\"3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906\",\"pubkey\":\"040f5835fd1b2e604e31d9bc3fe0b09e072762081350411a02c73c4a82fe745a\",\"created_at\":1791539064,\"tags\":[[\"p\",\"50780c4ce82ef325d6f45f53a30ef58deb07dfa9a95be290fdb460cbe4756f6e\"],[\"c\",\"agent\"],[\"z\",\"zstd\"],[\"v\",\"v1\"],[\"d\",\"agent-message:0123456789abcdef\"],[\"enc\",\"nip44\"]],\"content\":\"KLUv/UQAXADhCgBBdVBkUUphTmNaZGhtUElweGNhcWdhNDZPWDc3Qlc4VTlxMUxZckNhaXFCb1psdkFSN25rbjdXL0d1TVFJUXhVSGxZZ2VMQ2w1eE92NWpNSjdna0dtWXpqVUtCVVU0d285R2pnUmlteTZHMXVFYXdaeEIvczh2bEgxSHFDdU50ZEQwdTd4aVJGOG1aRzlQdnIrSWdSc1lva1JKYTVEU01QQUNWLzY0WUptYWltSVJOOUtBR1laaDRzYVBIOHZKUHorbXh6UmpUMGhzNjBvSEVHcHhMQ2NEQ0d0eVhscjVQMlFtLzN4Q2E5TUZNNDNsenhweUNSbUNXWW9QNTBoRGprUnBlT1BMYVVNazhQc1Q5U1lKMFZQODZ4Qkx6KzU2am1yaW9sci9MM1NOWkhqSWozbFV2bGhJR01GVGVSQ0F5bWpWQ04yMTZaQ0V1WVlESGNCTnJxSVdEMVBBPT2t+kXS\",\"sig\":\"e6323c249ac7bd6787d1a4c8165cff1047905f317eed027d1b7b1f5e249ced23e99bf377ef3a824bbf7d81542236fec38bda9faa0591b28486990aa1167682cc\"}",
  "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
  "relays": null,
  "retry_count": 10,
  "max_retries": 10,
  "last_attempt": 1791539070,
  "created_at": 1791539064,
  "status": "group_failed"
}
```

对应完整 witness：

```json
{
  "version": 1,
  "recovery_id": "30415263748596a7b8c9daebfc0d1e2f",
  "fanout_key": {
    "local_npub": "npub1qs84sd0arvhxqn33mx7rlc9sncrjwcsgzdgyzxszcu7y4qh7w3dqw4y824",
    "group_id": "82bd2972cccd0403b59cdaa4412c6664",
    "envelope_type": "message",
    "send_key": "a52ffd60fa92310846b0cf5869a99fdd",
    "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj"
  },
  "event_id": "3ab652f4ab22a053e7c17b8b35ab99f903f5086a2b44c459ace8d81844662906",
  "event_json_digest": "eb40f2dcf24ad0f0cdf0992448498c2f570530e48f649dfbc8f1b92281452d99",
  "recipient_npub": "npub12puqcn8g9mejt4h5taf6xrh43h4s0haf49d79y8ak3svher4dahq9z8wdj",
  "max_retries": 10,
  "expected_row_revision": 20,
  "expected_attempt_generation": 7,
  "old_queue_id": "2fc72e7f8539e9eff9120c979198790b",
  "new_queue_id": "00112233445566778899aabbccddeeff",
  "reason": "exhausted_failed",
  "authorized_at": 1791539080,
  "old_entry_digest": "7940e2b23e374216cbbda81a7e5f219c1fde502659bd112c2ab8beafcc752254",
  "old_status": "group_failed",
  "old_retry_count": 10
}
```

legacy failed 从未有 QueueID 的 missing 正例：使用 P-MISSING，但 witness.old_queue_id=""，row.queue_id=""；其它字段完整，expected generation 可以为 0，不构造 generation=0 发布 token。prepared + source active pending/null 正例仅通过 T2 采纳后初始化预算，不假设已 queued。普通 DM/null 正例直接读取现有 `legacy-dm-nil-relays.json`，不增 protocol/result/witness；缺 QueueID 的普通 DM 派生正例也限隔离 DM 路径。

### N：语法合法的 raw negative 示例

下面分别是：root escaped duplicate；禁止的额外 root 成员；result/witness 显式 null 的字段片段；enc tag 数组片段。片段测试只验证所标层，不能冒充完整 outbox/event 签名正例。

```json
{"entries":[],"entr\u0069es":[]}
```

```json
{"entries":[],"version":1}
```

```json
{"fanout_protocol":1,"failure_result":null,"recovery_witness":null}
```

```json
[["enc","nip44"],["enc","future"]]
```

共享变异表：以 P-O3 / P-MISSING / 配对耗尽例的**原文本**定点编辑，保留未改 fragments；语法不合法者另列，不经 map/struct 重编码而丢掉重复成员。

| 输入（相对于正例的唯一变异，除特别注明） | 结果/真实验收 |
| --- | --- |
| P-O3 result 加 `"vers\u0069on":1` 或 token key 加相同 `event_id`；signed event 加 escaped 同名 kind | duplicate key 拒绝，即使值相同；两个 durable records 不变 |
| result / witness / token / key 加 `"extra":0`；signed event 加 `"extra":0` | unknown member 拒绝；不剥字段后继续 |
| 删除任一 mandatory member；任一字段 null；protocol 缺失但 result 存在；version/protocol=2 | schema 拒绝，不补默认值/降级 legacy |
| generation=0/-1/I64+1/1e0/"7"；count=1.0；observed_absent="true"/false | 类型/范围或 missing 条件拒绝 |
| P-O3 retry_count 改 2；prior>=max；status 改 group_failed 但新 count=1<10；issue=queue_conflict/retry_exhausted | 跨字段拒绝；不投影、不发布 |
| token key recipient 或 local_npub 改成另一有效 npub；entry EventJSON 与 row bytes 有空白差异但签名仍有效 | 自身绑定错误先 corrupt；只与 frozen row 不同走 queue_conflict，原证据保留 |
| result expected revision / S count/time/targets 改值且 row 为未消费 | bookkeeping_conflict；有精确 consumed receipt 时旧 S 与后续 S 差异本身不冲突；改 raw digest 则冲突 |
| missing witness 加 old_entry_digest/old_status/old_retry_count（含 null） | conditional 禁用成员拒绝 |
| exhausted witness 带 observed_absent（含 false/null）、缺旧摘要、old_status 不配 reason、old count<max | conditional/schema 拒绝，不假称 exhaustion |
| witness old_queue_id=new_queue_id；new ID 碰撞另一 DM 的 QueueID；missing old_queue_id="" 但非 legacy failed | 拒绝/hold，不替换；跨 QueueID 碰撞归 corrupt |
| witness digest 与冻结 event 不同；recipient/max 与新 entry 不同；新 epoch 非零 count/last_attempt 且未消费 | 自身/schema 或 bookkeeping conflict hold；已消费后真实 O3 count>0 不误判 |
| queued row QueueID=old、唯一 entry=new 且 exhausted；再加合法形状 witness | D14 queue_conflict 优先，两库/mtime不变、publisher/delete spies=0 |
| 同 EventID 第二条为 DM/unknown route/坏 group/failed；另 case 两 EventID 同 QueueID | 全 route duplicate / corrupt，不能 filter 后选一个 |
| legacy DM/group relays 缺失、[null]、route/queue_id null；group 缺 route/QueueID | raw 拒绝，不走 normalize/auto-ID；source relays:null 和其空白保持正例 |
| 已消费 witness + 新 QueueID result（新 generation） | 正例共存；先验 receipt no-op 再按当前 result 投影；两个未消费则 conflict |
| [] / null default-relay 样例，默认列表为空 | 沿用既有发送失败语义，不伪造 accepted；新条目 []、旧 null bytes 不变 |

语法 negative 另测：截断对象、非法 UTF-8、P-O3 后拼第二个 JSON 值；全部拒绝并保留原 bytes。enc 独立正例 `[["enc","nip44"],["enc","nip44"]]`、`[["enc","nip44","opaque"]]` 的完整事件测试必须先在临时测试身份下签名，然后 raw 验证，不能修改签名后把 tag 策略错误与签名错误混为一谈。

## 7. 实施拆片与独立验收

下面是 dispatch 单位，不是把 F2b 扩成整个 M1。每片生产 Go 新增+删除 **≤300 行**（表内预算更低；测试/docs 不计），提交前量实数，超预算继续拆，不能删验收凑行数。符号名字只作任务边界，冻结 API 归独立评审。全部先 inert/禁用，没有此文带来的 schema writer 或 S7/S8/S9 接线授权。

| 片 / owner / 生产预算 | 依赖与范围 | 必需可观察验收（未来任务，非已通过） |
| --- | --- | --- |
| F2b-1 / messaging / ≤200 | 本补充逐项独立评审；B-schema-1 条件修订待接受，parser仅可先inert；version/key/token/result parser，沿用 F2a tokenizer | P-O3 + 每 required/null/type/range/duplicate/extra 变异；result_id 重算；raw value fragments 完整；parser 无 I/O/身份生成/修改 |
| F2b-2 / messaging / ≤200 | b-1；witness common 与 missing/exhausted 两条件 schema | 三 witness 共享例 + old/new、recipient/max、digest/conditional 负例；legacy empty old ID 的结构接受与授权验证区分；raw witness bytes 保留 |
| F2b-3 / messaging / ≤180 | b-1/b-2；entry metadata relations、version gate、enc 决策测试，完整跨 route 枚举保留 | DM metadata拒绝、protocol缺/未知、合法 consumed 共存形状与不可自证授权；签名 enc duplicate/extra 正负例；原 F2a fixture/hash 套件；不接 writer |
| F2c / messaging / ≤150 | 完整 F2b 评审/合入；稳定 sibling 锁 raw reader | 两 OS 进程 snapshot一致/锁阻塞；raw错误不 normalize/自动 QueueID/写文件；只读mtime不变；unsupported locking平台禁用 |
| F2d-1 / messaging / ≤250 | F2c；raw patch 与 fsync/rename primitives，仍无生产新schema启用 | 授权字段之外 entry/value bytes相同；旧 relays空白/null与event_json保持；consumed fragment digest不变；rename前失败保持旧snapshot、rename后不确定明确上报 |
| F2d-2 / messaging / ≤220 | d-1；精确条件 O3/D6 serialization primitives（不自授 SQLite授权） | O3 result+count/status/time同提交；D6 remove+new+witness同提交；旧 raw digest含null；新默认[]；duplicate/QueueID冲突零写 |
| F2e-N / messaging / **每个 caller批次 ≤250** | d-1/d-2 + F3授权接口评审；按既有rev7 writer inventory迁移typed callers、clear/cleanup/adoption，逐批列完整剩余caller | 每批真实调用链与 held/unconsumed保护、DM default-relays/null回归；不能宣称整个F2完成直到inventory零遗漏及V6跨更新通过 |
| F3-result / groupchat+messaging / 拆成 lock/fence、T4-replay、uncertain-commit **每片 ≤250** | F1验收、完整F2；沿用rev7 F3，不新增S7 | V2双OS进程A/B迟到 failure/ACK两库逐字段不变；V3 O3后SIGKILL、SQLiterollback、重放一次；已消费receipt跨后续S/T3不冲突；uncertain阻止R/P |
| F4-witness / groupchat / 拆成 D6-authorize、T2prime-replay **每片 ≤250** | 完整F1/F2/F3；沿用rev7 F4共享验证 | V4三reason实际D6、T2'前kill/restart、hookrollback、第二次不reset；D14不同QueueID含exhausted两库/mtime不变且publish/delete=0；V5优先级相关矩阵 |

F3 受控部署/完整 writer gate 的 V7 仍是既有计划的独立必交项，不因上表只展开 result/witness 部分而免除。每个实现 PR 提供 predicate→真实测试名/输出、生产行数、未覆盖项；定向 tests 与 go test ./... 按原计划完成，OS进程/kill/fsync故障证据不能用 -race 替代。F2b parser pass 不能关闭完整 F2/V6，F1合入也不能自动关闭F1验收。

## 8. 待裁定项与 blocker 记录

新增选择清单：P1 enc 同值重复/字符串后缀接受且对象 extra 拒绝；P2 完整 key/token 布局、canonical wire identity/opaque IDs及QueueID保留；P3 摘要输入范围、编码与token派生result_id/随机recovery_id；P4 非耗尽send_failed/耗尽retry_exhausted的显式条件修订提案，B-schema-1等待独立接受；P5 witness具体成员及条件/重复recipient一致性；P6 新epoch created_at。表内 counter词法/range沿用F2a，version=1及optional仅省略沿用rev7，非新增null豁免。所有P项为**proposed decisions awaiting independent review**，不得以本文存在就称为schema freeze获准。

**真实 blocker B-schema-1**：§2 耗尽验收 retry_exhausted 与 §3.5 current-O3 send_failed、§3.3 T4 issue 投影的组合互斥，详见 P4 的单次耗尽反例。coordinator 已确认并提出 P4 的最小条件修订，仍等待独立接受及原契约修订；本补充未改第三份 rev7 文件，不偷偷覆盖其语义。其余“生成一个规范enc”与兼容reader同值重复、旧relays:null与新条目[]、旧消费凭据与后来S不同revision有不同作用域，不算契约矛盾。代码现状与目标的差距仍为实现blocker；评审若发现其它真实矛盾，登记具体条款/反例并阻塞受影响实现，不允许实现者静默选择一边。

仍未关闭：B-schema-1 exhausted issue裁定；本补充逐项独立评审；F2b实现；完整F1/F2/V6验收；F3跨存储fence/barrier/全部writer与受控部署V7；F4来源witness/幂等恢复验收。coordinator为独立reviewer，外部PR-Daemon执行后续acceptance；本任务只提交两份docs的本地commit，不push/PR/merge、不运行发布或新schema写入。S7/S8/S9继续等待这些gates。
