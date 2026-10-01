# E-M1 PR 依赖与合并顺序

收尾快照（2026-10-01 14:38 UTC）：#119 head `3a837d1a7ee8c3b75a589e6556bab5f6fe32f564` 已于 14:32:14Z 独立 squash 合并为 `419d0e3b22ce4dbae28dcc7a6320ed63b519deee`；#118 head `94532e7701ed87e0137ae6ac4a48e62cb4a2304f` 于 14:32:54Z 独立 squash 合并为当前 main `884424167ef84bf1aaf1143df8486294f5c75425`。两项均获 clestons 对最新 head 的外部批准、全部检查通过，核对实际差异后按 SHA 正常合并。当前 main 的 [CI 36877200986](https://github.com/iDoris-ai/Hyphae/actions/runs/36877200986) SUCCESS；#119 合并版本的制品 [Actions 36877114664](https://github.com/iDoris-ai/Hyphae/actions/runs/36877114664) SUCCESS。本会话只剩交接文档 [#120](https://github.com/iDoris-ai/Hyphae/pull/120) 待外部评审；不等待新评审，不启动 R3 或其他开发，交接后按用户要求暂停 Goal。E-M1 尚未完成，定时扫描关闭。下方 14:23 状态为合并前历史快照。

14:23 UTC：Hyphae #105–#117 已合入 final main `57494422d17051dc5440d2dbed9411df8acd5536`，main CI `36871057418` SUCCESS。#118 head `94532e77`、#119 head `3a837d1a` 均 OPEN、REVIEW_REQUIRED（无 reviews），两者最新 CI 已全绿；#118 的本机 full/race/9 个真实 relay case也通过。#119 旧head普通CI `36873124154` Ubuntu `TestDaemonParentCancellationReleasesHomeLock` 3.01s timeout/ci-ok FAIL，macOS成功；旧artifact run `36873124352` 三job成功。测试主动轮询争锁竞态已修正；新head普通CI `36875227363` 和artifact run `36875227369` 均SUCCESS。失败/修复证据见handoff。R3未开始、无Release；E-M1与T01-E/T07/T19未完成。

12:33 UTC：#104 `aaa6d0f` / CI `36861579126` 与 #115 `2fa6f0b` / CI `36861616162` 完整 SUCCESS；#103 CI 成功，#102/#105/#106 Python discovery 两平台 16/20/23 项通过，#111–#114 CI 绿待外审。S2b 三个 FAIL 中两项是新增断言错误（分页应 3 页；stderr 可含诊断/明文）且已校正；ConflictPrecedesPasswordRead 完整套件重复超时、focused 通过但根因未知，generic helper 后全量/race 复验继续。细节见 [progress](progress.md)。

12:46 UTC：#102/#103/#104 已按最新 head 外部批准与检查结果独立合入（merge `3be99fd`/`5f179d6`/`b1ddf97`）；#102 CI `36863563740` SUCCESS。当前 main `b1ddf97e3dce74de3eaf19784c174543b7b5831c` 的 CI `36863806313` 完整 SUCCESS（macOS、Ubuntu、ci-ok）；三个旧 worktree/本地分支已清理，远端同名分支已自动删除，原 main checkout 与 untracked `AGENTS.md`、`tmux.sh` 保留。#113 `3e9baf3` CI `36862751523` SUCCESS、待外审；#115 CI 成功、待外审。S2b 候选 `c55ffcd` full/race exit 0（44.182s），真实 relay、125 条补收/重启及中断路径已实跑；CLI conflict 3 秒超时仍未查明，详见 [progress](progress.md)。#103 的额外 O_NOFOLLOW/path 防护建议另开小 PR。#114/#115 合入前不发 S2b 生产提交；#105/#106 合入前不发 R2b；保持 PR 独立。

12:04 UTC：Agent24 自有文档 #634 已在有效外部批准、六项检查通过后正常合并；新 main f5a76c01 的 CI 运行中。Hyphae #111/#112 为独立 main PR，待真实 CI/外部评审。#102/#105/#106 补 Python 工具单测的 CI 步骤由 Luna 处理中，变更后须按新 head 检查，不沿用旧绿色结果；其余队列依赖不变。

