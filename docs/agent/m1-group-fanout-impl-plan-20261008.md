# M1 群 fanout + durable send-intent 实现拆分方案（P0-E）

状态：**rev7 设计评审 PASS（2026-10-09；仅文档/设计层）**。依据[rev7 契约](m1-group-fanout-contract-20261008.md)；本文不是已实现能力声明。S7 与受影响的 S8/S9 接线仍 **BLOCKED，直到 F1–F4 分别评审/合入并通过其验收门槛**。本计划只拆分后续工作，不修改 Go、测试或已批准的 #165/#166。

## 0. 精确基线与已实现范围

- 工作树 HEAD / PR base：`699b36f74c6b3802b93768d670229d7b2d3b6cff`，包含 S2（#156）、S3（#159）、S5a（#160）、S10a（#157），以及 #145 verified 路由、#151 keystore API；不再沿用旧计划的 `6c5e751` 状态。
- 单独检查 approved S6 #165：`c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b`；`fanout_queue.go` 的 queueTargets 先入 JSON 再 state CAS，BeforePublish 只检查 accepted，RecordAttemptFailure 增加 attempts。**没有 generation reservation/token 或 recovery witness**。
- 单独检查 approved S5b #166：`7861d029b5ccc2959b80a184a390ec132ddd025f`；`outbox.go` 的 handler 三个签名均无 generation；recordGroupAttemptFailure 先执行 O3 的 RetryCount/status 写，再回调 T4。仅加 callback fence 不足以防 stale O3。
- base 的 [fanout.go](../../internal/groupchat/fanout.go) 与 `store.go:beginImmediate` 使用 SQLite；[outbox.go](../../internal/messaging/outbox.go) 的 UpdateOutbox/writeOutbox 使用独立 JSON 文件锁/fsync/rename，二者不共 DB/事务。`readOutbox` 会 normalize 空 Route；`currentOutboxAttempt` 可补 QueueID。base 的 [outbox_group.go](../../internal/messaging/outbox_group.go) 已排除 exhausted pending，requeue 能替换它，但 GroupOutboxEntriesByEventID 不是全 route 证据扫描。
- 两个 approved head 的 tracked docs 仍为 rev3。rev5 临时档案只作设计历史，不能用来声称这些 head 已满足 rev7；具体源码/API 对照见契约「实现状态与精确基线」。不得在 #165/#166 追加提交；其合入由 coordinator 单独处理。F1–F4 必须在包含这两个批准对象或经独立核验等效合并内容的 main 上另开 follow-up。
- base 尚无 S1 BuildAgentMessageEvent、S4 PrepareMessageFanout、S7 ReconcileFanout、S8 provider、S9 CLI；此处不推断任何其他未检查 PR 的状态。G1 已由 S2 关闭；G2 尚有 queued bool，留 S10b；D8 已选 `hyphae groupchat`，D13 已定 keystore 语义，无需再次产品拍板。

## 1. 实施约束与交付门槛

保留原拆分约束：每个代码 PR 的生产 Go 新增+删除 ≤300 行（测试/文档不计），超过先拆，绝不以一个巨大 follow-up 掩盖复杂度；不得超过原硬上限 500。F1–F4 是依赖门槛，可各拆多个 ≤300 行 PR，**该门槛所有子片合入才算完成**。不沿用旧计划「总共约 2,065 行」的估算，新增协调协议须重新计量。

实现者与独立评审者分开；未来实现 PR 按项目既定 PR-Daemon 流程检查，定向验收与 `go test ./...` 通过后才可请求合入。PR 描述必须列生产行数、具体测试/输出、未覆盖范围。文档 PR 不运行或修改共享 PR-Daemon；approved #165/#166 保持不变，follow-up 可显式修订其调用路径，旧计划的「后片只新增不重写」不适用于本次经评审的安全补强。

新增能力先受禁用 gate 保护；不能在 F1–F4 未完成时暴露群发布或新 schema writer。rev7 对 B1/B2 的设计修订已获设计评审 PASS，但 S7 仍 **BLOCKED**。approved #165/#166 没有未来 strict raw 验证、generation fencing 或 witness；其 legacy group/DM nil relays 可合法序列化为 null，不能按损坏拒绝。

