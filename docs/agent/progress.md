# Progress — 生态里程碑进展

最后更新：2026-10-01。本文记录实际交付与验收边界；各节固定其对应版本，新审批及合并以 GitHub 为准。

14:03 UTC：#105–#117 已按记录中的审批 head 独立合入；最终 main `57494422d17051dc5440d2dbed9411df8acd5536`，main CI `36871057418` SUCCESS。#118 runtime (`94532e77`) 四项 CI SUCCESS、待 review；本机 full/race/9 项真实 relay 检查通过，日志在 `build/agent-handoff/20261001/g2-runtime-main/`。#119 R2b (`0114d622`) 仍 OPEN；actionlint、39 项 Python 和生产 lock 步骤通过，artifact run `36873124352` 的 lock 校验、Linux/macOS 构建与制品执行均 SUCCESS；普通 PR CI `36873124154` 仍待最终核对。生产 lock 固定 Agent24 `65a5c511…`，SHA256 `fbb96d21…`。Release 未发布，R3 未开始。Agent24 main 为 `f5a76c01`；#626 `c6d6688a` 双 Rust CI 因 clippy `double_must_use` 失败；#635 `a24c09b5` CHANGES_REQUESTED 且双 Rust CI 失败；#627 新 head `7ef5704c` draft、#633 `8c006b1e` draft。主仓 checkout 仍是 `b1ddf97` 且保留未跟踪 `AGENTS.md`、`tmux.sh`；launchctl 已核对定时服务 disabled/not loaded，本轮未改主仓。T20 仍 IN_PROGRESS，T01-E、生产 T07、T19 四仓验收未完成。旧 `/tmp` worktree/原始诊断日志当前不可用且未恢复；固定第一轮 artifacts、tracked acceptance 与新 G2 日志仍在。完整接手清单见 [handoff](handoff-20261001.md)。

12:33 UTC：#104 head `aaa6d0f` / CI `36861579126` 与 #115 head `2fa6f0b` / CI `36861616162` 均完整 SUCCESS；#104 的双平台检查及 `ci-ok` 已通过。#103 `9ee5cca` / CI `36859748709` 成功；#102/#105/#106 Python discovery 两平台实跑分别 16/20/23 项通过；#111–#114 CI 绿待外审。#104 Windows compile 旧失败/新通过，daemon race 9.415s。G2-S2b 近期 3 项 FAIL 中两项为新测试断言错误：标准分页应为 3 页而非 2 页，另一个“stderr 不得含正文”超出现行契约（状态设计允许 stderr 诊断/明文）；两处已校正。`TestDaemonCLIConflictPrecedesPasswordRead` 在完整套件重复时 3 秒超时，focused 单项通过但根因未证实；补入 #104 generic helper 后继续跑全量/race，不能称已修复。保留所有原始日志及其路径；四项此前通过仍限于原范围，不能记第二轮全通过。定时扫描保持停止；全 E-M1 未完成。

12:25 UTC：iDoris T01-D 接口审阅更新见 [iDoris 协作记录](../cooperation/iDoris.md)；只读固定 iDoris `ffed37a` / Agent24 `f5a76c01`，未运行服务/编译/付费 provider。确认 iDoris Rust router 已有 SQLite ledger 模块但正式入口未启用；Agent24 默认 provider 尚未接线。T10/T11 先服务端合同、后专用 adapter；T01-E/T07 门槛不变。

