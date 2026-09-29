# 生态里程碑任务与 Luna 分工

更新：2026-09-29。范围依据 [生态里程碑草案](ecosystem-roadmap.md)。保留历史 M1～M5 编号；本表用 E-M 编号关联旧任务，不覆盖旧台账。

## 工作方式

- **主代理**：架构、契约、任务拆分、依赖协调、代码评审和验收；生产代码与测试实现交给 **GPT-6 Luna**。
- **Luna**：按任务单实现、补测试、提供可复现证据；接口变化先交主代理评审。
- 同时最多三位 Luna。每项实现使用独立分支/工作树；同一文件的修改串行，依赖通过验收后才派发。
- 每次派工固定：目标、基线 commit、允许修改的文件、输入/输出契约、依赖、验收命令、边界用例和交付物。
- 多个前置 PR 的共同开发基线使用 `integration/em1-cli-foundation`，仅组合已验收改动，不为它创建汇总大 PR。最初的组合验收快照为 `60715f3`，每次派工固定具体 commit。后续跨依赖的小 PR 暂以此分支为 base；不能把这些 PR 自身的改动提前合入其 base。前置 PR 合入主线后逐项改回 main，并核对差异及回归。主线尚未合并本轮 PR。
- 状态为 `WAITING → READY → IN_PROGRESS → IN_REVIEW → DONE`。`DONE` 需主代理验收；测试通过不等于整个里程碑通过。环境缺失单独记录，不能计作通过。
- 当前推进上游迁移、存储修复和 CLI 通信接口；基础 UI 与跨仓执行由协作文档约定后交对应仓库推进。

### 已核对基线

| 仓库 | 本地 HEAD | 使用位置 |
|---|---|---|
| Hyphae | `346f994` | 当前仓库；保留未提交的 `AGENTS.md`、生态路线图 |
| Agent24 | `32072b0` | `../Agent24` |
| AgentEar | `a26c914` | `../AgentEar` |
| iDoris | `074d35f` | `../iDoris-hyphae-review`；保留原 `../iDoris` 的未提交修改 |

这些是本轮本地审阅基线，不代表重新同步过远端。后续派工需再次检查工作树状态。

Agent24 正式后端为 Rust（`apps/desktop/src/main/backend-manager.ts`），模型和模块接线应进入 `rust/`；Node daemon 是参考实现。Nostr bridge 仍位于 `packages/nostr-bridge/`。已有设计 `docs/design/INTEGRATION-AGENTEAR-IDORIS.md`、`A3-ATTACHED-MODULE.md` 必须复用：AgentEar 已有 A3 附着；外部 OS 包通过 `agent24-os-packages` 发现，不把两种生命周期强行合并。

## E-M1：设计门槛

以下是主代理负责收口的设计约束。T01 分为基础 CLI 契约与高层行为契约两个检查点；基础 CLI 修复复用现有协议，高层行为实现须等待对应契约冻结。

### 三段交付

1. **A：CLI 通信主干。** 双身份在真实 relay 上加密收发、历史可查、失败入队、断线重连和重启补收；Hyphae 独立运行，Agent24 通过 CLI 适配。无 UI/模型依赖。
2. **B：基础 UI 通信管理。** Agent24 复用同一通信服务，先身份/联系人/relay/状态，再基础消息/待发与重试。Hyphae 提供机器可读接口与诊断，UI 由对应仓库实现。
3. **C：原有协作闭环。** behavior 收发、授权单步执行、持久化去重和回执；接入模型、语音、外部模块，保留原 E-M1 总出口。

各段分别验收；跨仓尚未完成时不得把本仓通过等同于整段完成。E-M2～E-M5 编号及目标保持不变。

