# Agent24 共享资料/查询候选验收任务

状态：根代理准备，跨仓实现由用户推动。Hyphae PR #96 固定 head `094d8d1cea63a538b10a0eff403090fa3f32099e`，已通过真实双平台 CI；候选尚未冻结。正式通信 CLI 接线可并行推进，不以本任务代替 CLI 或 UI 验收。

## 输入与完整性

仅使用固定提交中的以下文件，不追踪可移动分支，不重算或改写样例预期：

| 文件 | SHA-256 |
|---|---|
| tests/contracts/testdata/public-query.schema.json | 41e56895558c9723ec86fc9e1e46472803eefb3328d691d2bc290899e575c48c |
| tests/contracts/testdata/public-query-fixtures.json | 5139728518dc0be5db28eb9f5a3cb40198874d0d7a27fa57e9d232731f731a72 |

参考说明：tests/contracts/PUBLIC-QUERY-SCHEMA.md；Go 参考入口：tests/contracts/public_query_schema_test.go。依赖设计固定 #92 `94ad0e05` 和 #95 `bdeff57a`。严格 JSON 输入策略为独立 #91 `55982f24` 候选，其词法前置不能由 JSON.parse 或普通 schema 校验替代。

## Agent24 小任务

1. 独立 worktree 的测试提交固定原始 schema/fixtures，保留 Apache/NOTICE 及来源记录；只引入测试参考，不启用生产新协议或旧 bridge 执行路径。选择与消费端语言匹配的 Draft 2020-12 validator，固定版本及锁文件。具体库由对应仓库按现有依赖选择，不能默默换 draft 或忽略 format。
2. 注册并断言四种 UTF-8 字节格式：hyphae-utf8-1-128/1024/4096/256。JS 字符串 length 与 JSON Schema maxLength 都不是 UTF-8 字节数。禁用 schema 远端加载，仅允许本地 defs，保留拒绝非法字符的 regex。
3. 展开 body 或 recipe（二选一），prefix + fill 的 repeat 次重复 + suffix，不 trim/normalize；query_context 或 query_context_recipe 同样处理。fixture profile 为 hyphae-public-query-fixtures/1；70 个唯一 id 全部执行。expected_bytes 为展开正文的字节数，不计算 fixture 包装。
4. 正文先做 32768 字节上限，再解析/schema，再 payload_semantic；response 的可信测试 query context 同样先限长/解析/schema/TTL，核对 type=query、request_id、scope 和指定能力 id/version。context 仅为测试 metadata，不是发送者、签名或授权证据。
5. 语义检查有效期差值 (0,86400]；重复能力按 (id,version) 判定，即使 description 不同也拒绝，覆盖 declaration structured、response profile structured 和 response capabilities。严格 JSON 词法前置单独验收；本套 70 例并不覆盖全部 #91 拒绝条件。
6. 输出每个 fixture id 的 actual/expected stage：accepted、body_limit、schema、payload_semantic；要求无缺失、无重复、无跳过、全数匹配。提供消费端固定 commit、验证器版本/锁文件、实际命令和结果。无支持的 case 必须显式报告失败，不能修改预期让测试过关。

## 必须保留的边界

capability query 为平铺 scope/id/version；capabilities response data 为 {capabilities:[...]}；profile response data 是 profile 本体。错误 response 也需要有效原查询 context，不能因没有成功 data 跳过 request_id 验证。协议当前只涵盖 declaration/notification/query/response 候选，不接 acceptance/execution。本套 70 例不覆盖真实签名、tag/event 关联、现在时钟、持久化和授权；对应层分别提供验收证据。

## 验收出口

根代理对照固定 fixtures 的逐 id 期望与 Go 参考测试断言，审查消费端结果；Go 测试成功时不逐条打印阶段，失败按 id 报告。70 例一致只能证明这个候选的跨语言参考一致；生产接收器和 T01-E/T19 四仓验收仍有其他门槛。不得由 schema PASS 自动启用模型、module、run 或将旧普通消息/answer 当执行请求。

## 2026-09-30：新增的两项独立参考任务