12:46 UTC：#102、#103、#104 最新 head 分别获外部 APPROVED 和四项检查绿，根代理核对独立差异后正常 squash merge：#102 `3be99fd`（12:42:25Z，CI `36863563740` SUCCESS）、#103 `5f179d6`（12:43:56Z）、#104 `b1ddf97e3dce74de3eaf19784c174543b7b5831c`（12:44:33Z）。当前 Hyphae main `b1ddf97` 的 CI `36863806313` 已完整 SUCCESS（macOS、Ubuntu、ci-ok）。三个已合并 PR 的旧 worktree/本地分支已删除；确认远端同名分支已自动删除；原 main checkout 与 untracked `AGENTS.md`、`tmux.sh` 保留。独立 #113 head `3e9baf3` 的 CI `36862751523` SUCCESS，待外审。S2b 新候选 `c55ffcd` full/race 44.182s 通过，真实 relay 配置、125 条补收/重启及 SIGTERM/SIGKILL、断线、broken pipe 已运行；仍有 `TestDaemonCLIConflictPrecedesPasswordRead` 完整套件重复 3 秒超时三次，focused 单项通过但根因未证实（psS+SIGQUIT 未取到 Go 栈）。标准分页页数与 stderr 正文禁令两项新增断言已纠正；这不能证明生产 acceptance，也不能记作 timeout 根因修复。原始日志均保留。#103 非阻塞 O_NOFOLLOW/path 防护建议另交 CLI Luna 做独立小 PR，不改已合并 head。S2b 生产提交仍等 #114/#115 合入；R2b 仍等 #105/#106 合入，不做汇总 PR。E-M1 未完成。

13:17 UTC：已在 [共享合同样例任务](../cooperation/Agent24-contract-fixtures.md) 增列固定 #110 T01-C 的 102 个执行恢复参考消费任务（固定 head、文件 SHA、102 个唯一 ID、逐项 action/state/reason 与无跳过输出）；只作参考，不冻结规范或宣称副作用/四仓验收。Agent24 #626 `c6d6688a` Rust 双平台 CI `36864127120` 失败，日志为 clippy `double_must_use`（async_trait 生成 Future 与 `-D warnings`，见 `agent24-tools`、`agent24-domain`）；这不是已证实的 Hyphae 协议缺陷。#635 `ee4c0327` 收到 CHANGES_REQUESTED，CI 绿；#627/#633 仍 draft。Hyphae #116 `b87cd8c` 双平台 CI `36865178936` SUCCESS、READY_FOR_REVIEW；PR 正文仍如实注明本地 daemon 全量套件超时未解决，不将 CI 绿称作根因修复。#105–#115 等待外审。另已在 [#601 更新](https://github.com/iDoris-ai/Agent24/pull/601#issuecomment-5932203767)记录新 daemon lock/main CI、旧 a4 制品无源码锁（缺锁不证明静止）及 SQLite retry 应清空 staging；没有改冻结制品。

13:15 UTC 诊断补充：根代理读取了权限 `0600` 的 `/tmp/hyphae-g2-status-prep/logs/daemon-inittrace-final-capture.log`：cold `--version` 无 stdin/identity，2.5 秒时仍为 ps `S`，SIGQUIT 后 2.97 秒结束，stdout/inittrace 为空；诊断用例 SKIP，随后同 binary 原持锁空密码用例 0.90 秒 PASS。`spawn-to-first-inittrace=2.97s` 实际是 reader EOF，不能解释成 Go 启动时间。独立 task 已交 CLI Luna，要求 cold `--version` 单次有界 15 秒硬验收与原 3 秒锁测试分开，无重试/skip；尚未交付。S2b main 组合最近 9 项真实 CLI/relay case 已由根代理读原日志确认 PASS，但不据此宣称完整验收通过。