1. **协议只有一份权威定义。** `protocol-v2.md` §4 仍用 `30078`，历史 `M2-F5-T5` 要求独立 behavior kind；T01 统一权威文档和速查文档，确定具体 kind、存储语义、版本与迁移策略。每种 behavior 不另分 kind；旧消息和 profile 继续可读。
2. **身份来自验证过的事件。** 发送者取已验签的 Nostr 公钥；正文身份不得覆盖签名身份。授权绑定发送者、接收者、能力、参数摘要和有效期。Agent24 执行授权与审批，Hyphae 搬运协议。owner-attestation 预留不能直接当执行权限。
3. **请求与回执分流。** 契约明确 `request_id`、版本、能力版本、有效期、请求/回执类型、关联事件及状态。`inquire` 的能力查询与可执行请求必须区分；回执、注册、订阅通知不得进入执行入口。先完成单步请求，不扩展自动协商。
4. **投递与执行各自记账。** relay ACK 只证明 relay 接收。发送重试复用已签名事件；Agent24 用发送者、接收者和请求 ID 持久化执行记录，相同 ID 不同参数拒绝。结果及回执待发记录可靠保存，回执重发不能重做任务。
5. **明确崩溃窗口。** 执行开始后、结果保存前崩溃，标记待核对并查已有 run。只有能确认未执行或执行器支持幂等时才能重试；无法确认则返回未知状态。验收包含副作用计数，不能仅统计消息数。
6. **模型约束到实际调用。** 隐私、预算、所需能力贯穿 iDoris 调度；记录真实 provider/model 和用量来源。禁止把估算用量标成实际值；本地不可用时，本地限定请求不得转外部。
7. **外部模块复用已有能力。** 先选一个真实、具有版本和权限声明的外部 OS/workspace，复用发现、安装、附着和停用机制。设备授权继续归 AgentEar；不在 E-M1 构建完整 workspace 产品。

T01 的交付包括：权威协议修改、字段/错误码表、跨仓共享正反例、兼容矩阵、执行状态图，以及单 relay 验收场景。kind 编号、编码/加密顺序和具体字段格式在该任务完成后冻结，不让各实现任务自行补齐。

## E-M1：可派发任务

表内编号省略 `E-M1-` 前缀。未注明者由 Luna 实现、主代理验收。命令见后文；表内同时规定必须新增的断言。

