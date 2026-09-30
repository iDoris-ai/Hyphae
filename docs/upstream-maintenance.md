# 上游跟踪与组件边界

更新：2026-09-29。本文记录上游迁移与协作约束，不调整 E-M 里程碑编号或验收顺序。

## Hyphae 的两种使用方式

Hyphae 是可独立运行的开源通信组件，Agent24 是其消费者之一。

- **连接已有 relay**：使用自己的 Nostr 身份连接本生态公开 relay，也可连接其他兼容 relay；发送、订阅和保存消息。
- **自部署 relay**：在自己的主机运行 relay，将可达地址提供给其他人。客户端可同时连接自建与公开 relay。

客户端连接多个 relay 不代表 relay 会相互转发。双方需要共同可达的 relay，或显式配置后续的转发机制。自部署也不自动解决公网可达性、域名和 TLS。

“邀请加入”有两层含义：分享连接地址；或授予受限制 relay 的读写权限。后者需要经认证的成员授权、撤销与测试。私有 relay 的内容不可默认转发到公开 relay。多人在同一 relay 上交换事件，与端到端加密群组的成员/密钥管理，也需分别实现。

本次迁移提供标准 relay 基础部署；邀请制、私有读写策略、加密群组和 relay 间自动转发仍需后续实现。应用收到通信消息不等于获得 Agent24 执行权限。

## 上游来源

| 用途 | 跟踪目标 | 版本依据 |
|---|---|---|
| Nostr 客户端、签名与协议库 | `fiatjaf.com/nostr` | 根 `go.mod` 的明确版本和 `go.sum` |
| relay 框架 | `fiatjaf.com/nostr/khatru` | 与客户端属于同一个 Go module，联合升级 |
| relay 事件持久化 | `fiatjaf.com/nostr/eventstore/boltdb` | 同 module；与客户端 SQLite 数据目录分开 |
| 参考 CLI | `third_party/nak` | Git submodule 固定 commit；不是运行时必需组件 |

当前固定版本的 NIP-44 `Decrypt` 对 CR/LF-only Base64 输入会在空解码结果上 panic；Hyphae 的 `DecryptMessage` 在调用前拒绝该空 payload。上游应在访问首字节前检查解码长度。这项应用保护不代表 SDK 已修复，也不代表 MAC 比较已确认使用常数时间或应用已设置输入最大长度。

旧 [fiatjaf/khatru](https://github.com/fiatjaf/khatru) 已归档，上游 README 指向新 module。新的源码托管地址可能变化，应以 module 的官方元数据和 Go 校验链为准，不把 GitHub 旧仓库地址写死在构建步骤里。

本轮核验到的更新候选为 `v0.0.0-20260928115942-58e4c715304e`。这是此次升级的版本快照，不是永久“最新”版本。API、持久化兼容和行为测试通过后才作为发布依赖。

部署入口从本仓库的薄适配层构建 relay，使用 `go.mod` 固定的 khatru。L2 功能通过适配层和框架 hook 扩展。后续是否单独建立 `relay-khatru` 仓库不阻塞本次迁移，也不要求复制一套上游核心。

## 自动更新的含义

1. 定期查询上游版本；无 tag 的 Nostr module 也检查新的 pseudo-version。
2. 在独立候选分支更新精确版本，整理依赖并记录差异。
3. 运行单元测试和真实 CLI relay 集成测试，再编译客户端与 relay 并运行核心竞争检测。候选工作流在生成 bundle 前执行 `go test -tags integration ./tests -count=1`；PR 兼容工作流也单独执行此命令。当前集成测试启动本地 relay 与真实 CLI 进程，覆盖加密消息收发、JSON 输出、relay 重启后的事件读取和历史记录。它不代表已完整核验所有旧事件、过滤器或可替换事件语义；这些仍需结合现有单元测试与依赖升级专项验收。
4. 通过后创建或更新升级 PR；失败保留日志和候选信息，由 Luna 修适配、主代理验收。
5. 审阅通过后合并、按发布流程交付。生产构建始终固定版本，可回退到已验证版本。

候选工作流需要生成或刷新候选时会运行候选测试；判定无需更新候选时会提前以 `changed=false` 结束，不会把 no-op 当成一次候选测试验收。

新功能如果落在已经使用的兼容 API 中，可随升级获得；新 API、新 NIP、权限语义或破坏性变化仍需要显式适配。库升级不自动把新能力暴露给终端用户。

自动跟踪不代表修改用户已安装的二进制。Agent24 的打包、版本发现、更新提示和回滚由其仓库接入，见 [协作清单](cooperation/README.md)。本轮不启用未经兼容验证的自动合并或静默客户端安装。

GitHub schedule 只在默认分支生效。更新配置已合入 main，现有每周工作流处于 active。2026-09-30 在 main `e753e6f` 手动触发的 [首次线上运行](https://github.com/iDoris-ai/Hyphae/actions/runs/36702947244) 已成功：解析并核对 `fiatjaf.com/nostr@v0.0.0-20260928115942-58e4c715304e`，依赖无变化，按无变更分支退出，publish job 被跳过。这只验证线上触发和解析；本次未执行候选变更后的测试、上传 bundle 或自动建 PR，不将绿色运行当作发布闭环通过。

仓库 Actions 的 `can_approve_pull_request_reviews=false`，自动创建/审批 PR 的开关仍关闭，本轮保持设置。候选测试通过与 PR 发布是两个出口：有新版本时还须验收真实 bundle、分支推送、PR 创建及最新 head CI，不能因当前 no-op 成功消除该缺口。上游 integration 门禁补充见独立 [#86](https://github.com/iDoris-ai/Hyphae/pull/86)，仍待最新 head 审阅合并。

按当前 [GitHub 工作流触发规则](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow)，默认 GITHUB_TOKEN 创建/更新 PR 的 opened/synchronize/reopened 事件会产生待工作流批准的运行；须单独核实真实 checks 及其 head。workflow_dispatch/repository_dispatch 可以产生运行。更新工作流自身继续先测试精确候选树；它不替代 PR 最新 head 的 CI、review、CLA 和分支保护。

## 提交与验证约定

- feature/fix 使用独立 worktree 和工作分支，及时 commit、push、提 PR；主工作树保留用户修改。
- 原则上每个 PR 的实现、脚本和配置改动控制在 300～500 行以内；测试和设计文档单独计数。
- 超出时优先拆分。生成的依赖锁文件、不可分割的接口迁移等特殊情况可酌情处理，在 PR 中说明原因及实际规模，不为凑行数拆出不可构建的提交。
- 依赖升级、部署适配、自动跟踪和跨仓设计分别评审；堆叠 PR 明确 base 和合并顺序。
- 每个 PR 记录实际运行命令、结果和未覆盖项。已有 CI PR #37 不等于主线已有该 CI；只以当前分支真正运行的检查为依据。
- relay 与加密依赖升级保留旧事件 fixture，实际核验签名、NIP-44、过滤器与可替换事件语义。

目前自动候选门禁运行现有测试集中的旧数据/协议回归用例，以及上述 CLI relay 集成流；这不是对每种历史事件格式或可替换事件行为的完整兼容证明。升级评审仍须确认相关 fixture 覆盖和新增差异。

## 参考

- [khatru 新包与 API](https://pkg.go.dev/fiatjaf.com/nostr/khatru)
- [NIP-01：客户端与 relay 的基础协议](https://github.com/nostr-protocol/nips/blob/master/01.md)
- [GitHub Dependabot 配置](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference)
