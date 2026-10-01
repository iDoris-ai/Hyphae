# Progress — 生态里程碑进展

最后更新：2026-10-01。本文记录实际交付与验收边界；各节固定其对应版本，新审批及合并以 GitHub 为准。

## 2026-10-01：CLI 联调缺口与当前出口

### 本轮收尾：托管状态流与依赖推进

R2b 已提交 `343520df6470884f37cc962a3105cebbc5905783`，独立 worktree `/tmp/hyphae-artifact-r2b-wt`，生产 397 行（workflow 237、helper 160），测试 149 行，文档 17 行。Luna 与根代理固定最终提交、隔离 HOME 的四套工具 39 项均通过；Actionlint、实际 YAML 结构和四段 bash 语法检查通过。已修复 checkout 越出工作区、ZIP 丢执行位及 FIFO 打开阻塞；下载包复验后才运行，Linux relay 摘要绑定 build output。源码/编排本地验收完成，真实固定 Go 构建、GitHub Actions、制品传输与 live relay 尚未通过。尚无远端 PR；不能记 T20 或 E-M1 完成。另派 GPT-6 Luna 在独立原 main 基线准备 C1 时间/执行恢复候选 fixtures 与测试，生产入口不变，T01-E/T07 门槛保留。

R2a 校验器已提交 `cb374a257466b0afa19a4924898843dcec17c8a1`，491 行生产代码、313 行测试、16 行文档，工作树干净。Luna 最终 14 项和根代理固定最终提交、隔离 HOME 的独立 14 项均通过。已修复校验后复制路径被替换的问题，最终交付字节重新校验摘要与大小；测试对恶意归档同步更新 manifest/SHA256SUMS 后实际命中成员拒绝门禁。公开 CLI 始终固定真实生产基线，合成夹具只进入内部测试函数。这是本地校验工具验收，未生成真实 Go 1.26.4 制品或运行 relay。已派同一 Luna 在新的独立 worktree 实现 R2b CI，分别固定工具、源码与对仓生产 lock，下载同 run 的制品后在两个目标平台真实执行。没有继续重复查询 GitHub，E-M1 仍未完成。

07:29 UTC：Agent24 第二轮候选已出现：#626 `5531abe0` REQUEST_CHANGES（孤儿 pid 标记受 locale/TZ 影响）；#627 `95013516` 依赖 #626 的草稿；#628 `b45ba08a` Linux scheduler 恢复轮询测试失败，已查实际日志；#630 `104b8c44` 有当前 head 批准、CLA 仍在运行。已读取 #630 的实际 event_id、收发重试与托管运行记录；这是对仓本地组合报告，仍缺计数正对照、模型/模块计数、Hyphae 独立复测、125 条与 UI 验收。已更新按仓协作文档与后续依赖，R2 在 `/tmp/hyphae-artifact-r2-wt` 实现，不修改对应仓库代码。

07:17 UTC：FU-1/R1 已在独立 worktree 提交 `5fe391edae239aa99849b77afdb861d5d6622a77`，343 生产行、332 测试行；工作树干净。Luna 7 项黑盒及根代理最终提交、隔离 HOME 的独立 7 项均 exit 0。真实全脚本在干净 a4 checkout 上因本机 Go 1.27.1 被固定 1.26.4 门禁拒绝，exit 1；未切换/下载工具链，也未生成真实二进制。R1 仅通过本地工具源码/编排验收，未发布、未通过真实 CI。根代理已派同一 Luna 新独立 R2：真实固定 Go 构建、下载/安全解包/生产 lock 校验、Linux/macOS arm64 第一轮运行；前置开发基线只作组合，不发布汇总 PR。FU-1、T20 和 E-M1 仍未完成。