**B1 选择有条件的受控部署，不宣称任意 same-HOME 旧 writer 会被拒绝**。契约 §3.6.1 是 F3 的完整规范：唯一 installation owner（管理员或单用户 HOME 所有者）控制 HOME、安装树、所有 launcher/automation/更新权，全部受支持操作仅经登记入口。任意 copied same-UID binary、绕过入口的手动 SQL/文件写、恶意 owner 在支持/威胁模型之外；本方案未提供阻止这些行为的 OS/存储隔离。无法盘点或持续控制的 HOME 不可激活，S7 在该部署持续 BLOCKED；若需支持任意同 HOME 程序，须先另审存储/UID 隔离方案。

F3 的交付不能只是一句升级提示或一次 process scan：

1. 完整 writer/caller/路径清单：SaveOutbox/writeOutbox 整体替换、UpdateOutbox 全调用链、currentOutboxAttempt、clear/cleanup、status/retry/删除、DM enqueue/send 清理、group enqueue/requeue/handler；以及 SQLite/group migration、transitionFanoutTx、T1–T4、R/S、D6/T2'、reconcile/control activation。映射每项源码→executable→launcher→canonical HOME/outbox/DB（含 sibling/temp/WAL/SHM、别名/挂载），核查 owner/权限，包含直接存储 helper 和恢复脚本；这是未来完整审计的交付物，本文不宣称已盘点完毕。
2. 每个受支持启动面都归固定绝对路径 launcher：CLI 的 PATH/alias/function/命令缓存/登记绝对路径；桌面/IDE TUI 与 worker；daemon 的 launchd/systemd 服务与自动拉起；cleanup、cron/timer、脚本、CI/IDE automation 和持久化库调用程序。launcher 的清单固定唯一新版本绝对路径/hash、HOME/stores/owner/启用状态，禁止经 PATH 或参数选旧 binary，缺项/失配/closed 在打开 stores 前拒绝；旧入口移除或仅转发此 launcher。
3. 关闭全部入口和自动重启→等待/终止所有已登记旧进程及子进程、检查退出与打开文件→静止后一致备份 JSON/SQLite/WAL→退役全部登记 legacy executable/包链接/构建输出/脚本旧路径，清 shell 缓存→绑定经评审的新版本。入口保持关闭，仅受控新版本维护进程迁移/raw 审计/adoption；V7 验证后冷重启 daemon/TUI/automation，先 recovery barrier 后写入。不能用 outbox.lock、daemon.lock 或 marker 代替该流程。
4. 激活后每次 managed launch 校验固定 tuple；owner 持续维护 inventory、安装权限和启动定义，自动升级/降级也必须关入口、停全部长驻 writer 后变更，禁止活进程旁切版本。发现控制/owner/hash/路径漂移即关闭入口并停止 writer/调度，保留证据重新盘点；不能声称停新程序可撤销越界旧写。重启/升级中断默认 closed，同一允许的新版本在维护模式校验恢复后才开放。
5. 禁止旧版本原地打开 live stores 回滚。激活前只有全停后恢复完整迁移前备份；激活后若要退回旧版，须单独离线方案退役 live HOME/入口、在隔离旧 HOME 恢复完整迁移前快照，承认后续本地状态丢失与网络发布不可撤销；不混用新旧 JSON/DB。无法核实 inventory/ownership/control 或恢复路径则保持关闭，不写新 schema、不发布、不启动 S7。

这些控制尚未实现；F3 owner 必须交付安装/launcher 与验收，F1/F2 的禁用 gate 不能绕过它。受控入口排除旧版本的结果不得报告为 arbitrary-writer exclusion。

## 2. 切片、owner、依赖与可观察验收

owner 是责任角色，coordinator 派发时指派具体人；不能把跨包 API 缺口留作无人负责的 S7 内部细节。以下测试名是未来验收要求，**不是已有测试或已通过结果**。V1–V7 的完整断言在契约 §3.7。

