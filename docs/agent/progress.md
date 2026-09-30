# Progress — 生态里程碑进展

最后更新：2026-09-30。本文记录实际交付与验收边界；PR 状态截至当天 13:33 UTC 的最后一次核对，新审批及合并以 GitHub 为准。

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