消费端各开小测试 PR，固定下面的源提交，保留样例 ID、原始输入和预期阶段，不启用生产新协议：

| 任务 | 固定 Hyphae head | 输入与说明 | 用例 |
|---|---|---|---|
| 声明生命周期 | [#99 / 1f16d3eafc55c57d22e0b0740a9aec60b5258a41](https://github.com/iDoris-ai/Hyphae/tree/1f16d3eafc55c57d22e0b0740a9aec60b5258a41/tests/contracts) | testdata/declaration-lifecycle-fixtures.json；DECLARATION-LIFECYCLE.md | 32 |
| 非执行事件外层 | [#100 / f73ac3d010be868403e8cd6f034b76e94f45e31f](https://github.com/iDoris-ai/Hyphae/tree/f73ac3d010be868403e8cd6f034b76e94f45e31f/tests/contracts) | testdata/event-transport-fixtures.json；EVENT-TRANSPORT.md | 64 |

声明任务输入是假定结构、身份与动作已验证的记录，`alice` 等是合成标签；本层检查时间、最新已知选择、过期/撤销不回退、同秒排序、冲突和未来重评。它不验签、不存储、不证明全网最新，也不运行能力。

外层任务保留静态已签名事件、local_pubkey 及 event_json/Base64/recipe 三选一输入，严格按说明展开 UTF-8 字节，核对 expected_bytes 和每例 expected_stage。公开测试身份禁止用于真实通信。私有 content 的结构样例没有有效 MAC；outer_valid 仅指外层验证通过，不表示 NIP-44 解密、正文 schema、响应作者/事件关联、授权或执行通过。路由 tag 恰两项是本套收窄的候选规则，尚未冻结。

消费端分别输出全部 32/64 个 ID 的 actual/expected、版本及锁文件、固定 commit、命令和结果；无跳过、无改写预期。#91 的 51 个词法样例仍是独立前置。本轮 #91/#96/#99/#100 在固定 main `1948aadc` 的临时 Go 测试组合中共断言 217 个样例并通过 race，仅证明参考套件共存。跨语言、生产入口、持久化和真实 zero-run 证据仍分别验收，T01-E/T07 门槛不变。

## 2026-10-01：T01-C execution recovery 参考消费任务

这是独立于上文 217 个非执行样例的**102 个 T01-C 执行恢复参考**，只让消费端测试实现与固定 oracle 对照；不启动生产 T07、不调用真实 executor/model、不声称实际 run/副作用或四仓验收。T01-C 仍是未冻结候选，不是 wire 规范。原始说明明确 fixture 的 `register_run` / `start_existing_run` 是允许动作标签，不是实际执行计数。

### 固定来源与输入完整性

仅消费 Hyphae #110 fixed head [`18dd9dd7b7150c96bccfdc8f5fde44202831fae8`](https://github.com/iDoris-ai/Hyphae/tree/18dd9dd7b7150c96bccfdc8f5fde44202831fae8/tests/contracts)，不追可移动分支，不重算/改写预期。保留 Apache-2.0/NOTICE 来源记录。

| 文件 | 来源 SHA-256（原始字节） |
|---|---|
| `tests/contracts/EXECUTION-RECOVERY.md` | `dcaa0af71c795a55eaf51fd0a7e71a56bacb070b2fb1f8604f743757ff7543c7` |
| `tests/contracts/testdata/execution-recovery-fixtures.json` | `fdd39bc40de1f1b6ed8c9e899faae834410404f785ec72f20ea6e1c3f7a1ac55` |
| `tests/contracts/execution_recovery_test.go`（Go oracle） | `c182db4a793d0012b8b01314c645c1d9917e7a73e39a2a40497fdf359dfc8ec6` |
| manifest 指定 base commit | `a4aa606eb81d5c040d94c51cdf94553e646d8674` |
| manifest 指定 `docs/agent/t01-authorization-recovery-candidate.md` | `9ac0b52db70ad5e0e156ac8276e8aa7e13025e34cf3349ba6af49f21b9aa47b3` |
| manifest 指定 `docs/agent/t01-contract-gates.md` | `f739451f4821d321f465f565b69488c31d5468bb6c6dce3c28f8a28643de227a` |

fixture metadata 必须保留 `format=hyphae-execution-recovery-fixtures-v1`、`semantics=test-observation-only`、`case_count=102`。每条 case 原样输入 oracle；不丢字段、不改 bool/string/number 类型、不展开或规范化。解析整数时保持 lexical integer 语义（Go oracle 用 `json.Decoder.UseNumber`）；clock 字段仅接受安全整数 ±`9007199254740991`。候选状态图由固定 T01-C 文档 Mermaid 图解析，并核对每条合法边恰有一个 `transition_legal_*` 样例。

### 102 case 与比较要求

Go oracle 通过每个 case 的 `id` 建立 named subtest，并比较 `expected.action`、`expected.state`、`expected.reason`；消费端必须逐 ID 输出 actual/expected 三字段（fixture 未提供的 state/reason 按空值处理），确保 102 个唯一 ID 全执行、无缺失、重复或 skip。类别计数为 clock 12、preflight 15、replay 8、transition 46、recovery 21。主要输入字段如下，case 级真值始终以固定 JSON 为准：

- `clock`：`now/issued_at/expires_at`；要求 `0 < TTL <= 86400`、issued 最多领先 300 秒，开始条件严格 `now < expires_at`，并覆盖 fractional/unsafe integer。
- `preflight`：clock 加 `approval_persisted`、`scope_digest_match`、`budget_ok`、`privacy_ok`、`module_allowed` 及测试观察 `model_required`、`selected_model_available`、可选 `selected_model_location`。初次预检、ready→run_registered 和 existing-run recovery 共用这些门槛。预检必须要求 `model_required` 明确为 bool；仅当它为 true 时才要求 `selected_model_available` 明确为 bool 且 true。model-independent case 即使 selected model unavailable 仍可放行。远端模型只有当 `privacy_ok=true` 才符合此 oracle。`model_required`、`selected_model_available`、`selected_model_location` 是测试观察，绝非冻结的 wire 字段。
- `replay`：仅作者、目标、request id 三者相同才比较 immutable content；完全相同（串行及并发）输出 `reuse_existing`，同键不同内容（参数/预算/过期）为 `request_conflict`，不同作者/目标/request key 为 `new_request`。重投复用登记，不计作新 run。
- `transition`：输入 `from/to/facts`。20 条图内合法边各有唯一 `transition_legal_*` 正例；其它 26 条补充覆盖前置成立/不成立、模型无关/远端 privacy 正例及 blocked case，必须逐条服从 fixture 中 apply/retain 预期，不可一律按拒绝处理。ready→run_registered 复用完整 preflight；run_registered→expired 只接受明确 `confirmed_not_sent_no_start` 且已过期；failed/succeeded 只接受同 run 的执行器明确结果且结果已保存；不确定启动/运行进入 unknown，非法终态边和未知状态保留原状态。
- `recovery`：输入当前 state、run id/调用确定性、clock、重复观察、结果事务及回执待发送事实。已确认未发起且未启动的 run_registered 只有在同一 run id 存在、未过期且全部 preflight 再次通过时才能 `start_existing_run`；确认未开始但已过期才可 expire。调用不确定必须查询同一 run。unknown 的 not-found 不证明未发生副作用，仍保持 unknown，禁止另建 run；同 run 的成功/失败结果必须可靠保存后才转终态；保存失败继续按原 run 查询，不发完成回执。已保存成功/失败且回执待发时只 `resend_receipt`，即使请求过期；否则不重跑。

oracle 的可运行参考命令为 `go test ./tests/contracts -run 'ExecutionRecovery' -count=1` 及 `go test -race ./tests/contracts -run 'ExecutionRecovery' -count=1`。消费端需给出自身固定 commit、锁定的 JSON validator/实现依赖（如适用）、目标语言的普通及并发验证命令、102 行逐 ID 结果和零跳过证据；Go oracle 的 race 命令保留作参考，不强制所有语言实现 race 工具。参考一致不证明 schema/wire 冻结、真实审批/预算/隐私接线、executor 幂等/恢复、真实副作用或 T01-E/T07/T19 通过。