| 片 / 状态 | owner 与范围 | 依赖（全部先合入） | 可观察验收 |
| --- | --- | --- | --- |
| S1 / base 未有 | messaging owner：加密路径 BuildAgentMessageEvent；明文/auto-reply 不动 | 已有 #151 | 每 recipient 独立加密；旧 DM 构造行为回归 |
| S2 / base 已有 | groupchat store owner：storeMessageTx | 已合入 #156 | 保留已有 Tx 回滚测试 |
| S3 / base 已有 | groupchat store owner：fanout 表/状态/report | 已合入 #159 | 现有测试基线；不宣称有 generation CAS，F1 负责补强 |
| S4 / base 未有 | groupchat owner：PrepareMessageFanout / prepareFanoutTx，T1 | S1、S2、S3；新增 row 初始化对齐 F1 | 签名/历史/fanout 同事务；重入不签名；冻结 event、相同 created_at |
| S5a / base 已有 | messaging owner：route/status 与基本 requeue | 已合入 #160 | 现有 route/DM 测试；strict raw、见证和 writer 协调由 F2–F4 补 |
| S5b / approved #166 | messaging owner：现有 routed handler 分派 | coordinator 合入 exact approved #166 | 保留 approved head 测试；无 token API 不算新安全边界 |
| S6 / approved #165 | groupchat owner：现有 O1/T2/handler/requeue | coordinator 合入 exact approved #165 | 保留 approved head 测试；其 ABA/O3 缺口由 follow-up 修复 |
| **F1 / 必需 follow-up** | **groupchat store owner**：新增 row_revision、attempt_generation、phase、结果/恢复水位与精确凭据摘要 receipt、retry budget 快照、accounting_origin/legacy_attempts_base；nullable budget/report；严格 allowlist/CAS/报告；修正 relay_count 的 MAX | S3、S5b、S6 | V1：迁移不伪造旧 token/预算；reservation 不增 attempts；冻结字段/负数/溢出/共同时间拒绝；relay_count 3→1 合法；终态不变 |
| **F2 / 必需 follow-up** | **messaging persistence owner**：raw tokenizer/版本化 schema、全 route/EventID/QueueID 枚举、无损 writer、failure_result/witness 序列化、锁内精确条件写；禁自动 group Route/QueueID 修复 | S5a、S5b、S6；字段定义与已评审 rev7 一致 | V6 与 V5 原始 fixtures；异常 bytes 保留；跨 route duplicate；GetPending `<` 与 `>=`；未知/重复 keys、非法字段 null/缺失在 normalize/auto-ID 前拒绝；legacy DM/group relays:null 无损兼容，新条目默认 []；源路径 fixture/字段规则见 §4 |
| **F3 / 必需 follow-up** | **groupchat + messaging 协调 owner（一个负责人）**：WithGroupOutboxEvidence、Reserve/Start、CommitFailure 包住 O3→T4、CommitAccepted/CleanupAccepted；恢复屏障；审计所有 writer/锁顺序；契约 §3.6.1 的受控安装/launcher/持续运维启用 gate；只读 InspectFanoutEvidence overlay | F1、F2、S5b、S6 | V1/V2/V3/V7：真实双进程同 QueueID A/B 晚失败两库零变化；O3 后 kill 只投影一次；uncertain commit 阻止新 R；S 后 P 前结果未知；V7 逐入口 legacy 排除、writer/路径清单、停机升级重启及漂移/回滚 fail closed（限受控部署） |
| **F4 / 必需 follow-up** | **groupchat recovery owner**：RecoverFailedGroup 协调 D6；原子 witness + 新 queue；T2' CAS/幂等；消费屏障；共享证据分类/diagnostic 类型 | F1、F2、F3 | V4/V5：missing、exhausted pending、exhausted failed；D6 后 T2' 前 kill；无 witness/同旧 QueueID 拒绝；queued+不同 QueueID exhausted 零 mutation/publish/delete；重复 D6 不重置预算 |
| **S7 / BLOCKED** | **reconciliation owner**：纯 decideReconcile(raw evidence,row) 与锁内执行器；调用 F3/F4 共享验证/恢复 API，不自造第二套规则 | **rev7 设计评审 PASS + F1、F2、F3、F4 全部合入**；S6 | V5 完整 first-match 矩阵；V2–V7 集成故障/并发；orphan 仅无 row；accepted exact O2、冲突保存、cleanup_pending；第二次 durable 状态幂等 |
| S8a / BLOCKED | app/daemon owner：provider 定义/注册；daemon 与 storage outbox retry 经屏障和新 routed API；generic clear/cleanup 不能绕过 F3 | F1–F4、S7、S5b | 未注册不发送；expired/held 不调 P；GetPending 全量扫描分离；DM 回归；清理/重试并发证据保存 |
| S8b / BLOCKED | TUI owner：复用同 provider 的 outbox retry；每轮屏障 | F1–F4、S7、S8a | 重启持久报告一致；缺 handler/坏证据不发；双进程与 daemon 并发 |
| S9 / BLOCKED | groupchatcli owner：send/retry/status、FanoutReport 展示、终端转义 | F1–F4、S4、S7、S8a 的 provider、S5b | send 先 reconcile；retry 经 F4；status 只读；publish starts/本 QueueID failures/legacy/unknown 明确；1/2 部分结果非零退出；JSON 字段齐全 |
| S10a / base 已有 | groupchat store owner：控制 Tx 变体 | 已合入 #157 | 保留公开行为及回滚测试 |
| S10b / 待实现 | control fanout owner：*WithFanout；MarkActivationQueued 去 bool；onFanoutQueuedTx | S10a、S4、S6、F1–F4 | 控制状态与 T1 同事务；activation 与 T2/T2' 同事务；失败全回滚，G2/G4 关闭 |
| E2E-A | integration owner：真实 relay 发送侧 | S8a、S9；V1–V7 已过 | 三隔离 HOME；不可达 0/2→恢复 2/2 event 不变；注入单 recipient 失败 1/2；P 前 crash 后恢复不重复历史 |
| E2E-B | integration + P0-F owners：完整三人生命周期 | E2E-A、S10b、P0-F、create/accept CLI | 邀请→接受→激活→三人各发 3 条→重启 walk，9 条历史、0 群 DM 行 |

