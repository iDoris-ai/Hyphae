# M1.5 Spec Pack — Relay 自部署 + 花名册 + 短期修复清单

> 独立目录，供 `/loop` 自动化开发循环逐条消费。每个任务是一个独立文件，一个任务 = 一个 PR。
> 上游依据：[`../../docs/protocol-v2.md`](../../docs/protocol-v2.md)（架构决策）、[`../../docs/milestones/roadmap-v2.md`](../../docs/milestones/roadmap-v2.md)（M1.5 章节）、[`../../docs/buzz-comparison-analysis.md`](../../docs/buzz-comparison-analysis.md)（Buzz 借鉴分析）、[`../../docs/TODO.md`](../../docs/TODO.md)（短期任务 + V1 自测发现的 bug）。
> 本目录把上述四份文档里属于 **M1.5 阶段** 的内容合并、拆解成可独立提交 PR 的最小任务单元。
> 创建日期：2026-07-24

## 怎么用

1. `/loop` 每轮从下面的任务表里挑 **第一个 status = ready 且 depends_on 全部 done** 的任务
2. 按 [`LOOP_PLAYBOOK.md`](LOOP_PLAYBOOK.md) 的流程执行（实现 → 自测 → 自我 review → Codex review → 开 PR → 等后台 bot review → merge 或修复重来）
3. 任务完成（PR 已 merge）后，把下面表格里对应行的 status 改成 `done`，提交这个改动（可以和下一个任务的第一个 commit 一起，也可以单独一个小 commit）
4. 循环直到全部 `done`

## 任务表（按建议执行顺序排列，序号即依赖顺序，非强制路由——只要 depends_on 满足，可以调整顺序）

| # | 任务 | 文件 | depends_on | 规模 | status |
|---|---|---|---|---|---|
| 1 | 修复强制 ANSI 颜色输出 | [`tasks/01-fix-forced-ansi-color.md`](tasks/01-fix-forced-ansi-color.md) | — | XS | ready |
| 2 | CLI 全局 `--json` 输出模式 | [`tasks/02-json-output-mode.md`](tasks/02-json-output-mode.md) | 1 | M | ready |
| 3 | 修复 `history conversation` 与 `agent msg` 联系人解析不一致 | [`tasks/03-history-contact-resolution-fix.md`](tasks/03-history-contact-resolution-fix.md) | — | S | ready |
| 4 | 审计哈希链（`audit_log` 表） | [`tasks/04-audit-log-hash-chain.md`](tasks/04-audit-log-hash-chain.md) | — | M | ready |
| 5 | 成员角色模型（Human/Agent） | [`tasks/05-member-role-model.md`](tasks/05-member-role-model.md) | — | M | ready |
| 6 | Profile register 三模式 schema | [`tasks/06-profile-register-mode-schema.md`](tasks/06-profile-register-mode-schema.md) | — | M | ready |
| 7 | Profile discover 过滤条件扩展 | [`tasks/07-profile-discover-filters.md`](tasks/07-profile-discover-filters.md) | 6 | S | blocked |
| 8 | daemon outbox 诊断/清理命令 | [`tasks/08-daemon-outbox-diagnostics.md`](tasks/08-daemon-outbox-diagnostics.md) | — | S | ready |
| 9 | `internal/nostr` + `internal/daemon` 单元测试补齐 | [`tasks/09-nostr-daemon-test-coverage.md`](tasks/09-nostr-daemon-test-coverage.md) | — | M | ready |
| 10 | relay 部署脚本加固（为切换 `relay-khatru` fork 做准备） | [`tasks/10-relay-deploy-hardening.md`](tasks/10-relay-deploy-hardening.md) | — | S | ready |

规模粗略换算：XS ≈ 1 次 loop 迭代 30 分钟内，S ≈ 1-2 小时，M ≈ 半天左右（对 /loop 来说就是"预计几轮自我修正"的量级，不代表真实人类工时）。

## 依赖图（文字版）

```
1 (ansi fix) ──> 2 (--json)
3 (contact resolution fix)          [独立]
4 (audit log)                        [独立]
5 (role model)                       [独立]
6 (register mode) ──> 7 (discover filters)
8 (outbox diagnostics)               [独立]
9 (test coverage)                    [独立]
10 (relay deploy hardening)          [独立]
```

7 个任务完全独立，可以任意顺序做甚至（如果 loop 支持并发分支）并行做；只有 `1→2`、`6→7` 是硬依赖。

## 明确排除在本 spec pack 之外（不是 loop 任务）

- **创建 `AuraAIHQ/relay-khatru` 仓库本身** —— 这是一次性的人类基础设施操作（建 GitHub 仓库、配置 CI/发布），不适合也不应该由自动化循环代劳。任务 10 只覆盖 agent-speaker 这边脚本的准备工作，不创建新仓库。
- **M2 及之后的任务** —— 依赖 `docs/protocol-v2.md` §10 里还没拍板的开放决策（D1-D5，向量算法、邀请券是否上链等），等 M1.5 跑完、这些决策定下来后再产出下一批 spec pack（建议放在 `specs/m2/`）。

## 里程碑完成定义（M1.5 Done）

- 上面 10 个任务全部 `done`（PR 已 merge 到 `main`）
- `go build ./... && go vet ./... && go test ./...` 全绿
- `docs/milestones/roadmap-v2.md` 里 M1.5 的状态从 🔄 改成 ✅，并补一段"实际完成情况 vs 计划"的简短说明（每个任务花了几轮 loop、有没有中途改变设计）
- `docs/TODO.md` 对应勾掉的项目打 `[x]`