| ID | 任务与文件范围 | 依赖 | 验收出口 | 当前状态 |
|---|---|---|---|---|
| T01 | 主代理冻结四仓契约；Hyphae `docs/protocol-v2.md`、`docs/agent/spec.md`，Agent24 bridge 协议及模型/附着接口声明 | — | 上述七项设计收口；两端共享样例可明确判定接受/拒绝 | READY |
| T02 | SQLite 每连接 PRAGMA；`internal/storage/db.go` 与专门回归测试；承接 `M2-F5-T1` | — | 同时持有多条连接及重建连接均为 5000/1/1；真实外键拒绝；特殊路径正确；旧实现对照会暴露缺陷 | DONE（PR #38 待合并） |
| T03 | outbox 原子更新 API；`internal/messaging/outbox.go`、全部写入调用点；承接 `M2-F5-T2` | T02 | 独立锁文件覆盖完整读改写，唯一临时文件；多进程增删改不丢更新、JSON 可解析；不再保存过期快照 | DONE（PR #43 待合并） |
| T04 | outbox 重试并发与错误传播；outbox、命令及 daemon 调用点 | T03 | 重试的网络 I/O 不持有全局文件锁；写回只改目标记录；并发新增不丢、删除不复活；落盘失败不得报告已入队 | DONE（#48/#54/#56 待合并；组合故障验收另属 T18） |
| T05 | 重试保留明文与真实加密标记；outbox/store/daemon；承接 `M2-F5-T3/T6` | T04 | 先存解密明文再重试不覆盖；加密和未加密事件均准确；发布失败不改变加密属性 | DONE（#49/#57 待合并） |
| T06 | group UPSERT 保留字段；`internal/group/db.go`；承接 `M2-F5-T4` | T02 | 同 ID 空值更新不清明文；event_id 冲突行为有测试；群消息旧数据可读 | DONE（PR #46 待合并） |
| T07 | behavior 编解码与兼容读取；新增 `internal/behavior/`、`pkg/types/`；承接 `M2-F5-T5/M2-F1-T1` | T01 | 正反例跨语言一致；验签、版本、重复 tag、截断、解压上限、未知行为；旧 30078 不误解析 | WAITING |
| T08 | register/publish 收发与 CLI；behavior、profile、`cmd/hyphae/` | T07、T05 | 三种注册模式、能力版本可发现；广播只带允许公开的字段；CLI JSON 稳定；真实 relay 可查询 | WAITING |
| T09 | inquire/subscribe 收发与 CLI；behavior 及测试 | T07、T05 | 查询/回复关联正确，订阅过滤与退出正确；重复事件不重复通知；查询不触发执行 | WAITING |
| T10 | Agent24 → iDoris 适配器；Agent24 `rust/crates/agent24-models/src/router.rs` 及 provider/配置/测试 | T01 | 复用已有接入设计，接通 `IDORIS_URL`；超时、取消、不可用显式返回；mock 与真实服务分别验收 | WAITING |
| T11 | 隐私、预算、推理落点校验；iDoris router 与 Agent24 适配器 | T10 | 本地限定时外部请求数为零；预算拒绝不执行；审计与实际上游一致；用量标注实际或估算 | WAITING |
| T12 | 选定外部模块并固定权限/生命周期样例；Agent24 `rust/crates/agent24-os-packages/src/discovery.rs` 与真实项目 `domain-os.yml` | T01 | 主代理确认项目、commit、能力和授权范围；加载/停用/版本不兼容都有明确结果 | WAITING |
| T13 | 模块加载和单步能力调用；Agent24 `rust/apps/agent24d/src/{domain,attached,attached_routes}.rs`、AgentEar `src/a3.rs` | T12 | 从外部路径加载；拒绝未声明/未授权能力；脱离与重连不重复挂载；结果带请求关联；不扩大 AgentEar speak/stop_playback 命令集 | WAITING |
| T14 | Agent24 bridge 兼容新行为；`packages/nostr-bridge/src/{protocol,speaker,inbound}.ts` | T08、T09 | 新旧消息可读；完整发送者与事件 ID；只把获授权的执行请求送入 Agent24；回执不触发回复循环 | WAITING |
| T15 | 持久化接收与执行登记；bridge + Agent24 run 存储入口 | T14 | 同请求并发/重放/重启只登记一次；同 ID 参数冲突拒绝；无授权不得启动 run | WAITING |
| T16 | 授权后单步执行及崩溃恢复；Agent24 run/审批与 bridge | T15、T11、T13 | 审批等待不当作完成；拒绝不执行；执行记录关联 run；不确定崩溃窗口进入待核对；副作用不重复 | WAITING |
| T17 | 持久化结果与回执重试；bridge 回执存储/发送 | T16、T05 | 回执断线后补发；重启从已存结果发送；超时/重复回执不再次执行；发送错误可诊断 | WAITING |
| T18 | 合规 relay 验收工具与跨仓 fixtures；新增独立脚本/测试数据 | T01 | 临时身份、临时数据目录、relay 生命周期可复现；签名/过滤/替换语义可验证；记录四仓 commit | WAITING |
| T19 | 四仓真实联调；测试脚本与验收记录 | T06、T17、T18 | 下述三道门全部通过，语音→远端执行→播报及拒绝/断线/重启有证据 | WAITING |
| T20 | Agent24 CLI 通信接线；Hyphae 配套 JSON/连接诊断/恢复接口，Agent24 CLI 与 bridge | T01 基础契约、T02～T05 | 不启动 UI/模型即可双向通信；配置、错误、入队/relay 接受状态可读；离线消息补收；跨仓部分由用户推动 | IN_PROGRESS |
| T21 | Agent24 基础通信管理 UI：身份、联系人、relay 和连接状态 | T20 | CLI/UI 使用同一通信配置和服务；UI 不复制 Nostr 栈；对仓验收 | WAITING |
| T22 | Agent24 消息 UI：收发、历史、待发与失败重试 | T21 | 正确显示入队/relay 接受/对端确认；重复操作不产生错误状态；对仓验收 | WAITING |