F1/F2 可在接口设计获评审后独立实现；F3 接合前不得接线。建议超限拆分：F1 schema/migration 与 CAS/report；F2 raw decoder 与保持 bytes 的 writer；F3 锁/API、结果日志/屏障、全 writer/gate 审计；F4 witness/D6 与分类/报告。每个子片独立禁用、可测、可评审；不得以拆片遗漏总体验收。

## 3. 顺序与生产启用

```text
rev7 contract/plan design review PASS (docs only)
  + exact approved #165/#166 separately merged (no appended commits)
  ├─ F1 schema/CAS/accounting ─┐
  └─ F2 raw evidence/writer ──┴─ F3 O3 fence + replay barrier + enable gate
                                  └─ F4 witnessed recovery
                                      └─ S7 reconciliation
                                          └─ S8a provider/daemon/CLI-outbox
                                              ├─ S8b TUI retry
                                              └─ S9 user send/retry/status
S1 + existing S2/S3 + F1 initialization → S4 T1
S4 + existing S10a + F1–F4 → S10b control fanout
S8a + S9 → E2E-A; E2E-A + S10b + P0-F + lifecycle CLI → E2E-B
```

S7 不能为了“先写纯决策”而绕过 F1–F4 gate；先实现/评审这些 follow-up 才能把实际持久证据作为 S7 输入。S8/S9 受影响 wiring 也不能先接无 token #166 路径。只读 UI 草图不等于接线完成。F4 的少量分类/恢复原语是 prerequisite，不是偷做 S7 全量扫描/执行器。

