# T01-B：公开声明、通知与能力查询 payload 候选

状态：root 设计，尚未冻结、未实现。基线 Hyphae `e753e6f`；配套候选 [传输 #88](https://github.com/iDoris-ai/Hyphae/pull/88)、[JSON 输入 #91](https://github.com/iDoris-ai/Hyphae/pull/91)、[信封与恢复 #92](https://github.com/iDoris-ai/Hyphae/pull/92)。本文只收口四种非执行 payload；execution_request/receipt 仍等待 T01-D/E。不得据此开启生产入口，或把 schema 通过记作四仓验收通过。

## 通用规则

所有字段/数组上限还受 #88/#91 的整条明文正文 32768 字节上限约束；多个字段分别合法而聚合超限仍拒绝，不截断资料或能力。以下字段集合均封闭：缺必填、未知字段、null 或错误类型拒绝；不隐式转型、trim、Unicode normalization 或补默认值。文本长度按 UTF-8 字节；安全整数、Unicode 和重复 key 先通过 #91。数组的顺序保留；下文要求唯一的数组按原字符串或明确的二元键去重，不按显示名或模糊版本匹配。

- 公钥和事件 ID 是 64 字符小写 hex，继承 #92；payload 不再次声明 author/to/request_id。
- 能力 `id` 为 `[a-z][a-z0-9._/-]{0,127}`；只是名称，不是可请求的 URL 或模块路径。
- 能力 `version` 为 `[A-Za-z0-9][A-Za-z0-9._+-]{0,63}`，是精确、不透明版本；拒绝范围和通配。不能凭此推断已安装的模块版本。
- 公共 name 为 1～128 字节；description 为 1～1024 字节。公开字段由发布方先完成披露授权，不从本地记忆或模块清单自动导出。
- 能力 descriptor 恰为 `{id,version,description}`，三项必填；同一列表中 `(id,version)` 唯一，最多 32 项。descriptor 仅是作者声明，不是工具 schema、执行许可或独立可信能力证明。

## declaration：发布与撤销

外层 b=register、正文 type=declaration；json，无 p/e。payload 是 action 区分的两个封闭分支：

| 分支 | 必填字段 | 可选字段 | 禁止字段 |
|---|---|---|---|
| upsert | action=`upsert`、profile | 无 | 其他全部 |
| withdraw | action=`withdraw` | 无 | profile 及其他全部 |

profile 又按必填 mode 区分：

| mode | 必填字段 | 可选字段 | 禁止字段 |
|---|---|---|---|
| simple | mode、name | 无 | tags、description、capabilities 及其他全部 |
| tagged | mode、name、tags | 无 | description、capabilities 及其他全部 |
| structured | mode、name、capabilities | description | tags 及其他全部 |

tags 为 1～16 项互不重复字符串，每项 `[a-z][a-z0-9._-]{0,63}`。它是公开分类，不是权限或模型能力。structured capabilities 可为空数组，表示公开声明不提供能力；没有默认能力。需要多版本时分别声明 descriptor，查询按精确二元键匹配。

没有新声明时也不把旧 profile 升格为新执行许可。旧 profile 的 mode 缺省、自由标签、rates/rating/availability、旧 Capability.name 等继续作为旧格式展示；不静默转换到此 payload。新的 structured 在 E-M1 只承载描述与版本化能力；报价、信誉及工作时间的规范字段随对应阶段设计，旧字段不得通过未知字段兼容路径进入新执行授权。

### 时间、选择与撤销

候选声明生命周期为 `0 < expires_at - issued_at <= 86400` 秒。先验签、schema 和未来时钟门槛 `issued_at <= now + 300`；更远的未来记录暂不参与选择，重评时再次验签并用当前时间检查。不能因它时间大就覆盖当前记录，也不能用 relay 的接收顺序决定声明。

在符合上述门槛的同作者声明中，按 issued_at 取最大值；同秒按 event_id 小写 hex 字典序较小者胜出。选定后再判当前有效性：upsert 且 `now < expires_at` 才可提供能力；withdraw 或到期 upsert 都是 inactive。撤销记录即使到期仍作为最新已知记录阻止回退，直到更晚的有效 upsert 胜出。

接收端持久化最新已知声明的版本、事件 ID 与 active/inactive 状态；不能清掉失效记录后从旧 relay 历史复活能力。首次同步仅有部分事件时记为最新已知，不能宣称网络全局最新或全量同步完成。撤销限制后续开跑，不自动取消已经开始的 run；取消另由任务协作契约处理。

## notification：公开文字通知

外层 b=publish、type=notification；json，无 p/e。payload 恰为 `{topic,text}`：topic 为 `[a-z][a-z0-9._-]{0,63}`，text 为 1～4096 UTF-8 字节。生命周期同样为 `0 < expires_at - issued_at <= 86400`，未来时钟门槛为 300 秒，到期不作为当前通知。

通知可以展示，不能进入模型、模块或 run。E-M1 不自动获取正文中的 URL，也不定义附件抓取字段。私有参数、凭据、模块内部工具名不由通知广播；需要的新字段随明确协议版本演进，不以开放 payload 绕过白名单。

## query：公开资料查询

外层 b=inquire、type=query；nip44，唯一 p，无 e。payload 是 scope 区分的封闭分支，继承 #92 的 request_id、目标及时间门槛：

| scope | 必填字段 | 其他字段 |
|---|---|---|
| profile | scope=`profile` | 全部禁止 |
| capabilities | scope=`capabilities` | 全部禁止 |
| capability | scope=`capability`、id、version | 全部禁止；id/version 与 descriptor 规则相同 |

查询只读取目标作者已声明的公开 profile/descriptor；没有 params、任意 filter、HTTP 路径、模块工具名或试运行请求。返回已经公开的能力描述不授权调用能力。simple/tagged 的 capabilities 查询成功返回空列表；精确 capability 查询则返回 CAPABILITY_NOT_DECLARED。

查询登记绑定签名作者、目标、request_id、type 及不可变的 scope/id/version/issued_at/expires_at；同一键内容不同返回 REQUEST_CONFLICT，不能换查询范围后冒充重投。

已过期的新查询不产生新查询工作；格式合法且验签/目标均有效时可返回 EXPIRED。重复逻辑查询读取已保存的同一响应快照，不能因声明变化悄悄更新该查询结果；需要刷新则发起新的 request_id。已保存响应在断线恢复时可补发，响应本身不被请求期限抹掉。

## response：查询快照或错误

外层 b=inquire、type=response；nip44，唯一 p/e。继承 #92 的原目标签名、原作者 to、request_id、合法 request_event 关联；任何查询响应都不生成 execution_receipt 或 run。

payload 按 outcome 区分：

| outcome | 必填字段 | 可选字段 | 禁止字段 |
|---|---|---|---|
| ok | outcome=`ok`、scope、declaration_event、declaration_issued_at、declaration_expires_at、data | 无 | error 及其他全部 |
| error | outcome=`error`、error | 无 | scope、declaration_*、data 及其他全部 |

ok 的 scope 必须与原查询一致；两个声明时间均为非负安全整数，满足上述声明生命周期及期限关系，declaration_event 为小写 hex。data 的封闭分支为：profile 查询返回完整 upsert profile；capabilities 返回 `{capabilities:[descriptor...]}`；capability 返回一个精确匹配原 id/version 的 descriptor。数据来自生成快照时的 active 声明；不得从 inactive 声明返回能力。

声明事件引用是对端报告的来源，不能单独证明该声明已被查询方获取、完整验签或在网络中仍最新。响应可以迟到；不因当前时间超过 declaration_expires_at 丢掉历史查询快照，但展示为历史数据，不能凭它启动已失效的能力。真正开跑仍核对当前声明、许可和版本。

error 恰为 `{code,message}`，message 为 1～256 UTF-8 字节；code 只允许 NOT_DECLARED、DECLARATION_INACTIVE、CAPABILITY_NOT_DECLARED、EXPIRED、RATE_LIMITED、REQUEST_CONFLICT。分别表示没有已知声明、最新已知声明失效/撤销、active 声明没有精确能力、查询过期、资源准入拒绝、同请求键内容冲突。未知/未验签/错误目标/坏 schema 的输入不自动生成回复；message 不回显私有正文或凭据。错误不产生执行请求，也不改变既有执行状态。

## 下一项可派发交付

root 按上述封闭分支提交 JSON Schema、错误表及固定共享样例；Luna 在独立 worktree 实现参考验收。样例包含三种 profile、withdraw、重复 descriptor/tag、字段分别合法但正文聚合超限、未知字段/null、空能力、精确版本不匹配、同秒排序、未来 300/301 秒、到期撤销不回退、部分同步、响应 scope/来源/关联错误、迟到快照、重投保持结果以及全部非执行类型的 zero-run。

这只是 schema/参考行为验证。真实 Agent24 入口的 zero-run、双方同一 fixtures 消费、声明持久化、query 去重、断线补响应与模型/模块执行仍分别提供实际证据。T01-E 后才同步权威协议/速查并派发 T07；未完成部分不得由实现自行猜字段。
