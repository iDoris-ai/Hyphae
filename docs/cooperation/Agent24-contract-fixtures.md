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