T02～T06 是已有存储缺陷修复，可在 T01 期间推进，不改变跨仓契约。T03～T05 与 T14～T17 分别串行，避免改同一文件发生冲突。

T10/T11 需验证已有 `idoris-local`/`idoris-any` 设计与 `X-iDoris-Privacy`、`X-iDoris-Served-Locality`、Record-Id 的真实实现。回环 HTTP 地址不能证明推理在本地，缓存命中也必须保留原始落点。角色目录 Q-3 与预算核销接口在 T01 明确；避免在两个仓库重复实现预算账本。

T14 需为 Agent24 既有 `version/intent/thread_id/reply_to/topic/payload/expires_at` 信封定义逐字段映射。T15～T17 的执行状态归 Agent24；Hyphae daemon 只负责接收、投递状态和传输诊断，不能另起一套任务执行器。T06 仅修历史存储缺陷，不新增群组协作功能。

### 派发批次

1. 当前：上游迁移与测试基础、T02～T05 存储/重试修复、T20 的 Hyphae CLI 接口，先验收 A 段本仓能力；Agent24 CLI 接线交协作文档推进。
2. T21/T22 基础 UI 由 Agent24 推进；Hyphae 补齐真实接口缺口。模型线 T10→T11、模块线 T12→T13 可并行准备，不阻塞 A/B 段。
3. T01 高层契约冻结后：T07→T08/T09→T14→T15→T16→T17，完成 C 段。空闲槽位完成 T06/T18。
4. T18/T19 分别记录 A/B/C 验收证据；完整 E-M1 仍需四仓、真实 relay 和设备链路的原有出口通过。

T20 在 Hyphae 侧进一步拆小 PR：身份/联系人 JSON、outbox JSON 与错误传播、relay 配置/连接诊断、inbox 查询错误传播、daemon 重启补收及持久化去重。每项有独立错误/边界用例和真实 CLI 验收；Agent24 的命令名与 UI 不在本仓假实现。T04 再拆为重试结果事务与发布前可靠入队两步，避免一次 PR 同时改所有收发路径。

### 验收命令与证据

以下命令来自各仓库现有入口；新增行为断言和四仓脚本仍需在对应任务实现。列出命令不表示已运行或已通过。

| 位置 | 命令 | 使用范围 |
|---|---|---|
| Hyphae | `go test ./internal/storage/... -race -count=1` | T02 |
| Hyphae | `go test ./internal/messaging/... ./internal/storage/... ./internal/daemon/... -race -count=1` | T03～T05 |
| Hyphae | `go test ./internal/group/... -race -count=1` | T06 |
| Hyphae | `go test ./internal/behavior/... ./pkg/types/... -race -count=1` | T07～T09，新包创建后可运行 |
| Hyphae | `go test ./...`、`./build.sh`、`./test.sh` | Hyphae 完整回归与 CLI；脚本须先确认数据隔离 |
| Agent24 `rust/` | `cargo test -p agent24-models` | T10/T11 正式模型入口 |
| Agent24 `rust/` | `cargo test -p agent24-os-packages`、`cargo test -p agent24d` | 模块、附着、run；按代码范围补充相关 crate |
| Agent24 | `pnpm --filter @agent24/nostr-bridge test`、`pnpm --filter @agent24/nostr-bridge typecheck` | bridge |
| Agent24 | `pnpm test:contract` | 跨组件契约 |
| iDoris 独立工作树 | `pnpm --filter @idoris/router test`、`pnpm --filter @idoris/router typecheck` | 模型路由；按实际修改范围追加依赖包测试 |
| iDoris 独立工作树 | `pnpm smoke:agent24` | 已有接入 smoke；不等于实际 Agent24 Rust provider 已接通 |
| AgentEar | `cargo test --test contracts` | host 契约；附着代码改动后追加相关 Rust 测试 |
| AgentEar | `scripts/e2e-agent24.sh` | 已有 Agent24+AgentEar 语音集成；依赖 release 二进制、ASR/本地模型与 macOS 音频工具 |