12:04 UTC 补充：Agent24 自有文档 [#634](https://github.com/iDoris-ai/Agent24/pull/634) 已获 `clestons` 对 `c7bcf702` 的外部 APPROVE，六项检查通过；根代理重新核对 main base 与唯一文件 +17/-3 的实际差异后按 head SHA 正常 squash merge。合并为 `f5a76c015a7026c64fc872f47c6c160485cfed37`，该新 main 的 [CI 36859330529](https://github.com/iDoris-ai/Agent24/actions/runs/36859330529) 正在运行。已清理自有干净 worktree/本地分支，远端分支确认自动删除。Hyphae G7 [#111](https://github.com/iDoris-ai/Hyphae/pull/111) `74c47319`、G9 [#112](https://github.com/iDoris-ai/Hyphae/pull/112) `c53a09f0` 也已独立发布。另核对发现 #102/#105/#106 原 ci.yml 尚未执行各自 Python 工具单测，已派 Luna 给三个 PR 加同一自动发现步骤；原 CI 的 Go/integration 通过不作为这些 Python 工具的双平台验收。

## 2026-10-01 11:58 UTC：独立交付进入评审，CI 缺陷已定位

本轮继续 E-M1，定时扫描保持停止。Hyphae main 仍为 `fc6681c6fb605e734a4c819faf3e0abf5890d7e8`，该 SHA 的双平台 CI 通过。根代理核对源码、实际专项及全量日志后发布以下独立 main PR；均未自行 APPROVE，等待外部评审。

| PR | 固定 head | 交付与当前证据 |
|---|---|---|
| [#102](https://github.com/iDoris-ai/Hyphae/pull/102) | `26bfe6ab` | R1 可复现制品构建工具；真实 CI 通过 |
| [#103](https://github.com/iDoris-ai/Hyphae/pull/103) | `685e5dd3` | G8 完整 keystore 写事务；真实 CI 失败，Luna 修复中 |
| [#104](https://github.com/iDoris-ai/Hyphae/pull/104) | `82228178` | 同 HOME daemon 互斥；真实 CI 失败，Luna 修复中 |
| [#105](https://github.com/iDoris-ai/Hyphae/pull/105) | `fbf51599` | 固定制品第一轮工具；本机真实 relay 九阶段通过，工具 PR 的双平台 CI 通过 |
| [#106](https://github.com/iDoris-ai/Hyphae/pull/106) | `d0604af8` | 独立生产 lock、归档和解包校验；实际九文件 bundle 校验及双平台 CI 通过 |
| [#107](https://github.com/iDoris-ai/Hyphae/pull/107) | `b3e7b441` | G6 非法联系人 key 分类；本机全量/专项/race 通过，CI 运行中 |
| [#108](https://github.com/iDoris-ai/Hyphae/pull/108) | `c0fc0a5c` | G7b 存储信息 JSON；只读查询及三种机器开关已测试，CI 运行中 |
| [#109](https://github.com/iDoris-ai/Hyphae/pull/109) | `1713f7cc` | G1 只读口令校验；独立于 G8，本机全量/identity/race 通过，CI 运行中 |
| [#110](https://github.com/iDoris-ai/Hyphae/pull/110) | `18dd9dd7` | T01-C1 的 102 个恢复候选样例；仅测试/文档，本机普通/race 通过，CI 运行中 |

#103 的测试子进程继承临时 HOME 后再次进入 TestMain 构建 CLI；#104 的 signal 测试子进程也没有走已有 skip-build 分支。实际日志同时包含模块下载、子进程失败和临时目录内只读模块文件清理失败。Luna 正修 helper 启动路径和父进程构建缓存传递，保留跨进程锁与 SIGTERM 的真实断言，并用不预设 Go 缓存变量的环境复验。失败日志保留；不靠重跑掩盖失败，也不把它们记成 main 失败。

本机另发生磁盘不足：仅剩 116 MiB，临时文件创建失败。已暂停构建，核对归属后仅移除本轮四个临时 Go build cache，保留源码、日志、已构建制品、模块缓存及用户全局缓存；可用空间恢复约 2.3 GiB。后续构建复用规定的 GOPATH/GOCACHE 并串行，不再为每项复制大缓存。

下一批：Luna 整理 G2-S1/S2a 独立 PR，并在本地依赖基线实测 S2b 的 125 条补收、重启零新增、失败/取消及 JSON-lines。R2b 只在 #102/#105/#106 全部合入后基于新 main 发布自身 397 行生产差异，随后运行真实双平台制品构建、传输与 runtime CI；R3 Release URL 待这些出口通过后交付。G7/G9 继续独立发布，不合并任何 integration 汇总分支。

Agent24 main `92f844ee`；#626 `ba30f104` 已获有效批准、六项检查通过但仍 OPEN，#627 为依赖草稿。自有文档 [#634](https://github.com/iDoris-ai/Agent24/pull/634) `c7bcf702` 的六项检查通过、待外部评审。对仓生产代码由其推进。T20 仍缺最终 main 的实际托管/收发与三类计数有效正对照，T21/T22 基础 UI、T01-E 契约验收和 T19 四仓闭环仍未通过；#110 的样例不替代实际执行或跨语言消费。

## 2026-10-01 11:40 UTC：运行限制解除，真实第一轮通过并恢复发布

当前会话已实际切为完整本地访问/网络开启：loopback TCP bind/listen PASS，本机 gh 的 jhfnetboy 登录校验 exit 0。之前受限 shell 的 token-invalid 输出不能作为真实失效结论。GitHub connector 的账号连接与 gh 独立；本轮按既有授权使用已验证的 gh 路径，未修改 connector、保护规则或自行提交 PR APPROVE。Goal 实际恢复为 active，定时扫描保持停止。

- Hyphae [#101](https://github.com/iDoris-ai/Hyphae/pull/101) 最新 head 的外部批准、四项检查及实际测试-only差异重新核对后，按 SHA 正常 squash merge；main `fc6681c6fb605e734a4c819faf3e0abf5890d7e8` 的 [CI 36855662206](https://github.com/iDoris-ai/Hyphae/actions/runs/36855662206) 实际成功。原已合并干净 worktree/本地 receipt 分支已清理，远端分支确认不存在；主仓旧 checkout、AGENTS.md/tmux.sh 保留。
- GPT-6 Luna 使用原 a4/Go1.26.4 的真实 CLI `f53c29b3…`、relay `a012d86e…` 和原生产 lock，独立正式第一轮运行一次 exit 0。根代理核对 [原始阶段日志](acceptance/20261001-locked-round1/run.log)、[精确无口令命令](acceptance/20261001-locked-round1/exact-command.txt)、[范围报告](acceptance/20261001-locked-round1/report.md) 与 runner 实际断言：双向加密 event_id/正文对应、拉取前 history 为空、断线可靠入队、重启 relay 原 event_id 重试、outbox 清空和重复拉取后历史恰一条。错误项为错口令、非法 contact 公钥、未知发送联系人；最后一项不冒充 Agent24 的非法 npub 发送测试。此前 EPERM 失败证据保留。
- R1 构建工具 [#102](https://github.com/iDoris-ai/Hyphae/pull/102) `26bfe6ab`、G8 完整 keystore 事务 [#103](https://github.com/iDoris-ai/Hyphae/pull/103) `685e5dd3`、daemon HOME 互斥 [#104](https://github.com/iDoris-ai/Hyphae/pull/104) `82228178` 已分别提交、push、提 PR；生产新增/删除合计 343/407/97 行。最新基线的全量 Go 和对应专项/race/工具测试已通过，真实 GitHub CI/外部 review分别等待，未提前合并。
- Agent24 [#601](https://github.com/iDoris-ai/Agent24/pull/601) 已由对方合入 main `92f844ee`，对应 CI `36849380543` 成功。Hyphae 已实际在 [#601 回帖](https://github.com/iDoris-ai/Agent24/pull/601#issuecomment-5930558677) 确认 history 本地读、默认 30s 补收的边界、第一轮固定制品结果及后续交付。独立纯文档 [#634](https://github.com/iDoris-ai/Agent24/pull/634) `c7bcf70` 修正接口表并记录同 hash 复测；未修改对仓生产实现。
- #626 当前 `ba30f104` 已修孤儿标记并获新的外部批准、CI 全绿；不能继续记录为当前 REQUEST_CHANGES。#627 `500cc03e` 仍依赖草稿，由 Agent24 推进前置合并/迁移/正式 main 联调。#630 的候选组合通过记录不能代替最终 main 的 run/model/module 零计数及同量具有效正对照、125 条积压/重启零新增、持久凭据重启或基础 UI。

下一项并行推进其余独立 CLI 修复、第一轮复测工具、固定制品校验与真实两平台 Actions；R3 仍需真实 CI/下载前置，尚无新 Release URL。制品工具准备中曾同时启动三个全量 Go 与 relay 测试，触发已有 8秒 CLI timeout/relay readiness 失败；原日志保留，资源争用仅是推断。后续昂贵本地测试改共享 flock 串行；受控执行通过与初次失败分别记录，不以重跑通过宣称根因已修复。生产 T07 仍等 T01-E，T21/T22 和四仓出口均未完成，E-M1 保持未完成。

## 2026-10-01 11:16 UTC：按用户要求重新实测执行限制和连接账号

本地 `127.0.0.1:0` bind/listen 仍返回 errno 1 / EPERM。实时核对 Hyphae #101 head `b3f9053e`，匹配外部 APPROVED，macOS/Ubuntu/ci-ok/CLA 均成功；实际差异仅测试/文档。按既有授权重试一次 SHA 绑定正常 merge，仍在执行前被 `MCP tool call requires approval, but approval policy is never` 拒绝；复查仍 OPEN。此处是工具运行许可，不是 PR review，本代理没有提交 APPROVE。

新查到 GitHub connector 实际登录 `muziknozik`，该连接对 Hyphae 的权限响应 push=false；与此前 gh 登录和 PR 作者 `jhfnetboy` 不同。账号连接和当前 workspace-write/network restricted/never 运行策略须分别修复。Luna 只读检查没有找到用户配置中的权限设置或指定项目、系统、MDM 配置；ps 也被 EPERM 拒绝，具体启动覆盖来源仍未知，不能宣称是某一配置文件导致。本机 gh 报告 token invalid，但当前网络限制使此结果不能独立证明 token 真失效。

证据和已用本机 CLI 0.159.3 help 核验的恢复命令保存于生成物 `build/agent-handoff/20261001/permission-diagnosis-1116.md`。尚未修改宿主权限、账号连接或发布；E-M1 和 Goal 原状态保持，未重建扫描。

## 2026-10-01：Goal 标记 blocked，E-M1 未完成

同一执行阻塞连续三轮复核，全部 Luna 任务已终态，当前无进一步可独立执行的任务。Goal 实际状态已更新为 `blocked`；原目标保持 CLI 接线、基础 UI、授权执行和四仓验收全部完成。固定制品、候选样例、独立提交、补丁/bundle及真实失败日志均保留，定时扫描保持停止。

恢复前置：执行环境允许本地 TCP 监听及已授权的 GitHub 写调用；对应仓库落实 Agent24 托管/收发、基础 UI 与模型/模块/语音契约和实际证据。解除相关阻塞后由用户恢复 Goal，从已归档的第一轮精确命令继续，仍先真实 CI/外部 review 后按 SHA 合并小 PR，生产 T07 保留 T01-E 门槛。

## 2026-10-01 09:10 UTC：第一轮真实工具停在监听权限

固定制品具备后，GPT-6 Luna 在独立工具 worktree `343520df` 用原生产 lock 和归档 CLI/relay 完整调用第一轮工具一次。实际源码/Go/二进制摘要门禁通过，工具打印 BASELINE；随后初始 `127.0.0.1:0` bind 返回 `local-listener-unavailable`，exit 1。一次同 HOME 的最小诊断确认 errno 1 / EPERM。relay 和身份尚未创建，双向消息、离线重试、重启去重及错误断言均未运行，不能记联合验收通过。

原始日志、完整无密码命令和报告已保存到 `build/agent-handoff/20261001/real-a4-go1264/round1-attempt/`。临时身份/HOME 已清理，工具、源码及原用户仓库状态保留。继续运行的具体前置是允许本地监听的环境；发布/PR/Release 仍需可执行 GitHub 写调用的环境。既有实现/候选样例均有独立提交和交付，未再重复单元测试或创建额外扫描；跨仓 UI、模型/模块/语音及 T01-E 仍依原台账由对应仓库交付。

## 2026-10-01 09:01 UTC：固定 Go 真实制品已获得

更正先前“本机 Go 1.26.4 不可用”的结论：默认 Go 是 1.27.1，但缓存中存在可执行的固定工具链 `/Users/jason/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.4.darwin-arm64/bin/go`。根代理实际核对版本后，由 GPT-6 Luna 用已验收工具 `343520df`、干净源码 `a4aa606`、原生产配方生成两个平台的真实 CLI/relay、确定性归档、manifest 和 checksum，共九个文件。Luna 报告两次真实构建字节一致，报告及日志保留；未联网下载或修改生产 lock。

根代理独立对现存完整 bundle 执行生产 lock 校验和本机安全提取，两项 exit 0；四个二进制编译信息实际核对为 Go 1.26.4、CGO_ENABLED=0、trimpath。macOS CLI `f53c29b3…`、macOS relay `a012d86e…` 和 Linux CLI `042f6200…` 均匹配历史生产摘要。Linux relay `a59058571d246d0d8eea1231a7eb1ce5869a0b7cf684e9263376113e2e9b8a49` 为本次受控构建摘要，不冒充历史基线。根代理又在一次性 HOME 实际创建加密身份并列出同一公钥，两次 exit 0，encrypted=true、identity_count=1，原始公共结果及断言已保存，临时身份目录删除。

真实九文件制品和双方日志归档在 `build/agent-handoff/20261001/real-a4-go1264/`；只属生成物，不提交到 Git。manifest 的 local-candidate Release URL 仅是预期地址，尚未发布或可下载。现在已补齐本地固定制品，不能继续以“缺本地二进制/工具链”阻塞；后续使用这些已校验制品在支持监听的环境实际运行第一轮工具。Linux 实机、真实 relay、GitHub Actions、Release/安装器、Agent24 正式 main 的托管/收发、基础 UI、T01-E 及四仓出口仍未通过，E-M1 保持未完成。

## 2026-10-01 08:47 UTC：正式联调前置复核

上一轮完成 C1 源码与根代理独立验收；本轮按正式联调需要核对实际远端状态。Agent24 #628（`b45ba08a`）和 #630（`104b8c44`）已合并；main 已为 `be3652321f604b5242691abd2186212cc193ab75`，实际 `ci.yml` [36830773430](https://github.com/iDoris-ai/Agent24/actions/runs/36830773430) 五项成功。#628 当前 head 的检查已通过，历史 scheduler 失败不能继续列作当前失败；外部评审报告六次复测未复现，但本轮没有独立证明其根因或修复。

[#626](https://github.com/iDoris-ai/Agent24/pull/626) 仍为 `5531abe0`，现有 head 的 REQUEST_CHANGES 未解决；[#627](https://github.com/iDoris-ai/Agent24/pull/627) 仍为 `95013516`、依赖 #626 的草稿。正式 main 暂无这两项托管/收发接线。#630 的本地组合运行证据保留，不能代替实际 main 联调、三类计数正对照或基础 UI。

Hyphae main 仍 `a4aa606`，对应 CI `36738033201` 成功；Release 仍仅 v0.26.0、assets 为空。#101 和 Agent24 #601 仍 OPEN、head 未变且有匹配的外部批准，本会话 GitHub 写操作拒绝条件未改变，不重复尝试相同被拒操作。制品构建/校验/CI及 C1 小提交已本地验收；尚无新远端 PR。下一项真实出口仍需可发布/执行真实 CI 的环境、固定制品下载，以及 Agent24 修复/合入托管与收发链。跨仓实现由用户推动，协作文档与可发送正文已更新；不新增定时器、不启用 T07。

## 2026-10-01：执行恢复候选完成本地验收

GPT-6 Luna 在独立 worktree `/tmp/hyphae-em1-c1` 提交 T01-C1，最终 `427f631f986ec8100554f4118a228b3b1fa2ad2a`，基线 `a4aa606`，工作树干净。仅新增测试、102 个共享样例和说明，生产代码 0 行。根代理发现并修正无条件要求本地模型的问题：无需模型的能力可继续；需要推理时检查所选模型及隐私条件，首次登记、ready 转换和已有 run 恢复共用同一门禁。缺失/非法模型观察不放行。

根代理固定最终 SHA、隔离 HOME、关闭联网下载，独立运行整个 `tests/contracts` 的普通及 race 检查，两项 exit 0；解析原始 JSON 日志确认每项均实际执行全部 102 个样例，零跳过。测试核对候选文档原始摘要及全部 20 条状态边。样例原始 SHA-256 为 `fdd39bc40de1f1b6ed8c9e899faae834410404f785ec72f20ea6e1c3f7a1ac55`。补丁、bundle、PR 草稿和双方日志归档到 `build/agent-handoff/20261001/C1-execution-recovery/`，仍无远端 PR。

这是候选观察规则验收，未实现执行入口、请求账本或实际副作用。跨语言一致性、真实执行器恢复和四仓验收待对应仓库交付；T01-E、T07、T20、T21/T22 的现有门槛保留，E-M1 未完成。下一步交付 Agent24 消费样例，并在制品可下载、真实 CI/监听环境具备后收口 CLI 联调；见 [Agent24 协作说明](../cooperation/Agent24.md)。定时扫描保持停止。

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