没有把群 outbox 搬进 SQLite，也没有第二套 publisher；选择保留现有 JSON 与 SQLite，新增协调提交边界。P 不持锁，无法与本地事务形成原子提交；S 后 P 前崩溃计一次 publish start、结果未知，P 后 T3 前崩溃可重发原 event。这个已选语义必须由评审确认，不可写成“精确网络发送次数”。

## 4. 两份文档共用的数据与操作口径

| 项 | 唯一定义（契约对应） |
| --- | --- |
| R / generation | attempt_generation +1；只耗 generation，attempts/RetryCount/last_attempt_at/relay_count/Issue 不动（§3.1） |
| S / attempts | 当前 reserved token CAS，attempts +1、phase=started、记录 S 时间/targets，清旧发送失败 Issue；提交后 P；S 后 P 前 crash 也计一次启动（§3.1/3.3） |
| O3 / RetryCount | 当前 started token 通过锁内 CAS 后仅有效 P 失败 +1；与 failure_result/status 同次 JSON 提交；T4 只投影不再加（§3.3） |
| 两存储边界 | outbox lock→SQLite BEGIN IMMEDIATE；P 解锁；O3 与 T4 不是真原子事务，结果日志 + 强制屏障补完（§3.3） |
| O3 crash | 凭 token/revision/精确 receipt 与结果水位重放 T4，一次；已消费结果不与后续 S 的可变计数字段比较；先恢复再允许任何新 R/D6/修改，不能仅 callback fence（§3.3） |
| D6 | 显式授权；missing/同旧 QueueID exhausted 两状态可恢复；新 QueueID+RetryCount=0+耐久 witness 同次 JSON 提交，T2' CAS（§3.4） |
| D6 保留值 | attempts、attempt_generation、last_attempt_at、relay_count、历史水位保留；MaxRetries 冻结；T2' 清 Issue/phase=idle/新预算（§3.1） |
| raw | normalize/auto-ID 前字段级严格 JSON；全 route 枚举；群 Route/QueueID 不补；普通 DM 空/缺 route/QueueID 例外与 DM/group relays:null 例外分别定义，见本节与契约 §3.2 |
| deployment | 契约 §3.6.1 / 本文 §1 的持续受控安装前提；合作锁不排除旧 binary；任意 copied same-UID binary 不受阻止且不在支持模型内，前提不成立则 S7 BLOCKED |
| exhausted | GetPending 仅 `< MaxRetries`；`>=` 不发布，先同队列判失败再显式 D6；不同 QueueID hold 优先（§3.2/3.5） |
| D14 | queued+不同 QueueID：两边保留、零 mutation/publish/delete；不能 queued→failed→queued（§3.5） |
| diagnostics | first-match primary；重复/损坏/冲突不重叠；orphan 仅无 row；不覆写 RecipientDelivery.Issue；accepted 精确清理不确定为 cleanup_pending（§3.5） |
| report/UI | publish starts 与当前 queue failures r/M 分开；legacy 基数单列，native starts=attempts-legacy_attempts_base；nullable budget、只读 evidence overlay、unknown outcome、pending bookkeeping 独立显示；generation 仅诊断（§3.1） |

**B2 字段与 fixture 口径（F2/V6）**：契约 §3.2 字段表为唯一 raw schema。DM/group 的 required `relays` 可为字符串数组或 legacy null，二者零长度均选 defaultRelays；null 原始片段及 event 全部原始 bytes 在 adoption、升级、O3 与 recovery projection 中不变，补 protocol 后仍可保留 null。这是未来无损 writer 的字段保留要求，非现有 typed writer 的能力；授权修改外层计数/metadata 不要求整份 JSON envelope 字节不变。新 writer 新建默认选择 entry 必须 []；D6 新 QueueID 是显式替换，用 [] 且 witness 取包含旧 null 的原条目摘要，不改 event。缺 relays、relays 内 null/非字符串仍拒绝。