11:58 UTC 队列：main fc6681c 的 CI 通过。#102/#105/#106 各自 CI 通过、待外部评审；#103/#104 的真实 CI 失败已定位到测试子进程重复构建与临时 HOME 缓存，Luna 修复并复验；#107/#108/#109/#110 新独立 PR 的 CI 运行中。最新 heads 与范围见 [带日期进展](progress.md)。这些 PR 都只含自身差异，可独立评审；G7/G9 与 G2-S1/S2a 后续独立提交。R2b 依赖 #102/#105/#106，待全部进入 main 后才基于新 main 提自身工作流/helper 差异，禁止将当前 integration 父分支作为 main PR 发布。Agent24 #634 六项检查通过、待评审；#626 有批准但仍 OPEN，#627 仍依赖草稿，对仓生产合并由其负责。下方旧快照按时间阅读。

11:40 UTC 最新队列：#101 已按批准 head 合并，main fc6681c 的 ci.yml 实际通过。R1 [#102](https://github.com/iDoris-ai/Hyphae/pull/102) 26bfe6ab、G8 [#103](https://github.com/iDoris-ai/Hyphae/pull/103) 685e5dd3、daemon HOME [#104](https://github.com/iDoris-ai/Hyphae/pull/104) 82228178 为各自独立 main PR，等待真实 CI/外部 review。独立第一轮工具、R2a、R2b 及其余 CLI 改动由 Luna 整理，按实际依赖发小 PR，不合并汇总分支。Agent24 #601 已合并；根代理文档 #634 c7bcf70 独立待审；对仓 #626 ba30f104 已获新批准/CI通过，#627 500cc03e 仍依赖草稿，对仓实施/合并由其负责。下列带早期时间的队列是历史记录。

2026-10-01 本地补充：R2a `cb374a2`（生产 491 行）和 R2b `343520d`（生产 397 行）已各自在独立 worktree 提交并通过根代理本地验收；最终四套工具共 39 项通过，workflow 的 Actionlint、YAML 结构及 bash 语法通过。真实固定 Go 构建、GitHub CI、制品传输与 relay 复测尚未执行通过。发布顺序为 R1、独立第一轮复测工具、R2a、R2b；组合基线仅作依赖，不发布汇总 PR。上述提交尚无远端 PR，不能进入自动合并队列。

更新：2026-10-01。下表是本轮实现的依赖关系；验收通过不代表已进入 main。用户已授权主代理按依赖顺序合并；每项仍需有效批准、main 基线和通过的 CI。

C1 候选 `427f631f` 是基于 `a4aa606` 的独立测试提交，生产 0 行，无生产依赖；根代理 contracts 包普通/race 和 102 样例逐 ID 验证通过。尚无远端 PR，发布后交外部 review/真实 CI；不能提前解除 T01-E/T07。

08:47 UTC 最新对仓队列：#628/#630 已合并，main `be365232` CI `36830773430` 通过；#626 `5531abe0` 的 REQUEST_CHANGES 未解，#627 `95013516` 仍叠在它上面。下列 07:29 的“#628 未合并/失败”是历史快照，不能继续用作当前阻塞；#626 后续迁移必须保留已进 main 的 #628 接线。Hyphae #101、Agent24 #601 仍 OPEN且外部批准 head 未变；本会话写操作限制未变，不重复被拒合并。

## 当前队列：2026-10-01

07:29 UTC 对仓队列：#628 先解决实际 Linux scheduler CI 失败并取得批准；#626 修 locale/TZ 的孤儿标记后复审，二者后合者解决已记录的参数冲突；#627 随 #626 迁移 main 后跑完整 CI、再评审。#630 当前 head 已批准，仍等本次 CLA；它是运行记录，不代替候选代码合并与验收。对仓实现/合并由对应仓库推进，根代理不提交 GitHub review。

当前 Hyphae main 为 `a4aa606eb81d5c040d94c51cdf94553e646d8674`；#37～#100 的已有任务均已合并。#101 `b3f9053eb5d117877656c5102d23f91fb8325eb9` 与 Agent24 #601 `67ddbce30cc7dd713191f63e16f682094e99dee8` 有最新 head 的外部批准及通过的 CI，仍 OPEN；工具审批层拒绝本轮合并调用，用户授权不变。


