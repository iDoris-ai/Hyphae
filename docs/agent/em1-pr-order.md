# E-M1 PR 依赖与合并顺序

更新：2026-09-29。下表是本轮已验收实现的依赖关系；验收通过不代表已进入 main。主代理不自动合并 PR。

后台 PR-daemon 可以并行评审这些 PR，包括 draft。评审范围是各 PR 相对其 base 的差异；draft 在这里表示等待前置合入 main，不等于尚未实现。自动 review 与合并是两个步骤，review 结论不自动解除依赖门槛。

## 前置 PR

同一组中没有依赖关系的 PR 可以分别评审。前置 PR 合入 main 后，后续 PR 先改 base 为 main，确认只剩本任务差异，再合并。

### 合并与复审规则

- 本轮是堆叠 PR。若使用 merge commit，前置提交的祖先关系能保留；若使用 squash/rebase merge，后续分支通常还需重新整理到最新 main，不能只改 base。重新整理时只迁移本任务提交，核对差异和测试后再推送；不要把组合基线变成大 PR。
- 每次 review 记录所审查的 head commit。新提交、冲突解决或基线变化后，旧结论不能直接代表新版本；重新核查有影响的部分。
- PR-daemon 优先检查持久化事务、重复收件与副作用、加密失败、取消和部分成功结果。CLA 通过只说明贡献流程检查通过；本地测试证据和 GitHub CI 结果分别记录。
- 当前仓库开启了合并后自动删除分支。有后续 PR 依赖的分支须在删除前完成后续 PR 的基线迁移；临时 integration 分支待全部依赖迁移完再清理。

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
| [#54](https://github.com/iDoris-ai/Hyphae/pull/54) | 发送前可靠入队 | foundation |
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

`integration/em1-cli-runtime` / `b1cbaaa` 再组合 #60～#63，主代理已通过隔离 HOME 的全量 integration 测试。后续 daemon 历史分页接线与真实积压验收继续分别提交小 PR；依赖已发布的分支时，在 PR 描述固定前置提交，不把其余任务的代码混进差异。

`integration/em1-cli-backfill` / `916fc1f` 再组合 #64/#65。主代理已用 Go 1.25.0 通过全量 integration、vet 和构建；`test.sh` 暴露新身份空历史统计的 NULL 扫描错误，正在独立修复。真实 125 条积压验收仍是单独测试 PR，不把组合分支作为交付 PR。

## 独立事项

- [#39](https://github.com/iDoris-ai/Hyphae/pull/39) 是规划与验收记录；[#41](https://github.com/iDoris-ai/Hyphae/pull/41) 是按仓库命名的协作约定。
- [#44](https://github.com/iDoris-ai/Hyphae/pull/44) 是经测试后创建依赖更新 PR 的工作流。定时任务需合入默认分支才运行；Actions 创建/审批 PR 的仓库权限开关仍等待用户确认，不因 CLA 通过而开启。
- 建议优先处理已有 [#37](https://github.com/iDoris-ai/Hyphae/pull/37) CI PR，再推进业务链。2026-09-29 查询确认其当前 head `21b3fd7` 已获 review，Linux/macOS 测试和 `ci-ok` 通过；本轮未重新审查其全部实现，也未合并。当前 main 的 `required_status_checks` 为 null，新增功能 PR 主要只有 CLA 检查。CI workflow 合入后仍需把 `ci-ok` 配成必需检查才形成合并门槛；此建议不表示已经修改仓库设置。
- retarget 或解决冲突后若代码变化，运行相应测试；全部前置实现进入 main 后，运行一次隔离 HOME 的 `go test -tags integration ./... -count=1`。CLI/UI/四仓验收状态仍以任务台账为准。
