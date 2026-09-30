# E-M1 PR 依赖与合并顺序

更新：2026-09-30。下表是本轮已验收实现的依赖关系；验收通过不代表已进入 main。用户已授权主代理按依赖顺序合并；每项仍需有效批准、main 基线和通过的 CI。

后台 PR-daemon 可以并行评审这些 PR，包括 draft。评审范围是各 PR 相对其 base 的差异；draft 在这里表示等待前置合入 main，不等于尚未实现。自动 review 与合并是两个步骤，review 结论不自动解除依赖门槛。

## 本轮合并进度

2026-09-30，主线基线为 `e433a6754687857c146e289842c8c5f86b4b235d`。#37～#48、#50～#52、#68～#79 已合并；主线 Linux/macOS CI 通过。最低 Go 版本为 1.26，CI 按 go.mod 选择工具链。上述编号范围包含规划、CI 和维护 PR，不表示整个 E-M1 已通过。

| 下一项 | 当前门槛 | 后续动作 |
|---|---|---|
| #49：重试历史明文 | 已转 main，head `09b869b`，CI 通过，旧批准因 base 变化失效 | 获得当前基线批准后合并 |
| #53～#55：待发管理、可靠发送、daemon 收件登记 | 仍以 foundation 为 base；等 #49 合并 | 逐项转 main，确认仅本任务差异，标记 ready，运行 CI 并取得有效批准 |
| #56～#67 | 等各层前置合并 | 按下表逐层迁移，不能一次合并 integration 分支 |
| #80：20 分钟 PR monitor | 独立维护项，已转 main，head `7815a15` | Python 单元测试已接入双平台 CI；等待最新检查和批准 |

状态是本次文档提交时的快照。后台 monitor 每 20 分钟读取 GitHub 实际状态，在同一 Codex 会话跟进；本会话有排队任务时不重复入队。它不替代 PR-daemon 的 review，不绕过审批或 CI。脚本在 #80 中交付，本机配置和线程 ID 不进入仓库。

### 查询并发修复与剩余范围

#79 修复了 `relayquery.Fetch` 使用上游异步订阅时，断线触发事件发送与通道关闭的竞争。主代理在旧实现上复现 race；新实现按 WebSocket 线序处理事件和真实 EOSE，保留验签、过滤、NIP-67 提示、帧大小限制与查询 deadline。相关 race 重复测试、真实 CLI/relay 全量 integration 及双平台 CI 通过，详见 [验收记录](em1-cli-acceptance.md)。

#58 和 #65 合并后，inbox 与 daemon 分页将使用该查询层。当前 main 仍保留这些旧调用点；完整功能链中另外还有底层 `req/query` 和 `profile discover` 的直接 SDK 订阅，须分别迁移和验收。不能把 #79 当成 SDK 全局修复。基础 CLI/UI 接线和高层行为契约的设计可继续，但主线完整功能验收仍等 #49、#53～#67 收口。

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

**以下 PR 保持 draft，不合入它们当前指向的临时 integration 分支。** 当前基线的全部前置 PR 进入 main 后，再逐项 retarget 到 main，检查差异与测试，再标记 ready。这样保留每个小 PR 的独立审阅记录。

| PR | 内容 | 当前评审基线 |
|---|---|---|
| [#53](https://github.com/iDoris-ai/Hyphae/pull/53) | outbox list/clear JSON | foundation |
| [#54](https://github.com/iDoris-ai/Hyphae/pull/54) | 发送前可靠入队；`3f33164` 恢复必填参数，新 head 待复审 | foundation |
| [#55](https://github.com/iDoris-ai/Hyphae/pull/55) | daemon 持久化收件接线 | foundation |
| [#56](https://github.com/iDoris-ai/Hyphae/pull/56) | outbox retry JSON | reliability |
| [#57](https://github.com/iDoris-ai/Hyphae/pull/57) | 可靠自动回复 | reliability |
| [#58](https://github.com/iDoris-ai/Hyphae/pull/58) | inbox 查询与部分错误结果 | reliability |
| [#59](https://github.com/iDoris-ai/Hyphae/pull/59) | daemon 参数校验与退出取消 | #57 分支；#57 合入后改回 main |
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
- [#44](https://github.com/iDoris-ai/Hyphae/pull/44) 是经测试后创建依赖更新 PR 的工作流，已合入默认分支。Actions 创建/审批 PR 的仓库权限开关仍等待用户确认，不因 CLA 通过或工作流合入而开启；尚未验收自动创建更新 PR 的线上闭环。
- [#37](https://github.com/iDoris-ai/Hyphae/pull/37) CI 已合入 main，Linux/macOS 检查已生效。当前 main 的 `required_status_checks` 仍为 null，本轮由主代理逐项核对 CI，不绕过 review；尚未修改仓库保护设置。
- retarget 或解决冲突后若代码变化，运行相应测试；全部前置实现进入 main 后，运行一次隔离 HOME 的 `go test -tags integration ./... -count=1`。CLI/UI/四仓验收状态仍以任务台账为准。