最新实时更新（2026-10-01 06:58 UTC）：Agent24 [#622](https://github.com/iDoris-ai/Agent24/pull/622) 已在 `48c866ace755f2ae92464f3e12e59c9ea9c77a73` 获最新 head 的外部 APPROVED 后合并；旧 `c660319a` 的 REQUEST_CHANGES 不是最终结论。Agent24 main 为 `f1dbe1efe01766a31e4cff3768c7375c5ab0f2ae`，[CI 36826098337](https://github.com/iDoris-ai/Agent24/actions/runs/36826098337) 通过；正式 CLI/REST 的身份、联系人、relay 接线已进入 main，真实联合验收仍未完成。Hyphae main `a4aa606` 的 CI `36738033201` 通过。

Goal 已恢复为 `active`，未设置 token 预算；此前 usageLimited 只作为历史状态保留。根代理可以在既有 CI/依赖条件满足时 merge 外部批准的最新 head；禁止批准任何 PR 或提交 GitHub PR review，所有 PR review 由外部完成。

已派现有 Luna 在 `/tmp/hyphae-artifact-wt` 独立 worktree 实现 FU-1/R1：固定源码/Go、隔离环境、确定性归档与实际摘要。根代理已拆清 R1 工具→R2 真实 CI/联调→R3 Release/下载验收，见 [制品交付](em1-artifact-delivery.md)。当前 Hyphae Release 仍仅 v0.26.0 且 assets 为空；Agent24 已合并的真实 Linux 构建/hash CI 没有上传制品，不能替代下载交付。第一轮同 hash 独立复测仍缺制品与支持监听的执行环境。

下面的“实时核对”和固定版本记录是历史快照，当前状态以上述更新时间为准。

实时核对：Agent24 #620、#621 已合并；#622 已转 main 正式评审，head `c660319a8343f8e948db51a8b3779cd26ae3b4b1`，尚无正式 review，Linux/macOS Rust 检查仍在运行。Agent24 main 已为 `4fb5a89246a64e31870cb531e0e2044aa1a315f4`，CI `36822440235` 此次读取时仍在运行，不沿用上一 main 的通过结论。Hyphae main 仍为 `a4aa606`。

G2-S2b 已在独立 worktree 提交 `7b29bf6f2f76663fcbddb7bf69bb37d13fba25b7`，生产差异 410 行，测试 760 行：实际 daemon 生命周期、每 relay 扫描结果及 JSON-lines，stdout 仅状态、stderr 诊断，输出失败停止后续任务。已修复并保留多 relay 索引回归、测试数据库隔离和子进程异常回收。Luna 最终正常/race 专项及根代理独立 race 均 exit 0。真实 CLI 的 JSON 模式、未知 relay、generation、SIGTERM 与 kill 专项通过；真实 relay/125 条进程重启用例源码已保留，未在本沙箱运行通过。完整测试唯一尝试因 listener denied exit 1。开发组合仅作依赖基线；S2b 只提自己的小改动，不发布汇总分支。

独立 CLI 复测工具已提交 `3acedb216b09348fb649fe475924ce5f4ad0e2f1`，工具 499 行、独立测试 195 行，工作树干净；11 项编排/制品/超时单元测试及根代理无外部环境变量的 fixture 复验通过。生产 lock 夹具固定真实公开文件原始字节，实际集成仍必须显式提供外部 lock。第一轮同 hash 独立复测仍未通过。用户提供 laptop 的 `~/Dev/auraai/Agent24` 路径在本执行机不存在；实时读取 Hyphae Release 仅 v0.26.0、无二进制 assets；Agent24 Release 是 Agent24 包，不能替代锁定 Hyphae/relay。生产 lock 已读取并固定，不能换成 Go 1.27 构建或改 lock 来制造通过。复测工具缺制品实测 exit 1；单元测试只验工具编排，不验跨仓链路。

第二轮协作出口：#622 最新 head 评审及 CI 完成后逐项合并；COMM-3 收发/history/outbox 与 COMM-4a 托管可先各自开发草稿，再分别迁移 main。正式 CLI/REST 驱动托管、补收历史可见、配置重启和异常退出，六类入站 run/model/module 零计数及有效正对照都须提供证据。#601 回复及用户明确要求的制品 Release/asset URL 评论均已准备并尝试，但工具审批层在执行前拒绝发布，不能记送达。制品要求包含两个可执行文件、生产 lock、SHA256SUMS、固定 tag 与构建配方；Agent24 包不能替代 Hyphae 制品。整体仍为 E-M1/T20，基础 UI、T01-E 和四仓验收未完成。

以下记录固定当时版本，后续状态以上述更新及 GitHub 为准。

- 后续实时核对：Agent24 main `65a5c511` CI 通过，#620 `b00b51d7` 已合并，口令文档和两处缺断言已修复。#621 `f6d055ff` 尚无正式 review，但七项检查含真实 Hyphae Linux 锁定构建/hash 验证通过；#622 `13604363` 是叠在 #621 上的正式 CLI/REST 候选草稿。第二轮建议 COMM-4a 和 COMM-3 并行准备，合并仍按前置逐项进行，zero-run 需两项都就绪。详细接口接点与剩余门槛见 [Agent24 更新](../cooperation/Agent24.md)。下面固定旧 head 的评审记录仅代表当时事实。

- Agent24 [#620](https://github.com/iDoris-ai/Agent24/pull/620) `a9c4bd63` 已提供第一轮真实 CLI/relay 结果及 harness。Hyphae 已核对其 F1：history 只读本地库；单次 inbox 或 daemon 补收持久化后才可见新消息。daemon 内部直接 Walk，不启动 inbox 子进程。确认与下一轮建议已写入 [Agent24 协作记录](../cooperation/Agent24.md)；#601 评论发布被工具审批层拒绝，不能记为已沟通送达。第一轮已运行证据与未覆盖的 daemon/REST/UI/125 条/zero-run 明确分开，不把对仓测试报告当 Hyphae 独立复测。
- 根代理进一步审阅 #620 harness：显式验收缺变量会返回 PASS，relay 在 panic 后没有自动回收，B CLI 无超时，两个发现仅打印不失败，实际只测 A→B。具体位置及补齐要求已写入协作记录。Luna 正在新的独立 worktree 实现 Hyphae 侧生产 lock 双向复测工具；未运行真实制品前不记通过。G2-S2b 初稿的多 relay 完成回调索引遗漏已在评审指出，等待修复及保留回归测试。

- 当前 Hyphae main 为 `a4aa606eb81d5c040d94c51cdf94553e646d8674`。#85～#92、#94～#100 已合并；此前固定版本的全量、真实 relay、构建及双平台 CI 证据保留在 [CLI 验收记录](em1-cli-acceptance.md)。Hyphae 单仓 CLI 通过，E-M1 整体未通过。
- [Hyphae #101](https://github.com/iDoris-ai/Hyphae/pull/101) `b3f9053eb5d117877656c5102d23f91fb8325eb9` 与 [Agent24 #601](https://github.com/iDoris-ai/Agent24/pull/601) `67ddbce30cc7dd713191f63e16f682094e99dee8` 已有匹配 head 的外部批准，实际 CI 全绿。合并调用被本会话工具审批层拒绝；实时复查仍为 OPEN，不能记为已合并。用户合并授权持续有效，无须重新确认。
- Agent24 [COMM-0 #612](https://github.com/iDoris-ai/Agent24/pull/612) 已合并。当前 Agent24 main `c9f5f9cab1c208b09f7ebf9d13a3e1481adcaf12` 已包含 F4b 默认冻结的调用路径；这是源码核查，尚未完成真实计数后端的 zero-run 联调验收。
- Agent24 [COMM-1a #614](https://github.com/iDoris-ai/Agent24/pull/614) 已在最终 head `77655f48444e45e3ee0ec37e160bccccfbe8b8b2` 合并，双平台 Rust CI 通过。其测试扫描已排除 Cargo 的 `tests/` 目录，保留生产变量正对照；此前失败已解决。Agent24 当前 main 的 [CI 36812871955](https://github.com/iDoris-ai/Agent24/actions/runs/36812871955) 也通过。真实二进制测试仍在未设置 `HYPHAE_TEST_BIN` 时跳过，CI 尚未按锁构建 Hyphae；重新核对还发现 runner 未实现 `run_keystore_write`，正式 daemon 尚未依赖 comm crate，CLI 没有 Comm 子命令。COMM-1a 的全部验收出口和正式接线不能仅凭合并记通过。
- 反馈核验已完成，Agent24 文档勘误本地提交为 `9bfb0df6e01cd5492ea3022c4c2852d22360d172`，尚未发布；待 #601 合并后另提文档 PR。11 项 CLI 差异和后续独立任务见 [CLI 联调缺口](em1-comm-followups.md)。
- G7 会话历史 JSON、G8 keystore 完整并发写保护、G9 文件正文输入均已在独立 checkout/worktree 本地提交。G7 head `2a8de30083d1312f1f891d0647fcaf459237d977`，G9 head `3472f935cd319e6a028b1d364d63fa3691f937f4`，G8 head `087832c50c83e549ec3991cbeeb47f0d1bbe61cc`；两项专项真实 CLI 测试通过，G9 的 Linux amd64 测试交叉编译通过。G8 已在旧版复现两个陈旧快照写入丢私钥，新版对应回归及 identity/types、identity race 通过；评审发现的口令校验绕过已修复并有回归断言。三项尚未发布，不能记为已进入 main。
- G7/G8/G9 的本地组合 `16d28d543a28fd905772496638faa3a5f5c8d39a` 已通过会话 JSON、文件正文加密入队和 CLI 错误信封专项；生产代码无冲突，文档插入冲突已保留两段。组合仅用于验收，不创建汇总 PR。daemon 互斥 `7ea7ad8f` 的真实 CLI 和 race 通过，特殊锁路径修订已提交为 `6765d1d9632afd41f4e98393ce1c8e561b7f3da9` 并通过有界进程、race 与 Linux 编译；只读口令检查 `7fa3e7bcd93d081be6e1b0645fbcbb9168f1c271`、存储信息 JSON `7dadc7a980309facd714b7563a522600dc862da3`、联系人输入分类 `574f049b7d8761583f41193d8287c3a30afd0d3e` 已本地提交并通过各自专项/race。密码校验测试初稿曾失败，修正后的独立正常测试 exit 0 与 race exit 0 已核对；旧失败日志保留。七项本地组合最终为 `ef99b130116b79e2a0b9ada988995562bbc530b9`；完整 identity、messaging/CLI/daemon 专项、identity race 与构建 exit 0。组合不提汇总 PR。一项额外 daemon/identity 交互 race 的临时源码未保留，虽有 exit 0 日志，不能作为可复现验收；永久回归已补成仅测试提交 `042c23a393ce32dcfbaca1c7d35aaa4c16340d51`，正常/race exit 0，源码与原始日志已保留；它证明共享 HOME 的实际 CLI 互操作和锁保护，屏障不证明写事务必然重叠。G8 的独立竞争测试另行保留。新分支创建被本会话工具审批层拒绝，独立 commits、补丁、bundle 与测试证据保留待发布。
- 主仓仍停在原 checkout，用户文件保留。测试使用临时 HOME；原指定 Go 缓存写入受当前沙箱限制，使用临时缓存复验并记录偏离。新改动的全量 `go test ./...` 已运行，多个既有 httptest 因 `bind: operation not permitted` 失败；这不能记为完整回归通过，需发布后的真实 CI 验证。

新增生产改动分别为 G7 69、G8 407、G9 73、daemon 锁 97、G1 159、G7b 151、G6 3 行；各项独立分支，测试与纯文档不计，没有汇总大 PR。其专项证据不能替代未完成的完整 CI 和 relay 联调。

补收状态设计已对照 Agent24 已合并 COMM-0 G2 改为 `daemon --json` 的 JSON-lines；stdout 仅完整状态信封，诊断留在 stderr。状态编码 S1 已本地提交 `82de8549dffcf15f4e41f195a1d4c1f1e1ada11d`（410 生产行），专项/race 通过，根代理已核对取消优先、真实统计约束及输出失败终止。真实扫描结果 S2a 为 `6f7daa141fd889651eb43fcba670e4f306fa1bc7`（113 生产差异行），专项/race 通过，真实 relay 与完整测试因监听限制未验收。S2b 正在独立 worktree 接入实际 daemon 的状态流与局部日志分流；基线组合只用于开发，不提汇总 PR。快照区分当前扫描成功、失败与取消，不声明完全同步或送达。

阶段：T20 仍为 IN_PROGRESS；T21/T22 基础 UI 未验收；T01-E 未通过，T07 生产实现不启动。下一步是逐项验收上述小改动，发布后更新双方源码/hash 锁，完成 Agent24 实际 CLI 与真实 relay 的联合测试，再推进基础 UI。模型隐私预算、模块权限恢复和真实语音链路均仍待四仓交付与验收。

用户要求停止定时扫描，当前 monitor 已停用；本次主动推进不重建计时器。以下 2026-09-30 节中的队列和 monitor 描述仅代表当时状态。

## 2026-09-30：E-M1 阶段汇报

### 总体规划

保留 Hyphae 历史 M1～M5 的含义；生态规划使用 E-M 编号，见 [生态里程碑](ecosystem-roadmap.md) 和 [任务台账](ecosystem-tasks.md)。

| 里程碑 | 目标 | 当前阶段 |
|---|---|---|
| E-M1 最小协作闭环 | 单 relay、双 Agent，发现→请求→授权执行→回执，接入模型、语音及外部模块 | 正在推进，整体未验收 |
| E-M2 网络扩展 | 自部署、多 relay 接力、TTL/去重、漂流瓶、资料分级披露 | 尚未进入实施 |
| E-M3 任务协作 | 协商、委派、取消及验收 | 尚未进入实施 |
| E-M4 长期自治 | 订阅触发、持续匹配、调度、恢复、活动记录及防消息循环 | 尚未进入实施 |
| E-M5 价值交换 | 授权、信誉、AAstar Point 支付与对账，先测试网 | 尚未进入实施 |

E-M1 按 **CLI 通信主干 → 基础 UI 通信管理 → 高层协议与授权执行 → 四仓验收** 交付。Agent24 管执行、审批、记忆、调度和模块；AgentEar 管听说及设备权限；iDoris 管模型、隐私、预算和用量；Hyphae 管身份、发现、加密通信及协作协议。

### A：CLI 通信主干

Hyphae main 固定 `e753e6f5ced27a46a2a6befd20c49c5d4c8d6080`，已合并 #37～#65、#68～#84；该提交的 [Linux/macOS CI](https://github.com/iDoris-ai/Hyphae/actions/runs/36681399801) 通过。已进入主线的能力包括：

- 身份、联系人、relay 配置/探测及待发管理的 JSON 接口。
- 双身份真实 relay 加密收发；发布前持久化入队，断线后复用原事件 ID/签名重试。
- SQLite 连接配置、outbox 并发、历史明文保留、首次收件持久化去重。
- daemon 历史分页补收、取消及退出，加密身份的 stdin 非交互调用。
- 查询层的实际竞争缺陷修复，raw query 和 profile discover 接入修复后的查询层。

固定组合分支已真实验证 125 条离线积压全部补收、重启不产生重复新消息效果，见 [CLI 验收记录](em1-cli-acceptance.md)。这份证据不是当前 main 的完整收口验收；[#66](https://github.com/iDoris-ai/Hyphae/pull/66) 积压集成测试和 [#67](https://github.com/iDoris-ai/Hyphae/pull/67) 空统计修复合入后需重做固定 main 验收。Agent24 正式通信 CLI 适配器仍未完成，A 段整体未通过。

### B：基础 UI 通信管理

已约定 CLI/UI 共用通信服务、身份与 relay 配置；先管理身份/联系人/relay/连接，再接消息/历史/待发/重试。状态区分本地入队、relay 接受、对端确认和执行完成。Hyphae 已提供可接线接口，Agent24 的实际适配与 UI 尚未交付。

### C：高层协议与授权执行

待审交付包括传输、信封、授权及崩溃恢复候选、严格 JSON 正反例、公开查询 schema 和 70 个共享候选样例，以及固定 NIP-44 向量。候选和 Go 参考测试不等于生产入口或跨语言验收。

仍缺权威协议收口、消费端验证、register/publish/inquire/subscribe 新行为、Agent24 通信与执行分流、持久化 request/run 关联、执行去重和回执恢复。T01-E 未通过，T07～T17 的原有依赖继续保留。

### 模型、模块及语音

- iDoris 已核查实际接口；Agent24 正式 provider、逻辑执行预算准入/核销和实际用量来源仍需对应仓库实现。回环地址不能证明底层推理在本地。
- 真实外部模块选定 Sin90 `a61ab99443efe91432487000625dfce437660c85`，配对 Agent24 `7009294834b2251beac438f3190aae073742c5dd`。实际挂载、代理、事件及 memory/approval/scheduler 往返通过；完整权限、停用重连、版本/摘要拒绝和远端执行恢复未通过，T12/T13 保持 WAITING。
- AgentEar 已有附着机制；真实语音→远端执行→结果播报及拒绝、断线、重启链路尚未验收。

跨仓协作交接见 [PR #89 固定版本](https://github.com/iDoris-ai/Hyphae/tree/4969d5c4259b1709f86fc9943f6d80847f781f5b/docs/cooperation)，对应仓库实现由用户推动。

### PR 与上游状态

最后核对时 15 个自有 PR 检查全绿，仍待最新 head 的有效批准。检查成功不替代审批。

| 组别 | 待审 PR / head | 后续处理 |
|---|---|---|
| CLI 收口 | #66 `9d16be76`；#67 `d85aa14b` | 批准后正常合并，再固定 main 验收 |
| 真实 panic 修复 | [#93](https://github.com/iDoris-ai/Hyphae/pull/93) `19743c1f` | 优先审核，可独立合并 |
| main CI 监控修复 | [#97](https://github.com/iDoris-ai/Hyphae/pull/97) `a6e33a1b` | 审核合并后部署；当前未安装该修复 |
| 契约与跨仓文档 | #85 `f41cfc6b`；#88 `901a840f`；#89 `4969d5c4`；#92 `94ad0e05`；#95 `bdeff57a` | 设计复审，不解除生产契约门槛 |
| 独立功能及参考测试 | #86 `8867ddff`；#87 `d38882a3`；#90 `54808247`；#91 `55982f24`；#94 `fb165dca`；#96 `094d8d1c` | 各自复审；#66/#87 合入时检查重复 CI 步骤 |

旧上游依赖迁移和更新工作流已进入主线。[首次线上扫描](https://github.com/iDoris-ai/Hyphae/actions/runs/36702947244) 成功但依赖无变化，候选测试、bundle 上传与发布步骤均跳过；自动创建依赖更新 PR 的完整线上闭环仍未验收。仓库权限设置未改变。

### 下一步与分工

1. 根代理继续收口事件外层的签名身份、目标、重复 tag、版本、输入上限和兼容预期；Luna 在独立 worktree 实现共享正反例及参考验证。先限非执行类型，不开启生产行为入口。
2. Agent24 推进 CLI 适配和基础 UI；iDoris 明确 provider、隐私和预算保证；Sin90 明确能力及参数/结果投影；AgentEar 明确请求关联与播报去重。以按仓库命名的协作文档交接，分别回填真实证据。
3. #66/#67 合并后，Luna 在独立 worktree 执行固定 main 的全量测试、真实 relay integration、vet、构建和隔离身份 smoke；根代理验收并固定接线版本。
4. 契约及消费端结果一致后推进高层行为、授权执行和联合验收，覆盖拒绝、断线、重启、副作用不重复及真实语音链路，再判定 E-M1。

根代理负责设计、协调、评审和验收；GPT-6 Luna 负责实现与测试。每项独立 worktree、及时 commit/push/PR，生产差异通常控制在 300～500 行以内，测试及纯文档不计入该限制；小修复不为凑行数扩张。后台 PR-daemon 持续评审。

2026-09-30：用户先要求停止原 20 分钟扫描，已停用；随后授权按 30 分钟或一小时检查，选择复用现有 monitor 改为 30 分钟。检查不取代开发：每轮同时评估可独立推进的里程碑任务。有效批准、最新 head CI 和前置满足后按 SHA 正常合并；不自批、不绕过保护、不合并 integration 汇总分支。

### 当日后续交付与验收

- [#93](https://github.com/iDoris-ai/Hyphae/pull/93)、[#67](https://github.com/iDoris-ai/Hyphae/pull/67)、[#66](https://github.com/iDoris-ai/Hyphae/pull/66)、[#97](https://github.com/iDoris-ai/Hyphae/pull/97) 已按最新有效批准、通过的 CI 和绑定 head SHA 正常合并。固定 main 为 `1948aadc551e360176711f9c50172ed6edccd253`，其 [Linux/macOS CI](https://github.com/iDoris-ai/Hyphae/actions/runs/36729070274) 通过。
- Luna 在该固定 main 的独立 worktree、Go 1.27.1、临时 HOME 下通过全量默认与 integration 测试、vet、build 和 CLI smoke。三个真实 relay 测试都实际执行；125 条积压完整导入，重启后新消息效果为零；空统计四项为零。`test.sh` 只提示 E2E 入口，真实 relay 证据来自 integration 测试。
- 固定 macOS arm64 接线二进制 SHA-256 为 `a7bb4a83b5d6be0a939a4cd92a853a2672f97012c48d704a9a3a718b9e6d806b`；未安装生产二进制。Hyphae 侧 CLI 收口通过，Agent24 CLI/UI 及四仓闭环仍待对应仓库验收。
- Luna 新交付 [#99](https://github.com/iDoris-ai/Hyphae/pull/99) `1f16d3ea`：32 个声明生命周期共享样例；[#100](https://github.com/iDoris-ai/Hyphae/pull/100) `f73ac3d0`：64 个非执行事件外层共享样例。两项仅测试/文档，本地全量、专项 race、根代理独立复验及双平台 CI 通过，仍待外部审核。
- #91/#96/#99/#100 在固定 main 的临时 detached worktree 组合后，四套 Go 参考测试共 217 个样例通过 race 验收；未推送组合分支。该证据仅证明 Go 参考测试共存，不代表跨语言、生产执行入口或 T01-E/T19 通过。
- #97 的已审核合并版本已安装到现有 monitor，9 个 Python 回归测试及真实 scan-only 验证通过；当前 main CI 与 SHA 对应正确。继续使用同一个 1800 秒调度，不创建第二个计时器。

## 历史记录：2026-09-10

以下保留当日记录，不代表当前 PR、分支或生态里程碑状态。

## 此刻在做什么

**当前里程碑**:M2 · L3 行为协议标准化。尚未开工写 behavior 代码——按 `roadmap.md`,**M2-F5(存储层加固)先于其余 Feature**,而那一层的缺陷比原先估计的多。

**进行中的 PR**:

| PR | 分支 | 状态 |
|---|---|---|
| [#35](https://github.com/iDoris-ai/Hyphae/pull/35) | `fix/storage-upsert-monotonic` | 🔄 OPEN,等外部评审裁决 |

#35 内容:`StoreMessage` 从 `INSERT OR REPLACE` 改为 UPSERT(`is_incoming` 单调、空值不再清空已有列),外加根治 legacy 迁移的 id 碰撞。**缺陷最初由 Agent24 会话在排查自己的入站活性探针时发现并写出补丁,随后主动交还**;本仓库独立复现后接手、扩展并收口。

**下一个该做的 Task**:`M2-F5-T1`(SQLite PRAGMA 没有真正生效)。它排在 `M2-F5-T2`(SaveOutbox 并发)之前,因为**底下的连接仍然 `busy_timeout=0` 会让任何并发修复看起来无效**——继续以 `database is locked` 收场,让人误判修错了地方。

## 阻塞项

| 项 | 卡在什么 | 影响 |
|---|---|---|
| `relay-khatru` fork(M1.5-F4-T1) | D3 未拍板:放 `AuraAIHQ/` 还是 `iDoris-ai/` | **不阻塞 M2**;阻塞 M2.5 |
| D1/D2/D4/D5(M2.5-F0-T1) | 邀请券上链 / 漂流瓶向量算法 / NIP-42 auth / 1对1 通道协议 | 阻塞 M2.5 开工 |
| `scripts/deploy-relay.sh` 的 `local`/`tunnel` | 硬编码 `examples/basic`,上游已改名 | 阻塞真实 relay 自部署演示 |

## 分支与 worktree

| worktree | 分支 | 用途 |
|---|---|---|
| `agent-speaker/` | `main` | 主 checkout —— 只读代码/盘点/合并,**不在这里开发** |
| `agent-speaker-storage/` | `fix/storage-upsert-monotonic` | PR #35 |

2026-09-10 清理:12 个已 squash-merge 的本地分支 + 2 个已合并 worktree(PR #33/#34)已删除。远程一直是干净的(GitHub auto-delete-on-merge 已开启)。

## 跟进账本

见 [`followups.md`](followups.md)。FU-2~FU-7 已全部升格为 `tasks.md` 里的 `M2-F5-*` 正式 Task(它们不再是「零散小事」,而是当前 Feature 的主体)。FU-1(release 打包自动化缺失导致 `install.sh` 404)仍是纯跟进项。

## 里程碑状态

| 里程碑 | 状态 |
|---|---|
| M1 | ✅ |
| M1.5 | 🔄 代码 10/10 done,M1.5-F4(fork 仓库)BLOCKED |
| **M2** | ⏳ **当前目标**,从 M2-F5 存储层加固开始 |
| M2.5 | ⏳ 待 D1-D5 拍板 |
| M3 / M4 / M5 | ⏳ |

## 近期决策记录

- **2026-09-10 · 里程碑编号撤回重映射。** 早先把 `M1.5→M2`、`M2→M3` 映射成整数以适配点号式 Task ID,现已撤销,改用 `M2-F5-T1` 这种 `-` 分隔。原因:原编号在 `protocol-v2.md`、`roadmap-v2.md`、`testing-integration-plan.md`、`specs/m1.5/`、GitHub issue 里到处都是且全都还活着,让「M2」在两套活文档里指不同的东西是永久陷阱,而换来的只是点号。
- **2026-09-10 · 第一个 READY 任务从「SaveOutbox 并发」改成「PRAGMA 只在一条连接上生效」。** PR #35 的评审证明后者会掩盖前者的修复效果(`busy_timeout=0` 会让并发修复继续以 `database is locked` 收场)。
- **2026-09-10 · T1 的描述经实测修正。** 初版只点名 `busy_timeout`。外部评审用 `mattn/go-sqlite3` 复核后认为该条不成立(那个驱动默认就是 5000),但本仓库用的是 `modernc.org/sqlite`,默认是 **0**——原判断对本仓库成立,却漏了一半:`foreign_keys` 和 `synchronous` 同样只在一条连接上生效,只有 `journal_mode` 幸免(WAL 是数据库文件属性,不是每连接状态)。带对照的实测数据已写进 `tasks.md` 的 T1。**方法上的教训:验证「没有 X 就退回默认值」必须去量那个默认值,而且要用本仓库真正在用的驱动量。**