**三道门**：单元/契约 → 真实 relay 与二进制 → 四仓真实联调。`scripts/minirelay.go` 可用于基本传输检查；其简化实现不能单独证明标准 relay 的签名、过滤和可替换事件语义。

T19 必交矩阵：正常语音链路、未授权发送者、能力越权、审批拒绝、本地模型不可用、预算不足、relay 断线、重复请求、执行前后重启、回执丢失、旧事件兼容、回执防循环。每项记录输入、预期/实际、执行次数、请求/事件/run ID、实际模型落点与退出码；密钥和生产数据不进记录。

真实语音验收需要麦克风/扬声器和设备权限；缺少条件时记录未验收，音频 fixture 测试不能代替设备链路通过。E-M1 只需一套实际可用的本地模型与授权能力，不以全模型/全平台兼容作为出口。

## E-M2～E-M5：后续工作包

前一里程碑验收时，由主代理把下一阶段展开到与 E-M1 相同的任务粒度；当前均为待细化，尚未派发。

| 编号 | 子任务工作包（按顺序） | 里程碑出口 |
|---|---|---|
| E-M2 | T01 自部署 relay 打包/配置/健康检查；T02 邻居与转发契约；T03 多 relay 路由；T04 TTL/去重/断链；T05 资料分级披露；T06 漂流瓶匹配；T07 网络联合测试 | 多 relay 可达、环路不放大、TTL 可终止、private 不出本机；部署/邀请/匹配的旧 D1～D5 决策逐项收口 |
| E-M3 | T01 任务与协商状态机；T02 能力/条款协商；T03 委派与授权收窄；T04 取消/超时；T05 结果验收/拒收；T06 冲突恢复联调 | 双方任务状态可对账；取消与完成竞态有确定结果；委派不能扩权 |
| E-M4 | T01 持久订阅触发；T02 持续匹配；T03 Agent24 调度；T04 断线恢复/补偿；T05 活动记录；T06 长跑与循环防护 | 在设计阶段固定长跑时长、故障注入和资源上限；验收持续运行、恢复及消息环路终止 |
| E-M5 | T01 授权/计费契约；T02 信誉证据与更新；T03 AAstar Point 测试网支付；T04 账本与对账；T05 重放/重复支付/失败恢复；T06 测试网联合验收 | 授权、交易、任务一一关联；重复请求不重复扣款；余额/交易/任务账可核对 |

## 本轮派工记录