06:58 UTC：Agent24 #620/#621/#622 已依次合并，#622 最终 head `48c866ac` 有匹配的外部批准，main `f1dbe1ef` CI 通过。后续 COMM-3/4a 从该 main 独立推进，不再等待 #622。制品 R1 已派 Luna 实现；R2 前置 R1 验收/合并，R3 前置真实构建/hash/联调通过，见 [交付任务](em1-artifact-delivery.md)。所有新增本地 feature 尚无远端 PR，不能列成待 review 的 GitHub PR。
| 新工作 | 前置 | 交付门槛 |
|---|---|---|
| G7：会话历史 JSON | 当前 main | 独立小 PR；真实 CLI、身份隔离、三种 JSON 开关 |
| G8：keystore 事务与陈旧写拒绝 | 当前 main | 独立小 PR；所有写路径、跨进程不丢私钥、密码迁移与轮换 |
| G9：文件正文输入 | 当前 main | 独立小 PR；正文不在 argv，输入错误不改变历史/队列 |
| Agent24 文档勘误 | Agent24 #601 合并 | 本地 `9bfb0df` 重新整理到其 main；另提纯文档 PR |
| G7b：存储信息 JSON | 当前 main | 独立小 PR；未建库不创建目录，真实计数与三种 JSON 开关 |
| G6：联系人输入错误 | 当前 main | 独立小 PR；非法 key 返回 1，真实存储失败保留 4；新锁采用时 Agent24 更新旧断言 |
| daemon HOME 互斥 | 当前 main | 独立小 PR；同 HOME 冲突、退出释放、特殊路径无阻塞 |
| 只读口令检查 | 当前 main | 独立小 PR；验证 legacy/current 不迁移，所有结果保持磁盘内容及元数据 |
| G2-S1：JSON-lines 状态模型 | 当前 main | 独立小 PR；闭集统计、多行信封及短写失败，尚不启用 daemon |
| G2-S2a：真实扫描结果 | 当前 main | 独立小 PR；分页/部分结果、durable 新增及取消分类，尚不启用状态流 |
| G2-S2b：daemon 状态流接入 | G2-S1、G2-S2a、daemon HOME 互斥 | 本地 `7b29bf6f`，410 生产行，专项/race通过；独立小 PR，真实 relay/完整CI待验收；每条记录不作为永久同步证明 |
| 组合并发回归 | G8、G1、daemon HOME 互斥 | 独立测试提交；保留源码、真实 CLI 并发及 race；待生产前置合入后整理到 main |
| 第一轮 Agent24 CLI 联调 | #614/#620/#621/#622 已合并；Linux 构建/hash CI 已存在，仍缺下载制品及 Hyphae 独立复测 | 记录双方源码、构建配方/hash、实际入口及 zero-run 证据 |

上述 CLI 改动独立实现，生产改动分别审核，不汇总合并。详见 [接线任务契约](em1-comm-followups.md)。定时扫描按用户要求停用，本轮不创建新计时器。

后台 PR-daemon 可以并行评审这些 PR，包括 draft。评审范围是各 PR 相对其 base 的差异；draft 在这里表示等待前置合入 main，不等于尚未实现。自动 review 与合并是两个步骤，review 结论不自动解除依赖门槛。

## 历史队列：2026-09-30

2026-09-30 本轮实时核对：主线基线为 `1948aadc551e360176711f9c50172ed6edccd253`。#37～#67、#68～#84、#93/#97 已合并；该 main 提交的 Linux/macOS CI 与隔离本机完整 CLI 验收通过。最低 Go 版本为 1.26，CI 按 go.mod 选择工具链。编号范围包含规划、CI 和维护 PR，整体 E-M1 仍待跨仓验收。