required identity/event/status、group Route/QueueID、计数/时间拒绝 null/缺失；普通 DM route/queue_id 仅空字符串/缺失兼容，显式 null 不兼容。protocol 仅 legacy group 可缺，出现须为受支持整数。optional result/witness 只能省略，出现须为完整合法对象，不能 null；内部必需字段不得 null/缺失。witness missing reason 需 observed_absent=true 且省略旧摘要/status/RetryCount，exhausted reason 则要求三者并省略 observed_absent；old_queue_id 必须字符串，仅无历史 QueueID 的 legacy failed 可空。report 指针 RetryCount/SQLite nullable 预算的 null=unknown 不允许反向套到 outbox counter/token；未知字段/版本、重复 key（含转义同名）、类型校验一概不放宽。已消费 result/witness 原始片段也须保留到授权替换，防后续更新改变 digest。

| F2 必交的原始 fixture（源路径复制，非新版 marshal） | V6 指定结果 |
| --- | --- |
| `legacy-165-group-nil-relays`：exact #165 `c0a54111f78f357c6ae2772f06f9a3cc4fe7c63b` queueTargets→enqueue(target.eventJSON,target.recipient,nil,target.maxRetries)，复制隔离 HOME 落盘原 JSON/对应 row/event/hash；原生非空 QueueID、route=group、relays:null、retry_count=0、last_attempt=0、group_pending | matching queued upgrade/adoption 合法，当前 queue 预算 0/M；prepared O1→T2 fixture 经 T2 才投影预算；有效失败 O3→kill→T4 replay 只计一次，null/event 不变；耗尽派生 fixture D6 新 [] + witness 后 kill/T2' 幂等；failed+active pending 无 witness 仍 hold |
| `legacy-dm-nil-relays`：同 exact #165 outbox.go:enqueueOutboxEntry(nil)，与 base 相同源路径；复制落盘原 bytes，relays:null、retry_count=0、max_retries=10、last_attempt=0、pending、route 省略、源 QueueID 保留 | 接受为 DM defaultRelays，不写群 protocol/witness/预算或 fanout；DM 重试语义不变且保留 null/event；单列旧 DM 缺 QueueID 派生 fixture，不把例外扩到 group |

fixture 获取与所有断言都是未来 F2/V6 工作，本次文档变更没有执行或伪造完整签名测试数据。approved #166 `7861d029b5ccc2959b80a184a390ec132ddd025f` 已用零长度 relays 选 defaultRelays，但没有未来 raw validator。V6 从上述源 bytes 定向变异缺字段/null/重复/未知键，验证先 raw 后 normalization；不能用当前测试 helper 的简化 event 冒充符合全部 signed-event 约束的源 fixture。

## 5. 验证清单与命令

未来实现者应提交「契约 predicate/崩溃点 → 测试名」映射，不能只给一个绿色 package。所有测试使用临时 HOME/DB，不触碰真实用户数据。

| 验收 ID | 最小可观察证据 | 责任切片 |
| --- | --- | --- |
| V1 | 预留后 P 前 crash 的三计数、时间、Issue、targets；另测 S/P gap；迁移 legacy；report/UI 不混预算 | F1/F3、S9 |
| V2 | 两个 OS 进程同 QueueID A/B；A 迟到 failure/ACK 不执行恢复写，故两 durable records 不变；outbox 锁内 fence 无 TOCTOU | F3、S7 |
| V3 | O3 fsync/rename 后 T4 前 kill；重启只加一次 RetryCount，T4 后重复也一次；write uncertain hold | F3、S7 |
| V4 | missing/exhausted_pending/exhausted_failed 的 witness；D6 后 T2' 前 kill；same/new QueueID、无 witness、冲突优先 | F4、S7 |
| V5 | 全状态/条目数/预算/QueueID/raw/result/witness 矩阵的唯一 primary；held prepared/failed；清理待定/成功/冲突 | F4、S7 |
| V6 | §4 两个源路径 null-relays fixture/hash；adoption/预算/O3/recovery/DM defaultRelays；新条目 []；字段级 null/optional 验证、raw relays/event/已消费凭据保持；错误字段、duplicate/unknown keys、全 route 冲突在 normalize/auto-ID 前拦截 | F2 |
| V7 | §1 与契约 §3.6.1/§3.7：完整 writer/caller/JSON/SQLite/group 路径与 launcher inventory；逐入口 activation/O3→T4/D6→T2' legacy 请求被拒绝/不存在/仅转发新版本，实际 exec/hash/访问追踪证明旧程序不打开 live stores；升级重启/失控/回滚 fail closed；另测全部持久边界、锁顺序、0 群 DM 行 | F3 部署 gate 必须先于 S7；F4/S7/S8/S10b 续测协议集成 |