- `luna_storage` 初始实现、`luna_nostr_update` 补齐验证：T02 位于 `../Hyphae-em1-sqlite`，分支 `fix/em1-sqlite-pragmas`，提交 `1269744`、`a31b075`，见 [PR #38](https://github.com/iDoris-ai/Hyphae/pull/38)。
- `luna_network`：已完成通信线只读核查，结论已纳入 T03～T09/T18。
- `luna_integrations`：已完成 Agent24/AgentEar/iDoris 只读核查，正式 Rust 入口和接口缺口已纳入 T10～T17。
- `luna_nostr_update`：上游依赖更新见 [PR #40](https://github.com/iDoris-ai/Hyphae/pull/40)；T03 见 [PR #43](https://github.com/iDoris-ai/Hyphae/pull/43)，T04a 重试结果事务见 [PR #48](https://github.com/iDoris-ai/Hyphae/pull/48)。当前转入独立工作树修复 T05 的重试历史明文与加密标记。
- `luna_relay_migration`：维护中的 khatru relay 与部署脚本见 [PR #42](https://github.com/iDoris-ai/Hyphae/pull/42)，身份/联系人 JSON 见 [PR #45](https://github.com/iDoris-ai/Hyphae/pull/45)，T06 见 [PR #46](https://github.com/iDoris-ai/Hyphae/pull/46)；真实 CLI/relay 集成夹具见 [PR #47](https://github.com/iDoris-ai/Hyphae/pull/47)，双向验收已通过。当前转入独立 relay-query 工作树实现真实 EOSE、超时与断线的共用查询模块。
- `luna_upstream_ci`：测试后自动提依赖 PR 的配置见 [PR #44](https://github.com/iDoris-ai/Hyphae/pull/44)，已通过 GitHub 全量、构建、实际工作流脚本回归与 core race 检查；当前转入 `Hyphae-cli-relays` 做 relay 配置与入口接线。定时任务尚未上线，需配置合入默认分支并确认 Actions 创建 PR 权限。
- 本轮不修改其他仓库的生产代码；对应仓库的协作约定见 [PR #41](https://github.com/iDoris-ai/Hyphae/pull/41)。设计和验收材料由主代理维护。

### T02 验收记录

- 实现：`net/url` 构造 file URI，通过重复 `_pragma` 参数逐连接设置；WAL 保持初始化时设置，不改 schema。
- 主代理已静态审阅实现和连接替换/旧写法对照测试；Luna 已按评审意见补充合法外键插入、外键约束错误断言及实际数据库路径检查。
- `git diff --check` 已通过。
- 工具链问题已解决：使用校验过官方 SHA256 的临时 Go 1.27.1，不修改用户全局安装。Luna 已完成格式化与测试。
- 主代理在隔离 HOME 下独立复跑 `go test ./internal/storage/... -race -count=1`、`go test ./...`，均通过。
- 结论：T02 本仓验收通过，T03 已解锁。PR #38 已提交待评审，尚未合入主线；CLA 属于独立合并检查，不替代测试结论。

### T03 与组合验收记录

- T03 提交 `3ff4771`：跨进程锁、唯一临时文件、文件与目录 fsync；所有生产读改写走最新磁盘状态。可选 `queue_id` 区分同一事件重新入队，清理确认保留新项。Linux/macOS 支持锁；升级须先停止旧版写入进程。
- Luna 全量测试与 Linux/arm64 编译检查通过；主代理独立复跑 `go test -race ./internal/messaging ./internal/daemon ./internal/storage -count=1` 通过。
- 本地 `review/em1-cli` 工作树组合 `29b36fd`、`5eb9e03`、`a31b075`、`3ff4771` 后，`go test ./...` 与 CLI 构建通过。此分支用于验收，未合入远端主线。
- 使用实际 CLI 与本地 khatru 二进制、临时 Alice/Bob HOME 和 relay 存储，双向 NIP-44 加密发送、relay 接受和收件解密均通过；未使用公共 relay 或生产身份。
- 这只证明组合后的基础双向收发。T04/T05、管理接口、断线重试、超过十条离线积压补收与重启去重仍待验收，A 段尚未整体通过。

### T20 管理接口进度

- 身份/联系人 JSON：`36b8641` / PR #45 验收通过。Luna 全量测试通过，主代理独立 `go test -race ./internal/identity -count=1` 通过，含实际 CLI 子进程测试；覆盖创建、默认身份、联系人列表和规范化公钥、空数组、环境开关、非交互密码错误与磁盘失败。
- 加密库新增身份保持加密；已有未加密身份的库须先使用 `identity change-password` 完成整库加密。JSON 管理输出使用公开字段白名单。
- outbox JSON/可靠入队、inbox 错误传播、daemon 离线补收仍待后续小 PR，不能据此宣称 Agent24 CLI/UI 已接线。
- relay 配置/探测：`f2c62e2` / PR #50 验收通过。主代理独立 relayconfig/common/nostr race 通过；组合后的实际 CLI 在临时 HOME 保存本地 relay 后，无显式 --relay 的 info、加密发送和收件均使用该配置并通过。outbox 旧空地址条目的回退接线仍待完成。
- 首次收件登记：`b913bd9` / PR #51 验收通过，主代理独立 storage/messaging race 通过。SQLite 原子条件写入覆盖并发、重新打开数据库、发件升级为收件和收件人冲突；daemon 调用点仍在实现。
- 单次 relay 查询：`d70b751` / PR #52 验收通过，主代理独立 relayquery race 通过。关闭 SDK 本地伪 EOSE，处理真实 EOSE/CLOSED、取消/断线竞态及 NIP-67 提示；不能确认完整时返回错误。inbox 和 daemon 的调用点、历史分页另行接入。
- 基线追加 #51/#52 后为 `317fb82`；新的可靠发送、daemon 收件接线、outbox list/clear JSON 分别在独立工作树推进。
- outbox list/clear JSON：`073574c` / [PR #53](https://github.com/iDoris-ai/Hyphae/pull/53) 验收通过。真实 CLI 子进程覆盖安全字段、空数组、清理确认和输入校验；主代理独立 messaging race 通过。
- 可靠发送：`7c6876a` / [PR #54](https://github.com/iDoris-ai/Hyphae/pull/54) 验收通过。先存历史和已签名待发记录，再发布；错误保留事件 ID、relay ACK 和队列状态。主代理独立 messaging/common/daemon/storage race 通过。
- daemon 收件接线：`45d80ca` / [PR #55](https://github.com/iDoris-ai/Hyphae/pull/55) 验收通过。有界解压与解密失败不写明文，SQLite 成功后才记 seen/触发效果；初始化数据库失败后可在同一进程恢复。主代理独立 daemon/messaging/storage race 通过。它不保证落盘后崩溃仍会通知或自动回复。
- 新组合基线 `integration/em1-cli-reliability` / `6e64aaa` 包含上述三项；隔离 HOME 的 `go test -tags integration ./... -count=1` 通过。#53～#55 的 base 仍为 foundation，未把 PR 自身合入其 base，也未创建汇总大 PR。新的 retry JSON、inbox 查询接线、可靠自动回复使用 reliability 为 base，各自独立 worktree。
- retry JSON：`f98f820` / [PR #56](https://github.com/iDoris-ai/Hyphae/pull/56) 验收通过；真实 CLI 与 khatru 覆盖 ACK 后清队列、原签名 ID、配置回退、不可达仍排队及历史落盘失败的部分结果。主代理独立 messaging race 通过；并发替换统一返回 write_conflict。
- 自动回复：`bbd2ac0` / [PR #57](https://github.com/iDoris-ai/Hyphae/pull/57) 验收通过；加密失败停止，独立随机 d，复用发送前持久化与精确队列事务，断线后重试同一签名事件。主代理独立 daemon/messaging/storage race 通过。
- #53～#57 保持 draft 供后台 PR-daemon 评审；前置合入 main 后 retarget，不合入临时 integration。顺序与门槛见 [PR 依赖表](em1-pr-order.md)。
- inbox 查询接线：`77f5806` / [PR #58](https://github.com/iDoris-ai/Hyphae/pull/58) 验收通过。单页真实 EOSE、全失败与部分结果、事件去重/排序/limit、错误不写占位明文；关闭解密时锁定身份仍可只读。主代理独立 messaging/relayquery race 通过；实际二进制错误输出也已验证。此命令不承诺全量历史分页。
- daemon 生命周期：`561af2c` / [PR #59](https://github.com/iDoris-ai/Hyphae/pull/59) 验收通过。interval 非正值/溢出在触盘前拒绝；SIGINT/SIGTERM 取消当前网络等待，取消后不继续重试后续队列项。主代理独立 daemon race 通过；test-helper 子进程在停滞 WS 上收到 SIGTERM 后两秒内退出。
- `integration/em1-cli-recovery` / `c2f3651` 再组合 #56～#59，主代理隔离 HOME 的全量 `-tags integration` 测试通过。实际断线发送/重试 fixture、加密身份解锁与离线分页继续独立实现。
- 有界分页：`082f5fd` / [PR #60](https://github.com/iDoris-ai/Hyphae/pull/60) 验收通过；包含边界秒、去重、NIP-67 提示、取消、100 页/10000 事件/30 秒上限，以及无法前进时的未完成结果。主代理独立 relayquery race 通过。此 PR 尚未接入 daemon。
- 离线真实 CLI：`376a48c` / [PR #61](https://github.com/iDoris-ai/Hyphae/pull/61) 验收通过；实际发送入队、relay 重启后按原签名重试、对端解密与重复查询、两端历史明文核对。实际 daemon 二进制的停滞网络 SIGTERM 退出也通过；主代理独立 integration tests 通过。
- relay 连接清理：`a9aebe1` / [PR #62](https://github.com/iDoris-ai/Hyphae/pull/62) 验收通过；探测失败时也关闭 SDK 返回的非空连接。主代理独立 nostr race 通过。
- 加密身份解锁：`1bdf7bb` / [PR #63](https://github.com/iDoris-ai/Hyphae/pull/63) 验收通过；实际 CLI 测试覆盖显式 stdin、错误或缺失凭据、加密发送与收件，以及不解密时不读密码。主代理独立 identity/messaging/daemon race 通过。
- `integration/em1-cli-runtime` / `b1cbaaa` 再组合 #60～#63，主代理隔离 HOME 的全量 `go test -tags integration ./... -count=1` 通过。daemon 历史分页接线与实际 125 条积压/重启验收待完成。
- 创建身份的 stdin：`cefebc8` / [PR #64](https://github.com/iDoris-ai/Hyphae/pull/64) 验收通过；首次加密创建、向加密库追加、错误不改原文件、输入方式冲突和机器模式禁止提示均有真实 CLI 测试。主代理独立 identity race 通过。
- daemon 分页接线：`eb79c30` / [PR #65](https://github.com/iDoris-ai/Hyphae/pull/65) 验收通过；移除启动时间下界和 limit=10，坏事件不阻塞后续有效消息，查询错误报告未完成。103 条同秒积压、数据库重开去重、写盘失败恢复和取消均通过；主代理独立 daemon/relayquery/messaging race 通过。
- `integration/em1-cli-backfill` / `916fc1f` 组合 #64/#65；Go 1.25.0 全量 integration、vet、build 通过。smoke 脚本发现空历史统计的 SQL NULL 错误，修复与最终 125 条积压验收仍在推进，不能据此提前记 A 段通过。

### T04/T06 验收记录

- T04a 重试结果事务：主代理静态复核及独立 messaging/daemon/storage race 通过。网络调用前锁内确认 QueueID，失败使用最新重试次数，成功只移除相同队列项；并发删除不恢复，新入队项不被旧操作删除。relay ACK、历史落盘、队列状态分别报告；rename 后目录同步失败报告状态不确定。T04 的发布前入队和 CLI 错误传播仍待完成。
- T06 `a8a1a0b` / PR #46：使用按主键 UPSERT 保留空值更新前的正文和事件 ID；缺失事件 ID 存 NULL，不同消息的重复非空事件 ID 明确失败并保留旧记录。Luna 全量测试通过，主代理独立 group race 通过。
- T05a `09b869b` / PR #49：NIP-44 重试用空 plaintext 保留已有明文，未加密事件按压缩标签还原正文，未知编码或损坏压缩明确失败。实际 SQLite 回归、主代理独立 messaging/storage race 均通过；daemon 自动回复的加密错误处理与属性修复尚未包含。

### T18 本仓夹具与组合验证

- PR #47 `289e539`：构建实际 CLI 与 relay，使用两套临时 HOME、临时 relay 数据、回环端口；双向 NIP-44 收发、签名校验、relay 重启后事件仍可查询、双方历史的事件 ID/正文均通过。主代理独立运行 `go test -tags integration ./tests -count=1` 通过。
- 本地 `review/em1-cli` 已组合 #38/#40/#42/#43/#45/#46/#47/#48；主代理隔离 HOME 执行 `go test -tags integration ./... -count=1` 通过。该分支只用于组合验收，没有创建合并这些改动的大 PR，也没有合入远端主线。
- 加入 #49/#50 后，`60715f3` 再次通过上述全量与 integration 检查，作为共同开发基线；可靠发送、原子首次收件和查询完成判定分别在独立工作树继续实现。
- 待补：断线入队/重试、daemon 离线积压与重启去重、Agent24 基础 CLI/UI 和四仓高层链路。T18/T19 保持未完成。