| 下一项 | 当前门槛 | 后续动作 |
|---|---|---|
| [#85](https://github.com/iDoris-ai/Hyphae/pull/85)：T01 小交付门槛 | main，`f41cfc6b`，CI 全绿 | 独立设计文档复审；不解除 T07 门槛 |
| [#86](https://github.com/iDoris-ai/Hyphae/pull/86)：上游 integration 门禁 | main，`8867ddff`，CI 与兼容工作流全绿 | 独立复审；线上自动建 PR 仍未验收 |
| [#87](https://github.com/iDoris-ai/Hyphae/pull/87)：relay 保留与密文边界 | main，`d38882a3`，CI 全绿 | 测试候选复审；main 已含 #66 的相同 CI 步骤，合入时确认只保留一份 |
| [#88](https://github.com/iDoris-ai/Hyphae/pull/88)：传输候选 | main，`901a840f`，CI 全绿 | T01-B 设计复审，尚未冻结 wire 协议 |
| [#89](https://github.com/iDoris-ai/Hyphae/pull/89)：台账与跨仓协作 | main，本文件所在文档分支 | 最新文档 head 复审与 CI；含已有模块挂载实测，整体 E-M1 未完成 |
| [#90](https://github.com/iDoris-ai/Hyphae/pull/90)：profile stdin 解锁 | main，`54808247`，CI 全绿 | 独立功能复审；通过后扩展 headless 注册能力 |
| [#91](https://github.com/iDoris-ai/Hyphae/pull/91)：严格 JSON 正反例 | main，`55982f24`，CI 全绿 | 测试参考复审；生产入口尚未启用该校验 |
| [#92](https://github.com/iDoris-ai/Hyphae/pull/92)：信封与恢复候选 | main，`94ad0e05`，CI 全绿 | T01-B/C 设计复审；执行授权与冻结门槛仍保留 |
| [#94](https://github.com/iDoris-ai/Hyphae/pull/94)：固定 NIP-44 向量 | main，`fb165dca`，CI 全绿 | 固定来源测试复审；不表示 SDK 全部安全问题已修复 |
| [#95](https://github.com/iDoris-ai/Hyphae/pull/95)：公开资料/查询 payload | main，`bdeff57a`，CI 全绿 | T01-B 候选复审；后续共享 schema/fixtures 验证四类 body |
| [#96](https://github.com/iDoris-ai/Hyphae/pull/96)：公开查询 schema/fixtures | main，`094d8d1c`，本地 race/全量 Go 与双平台 CI 全绿 | 等最新 head 复审；70 个候选样例，消费端任务见 Agent24 协作文档，尚无跨语言验收 |
| [#98](https://github.com/iDoris-ai/Hyphae/pull/98)：带日期 progress | main，进展文档分支 | 最新文档 head 复审及 CI；记录阶段边界与下一步 |
| [#99](https://github.com/iDoris-ai/Hyphae/pull/99)：声明生命周期参考 | main，`1f16d3ea`，本地/独立 race 复验及双平台 CI 全绿 | 32 个固定样例复审；不代替持久化与消费端验收 |
| [#100](https://github.com/iDoris-ai/Hyphae/pull/100)：非执行事件外层参考 | main，`f73ac3d0`，本地/独立 race 复验及双平台 CI 全绿 | 64 个固定样例复审；不代替正文、MAC、授权或执行验收 |

#56 的真实 CI 失败来自测试构建进程把只读 Go 模块缓存写进临时 HOME；已修正构建环境，relay/CLI 运行数据继续隔离。#57 新增 ACK 与父取消回归，确认 watcher 返回前自动回复已结束；旧实现对照会失败。迁移 #65 时要保留该等待逻辑，并适配其查询完成及返回值变化。

独立文档 PR #84 已合并。#66/#67 合入后，已在固定 main `1948aadc` 完成默认/integration 全量测试、vet、构建、隔离身份 CLI smoke 和空统计验收，并固定 macOS arm64 接线二进制。完整证据见 [CLI 验收记录](em1-cli-acceptance.md)；Agent24 实际接线仍待对应仓库推进。

状态是本次文档提交时的快照。用户停止原 20 分钟扫描后，重新授权 30 分钟或一小时跟进；现复用同一个 monitor 每 30 分钟在本会话跟进 PR 与独立里程碑任务，本会话有排队任务时不重复入队。它不替代 PR-daemon 的 review，不绕过审批或 CI。脚本在 #80 中交付，#97 已合并并安装当前 main CI 绑定修复；本机配置和线程 ID 不进入仓库。

### 查询并发修复与剩余范围

#79 修复了 `relayquery.Fetch` 使用上游异步订阅时，断线触发事件发送与通道关闭的竞争。主代理在旧实现上复现 race；新实现按 WebSocket 线序处理事件和真实 EOSE，保留验签、过滤、NIP-67 提示、帧大小限制与查询 deadline。相关 race 重复测试、真实 CLI/relay 全量 integration 及双平台 CI 通过，详见 [验收记录](em1-cli-acceptance.md)。

#58 已将 inbox 接入该查询层，#60 分页模块与 #65 daemon 调用点已合入。底层 `req/query` 和 `profile discover` 已分别通过 #82/#83 迁移并合入。不能把 #79 当成 SDK 全局修复。#66/#67 已收口，Hyphae 侧固定 main CLI 通过；基础 CLI/UI 跨仓接线和高层行为契约继续推进。

## 前置 PR

同一组中没有依赖关系的 PR 可以分别评审。前置 PR 合入 main 后，后续 PR 先改 base 为 main，确认只剩本任务差异，再合并。

### 合并与复审规则

- 本轮是堆叠 PR。若使用 merge commit，前置提交的祖先关系能保留；若使用 squash/rebase merge，后续分支通常还需重新整理到最新 main，不能只改 base。重新整理时只迁移本任务提交，核对差异和测试后再推送；不要把组合基线变成大 PR。
- 每次 review 记录所审查的 head commit。新提交、冲突解决或基线变化后，旧结论不能直接代表新版本；重新核查有影响的部分。
- PR-daemon 优先检查持久化事务、重复收件与副作用、加密失败、取消和部分成功结果。CLA 通过只说明贡献流程检查通过；本地测试证据和 GitHub CI 结果分别记录。
- 当前仓库开启了合并后自动删除分支。GitHub 可能随分支删除自动调整下游 base；每次合并后检查实际 base、差异及批准状态。临时 integration 分支待全部依赖迁移完再清理。

| PR | 内容 | 前置 |
|---|---|---|
| [#38](https://github.com/iDoris-ai/Hyphae/pull/38) | SQLite 每连接配置 | main |
| [#40](https://github.com/iDoris-ai/Hyphae/pull/40) | 维护中的 Nostr 模块版本 | main |
| [#42](https://github.com/iDoris-ai/Hyphae/pull/42) | 持久化 khatru relay | #40 |
| [#43](https://github.com/iDoris-ai/Hyphae/pull/43) | outbox 跨进程事务 | #38 |
| [#45](https://github.com/iDoris-ai/Hyphae/pull/45) | 身份/联系人 JSON | #40 |
| [#46](https://github.com/iDoris-ai/Hyphae/pull/46) | group UPSERT | #38 |
| [#47](https://github.com/iDoris-ai/Hyphae/pull/47) | 真实 CLI/relay 集成夹具 | #42 |
| [#48](https://github.com/iDoris-ai/Hyphae/pull/48) | 重试结果事务 | #43 |
| [#49](https://github.com/iDoris-ai/Hyphae/pull/49) | 重试历史明文 | #48 |
| [#50](https://github.com/iDoris-ai/Hyphae/pull/50) | relay 配置/探测 | #40 |
| [#51](https://github.com/iDoris-ai/Hyphae/pull/51) | 原子首次收件登记 | #38 |
| [#52](https://github.com/iDoris-ai/Hyphae/pull/52) | 真实 EOSE 查询 | #40 |

## 组合基线上的小 PR

`integration/em1-cli-foundation` / `317fb82` 包含上表所有实现。`integration/em1-cli-reliability` / `6e64aaa` 再组合 #53～#55。两者仅用于开发和测试，不开汇总 PR。

下一层 `integration/em1-cli-recovery` / `c2f3651` 再组合 #56～#59，同样不直接作为汇总 PR 合并。

下表记录原始开发基线，当前状态以上方进度表及 GitHub 为准。**仍以 integration 或临时固定分支为 base 的 PR 保持 draft。** 当前基线的全部前置 PR 进入 main 后，再逐项迁移到 main，检查差异与测试，再标记 ready。这样保留每个小 PR 的独立审阅记录。

| PR | 内容 | 原始开发基线 |
|---|---|---|
| [#53](https://github.com/iDoris-ai/Hyphae/pull/53) | outbox list/clear JSON | foundation |
| [#54](https://github.com/iDoris-ai/Hyphae/pull/54) | 发送前可靠入队；必填参数及 JSON 错误回归已合并 | foundation |
| [#55](https://github.com/iDoris-ai/Hyphae/pull/55) | daemon 持久化收件接线 | foundation |
| [#56](https://github.com/iDoris-ai/Hyphae/pull/56) | outbox retry JSON | reliability |
| [#57](https://github.com/iDoris-ai/Hyphae/pull/57) | 可靠自动回复 | reliability |
| [#58](https://github.com/iDoris-ai/Hyphae/pull/58) | inbox 查询与部分错误结果 | reliability |
| [#59](https://github.com/iDoris-ai/Hyphae/pull/59) | daemon 参数校验与退出取消 | #57 分支；迁移时须包含 `561af2c` 和 `c4e54c9` |
| [#60](https://github.com/iDoris-ai/Hyphae/pull/60) | 有界历史分页模块 | reliability |
| [#61](https://github.com/iDoris-ai/Hyphae/pull/61) | 实际 CLI 离线重试与退出验收 | recovery |
| [#62](https://github.com/iDoris-ai/Hyphae/pull/62) | relay 探测失败连接清理 | recovery |
| [#63](https://github.com/iDoris-ai/Hyphae/pull/63) | 加密身份的 CLI stdin 解锁 | recovery |
| [#64](https://github.com/iDoris-ai/Hyphae/pull/64) | 创建加密身份的 stdin 通道 | runtime |
| [#65](https://github.com/iDoris-ai/Hyphae/pull/65) | daemon 历史分页接线 | runtime |
| [#66](https://github.com/iDoris-ai/Hyphae/pull/66) | 真实 CLI 125 条积压与重启验收 | backfill |
| [#67](https://github.com/iDoris-ai/Hyphae/pull/67) | 空历史统计归零 | backfill |

`integration/em1-cli-runtime` / `b1cbaaa` 再组合 #60～#63，主代理已通过隔离 HOME 的全量 integration 测试。后续 daemon 历史分页接线与真实积压验收分别提交小 PR；依赖已发布的分支时，在 PR 描述固定前置提交，不把其余任务的代码混进差异。

`integration/em1-cli-backfill` / `916fc1f` 再组合 #64/#65；`integration/em1-cli-acceptance` / `f46744a` 再组合 #66/#67。最终版本在 Go 1.25.0 下通过全量 integration、vet、构建及带临时身份的 `test.sh`。详见 [CLI 验收记录](em1-cli-acceptance.md)。不把组合分支作为交付 PR。

## 独立事项

- [#39](https://github.com/iDoris-ai/Hyphae/pull/39) 是规划与验收记录；[#41](https://github.com/iDoris-ai/Hyphae/pull/41) 是按仓库命名的协作约定。
- [#44](https://github.com/iDoris-ai/Hyphae/pull/44) 是经测试后创建依赖更新 PR 的工作流，已合入默认分支。Actions 创建/审批 PR 的仓库权限开关仍等待用户确认，不因 CLA 通过或工作流合入而开启；已在 main 手动触发首次线上更新扫描，依赖无变化、发布步骤跳过，见 [运行记录](https://github.com/iDoris-ai/Hyphae/actions/runs/36702947244)；自动创建更新 PR 的线上闭环仍未验收。
- [#37](https://github.com/iDoris-ai/Hyphae/pull/37) CI 已合入 main，Linux/macOS 检查已生效。当前 main 的 `required_status_checks` 仍为 null，本轮由主代理逐项核对 CI，不绕过 review；尚未修改仓库保护设置。
- retarget 或解决冲突后若代码变化，运行相应测试；全部前置实现进入 main 后，运行一次隔离 HOME 的 `go test -tags integration ./... -count=1`。CLI/UI/四仓验收状态仍以任务台账为准。