建议定向命令（测试随所属片新增后执行）：

```sh
go test ./internal/groupchat -run 'TestFanoutMigration|TestFanoutAccounting|TestFanoutCAS' -count=1
go test ./internal/messaging -run 'TestRawOutboxEvidence|TestGroupOutboxCollision|TestOutboxEvidencePreservation' -count=1
go test -race ./internal/groupchat ./internal/messaging -run 'TestGroupAttemptFence|TestGroupFailureReplay|TestGroupRecoveryWitness|TestGroupWriterGate' -count=20
go test -race ./internal/groupchat -run 'TestReconcileFanout|TestDecideReconcile|TestFanoutCrashPoints|TestFanoutConcurrentRetry' -count=20
go test ./internal/messaging ./internal/daemon ./internal/tui -count=1
go test ./internal/groupchatcli -count=1
go test ./...
```

`-race` 只检查进程内 data race，不能替代子进程 flock/SIGKILL/V2/V3/V4 测试。定向命令必须覆盖两种 JSON 提交结局与 SQLite rollback，且原始 JSON fixtures 不得先用 struct marshal 丢掉错误证据。生产符号 API 在对应 follow-up 独立评审冻结，不能在 S7 私改。

E2E-A 使用真实 relay `wss://relay.aastar.io` 和 alice/bob/carol 三个临时 HOME：relay 抓包无正文/群名/roster 明文；全部 relay 不可达发送后换 relay 重试保持 event；单收件人失败通过 fake publisher 注入，不能用收件人离线伪造 1/2。E2E-B 还依赖完整 inbound。`test.sh` 是否纳入新增包由相关实现片核查并补齐，本文不声称已经接入。

## 6. 七项发现与剩余限制

| review finding | 决策/契约交叉引用 | 关闭者与验收 |
| --- | --- | --- |
| 1 | approved 无 token；独立前置，不 amend（实现状态、§3.6） | F1–F4 + 独立评审 gate |
| 2 | O3 实际变更持锁 fence + JSON attribution + T4 恢复（§3.3） | F2/F3，V2/V3 |
| 3 | recovery witness 与 D6 原子；missing 是当前授权，不虚构历史（§3.4） | F4，V4 |
| 4 | generation / publish starts / failed budget 三分；crash accounting（§3.1） | F1/F3/S9，V1 |
| 5 | exhausted pending 独立分类，D14 高于恢复（§3.2/3.5） | F2/F4/S7，V4/V5 |
| 6 | ordered primary mapping，diagnostic 与持久 Issue 分开（§3.5） | F4/S7，V5 |
| 7 | raw 先验、全 route、阻止 auto QueueID、明确 DM 例外（§3.2） | F2，V6 |

**B1/B2 的设计修订已获 rev7 文档评审 PASS；实现验收仍待完成**：B1 只在 §1 的受控安装前提成立时可用，任意同 UID 绕过不受阻止；B2 允许 legacy relays:null 并保持 bytes，完整源 fixture 与运行验收归 F2/V6。设计不依赖虚构的 recovery 来源，但确实依赖明确的部署控制；不满足前提就是阻塞。其余剩余限制是这些 API/schema/锁协调/升级 gate **尚未实现且未获独立实现评审**，不是当前 source 已保证的性质；实现评审若不接受跨存储恢复协议，必须另行决定共享事务存储方案并先修订契约，不能用现有 callbacks 代替。S7/接线在这些 gate 关闭前持续 BLOCKED。
