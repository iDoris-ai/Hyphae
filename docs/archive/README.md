# 归档文档

这些文档已被更新的决策/实现取代，只作历史记录保留（每个文件顶部都有一行归档说明，写明被什么取代）。**不要按这些文档操作**——请用下面对应的现行文档。

| 归档文件 | 曾经是什么 | 现行替代 |
|---|---|---|
| `2026-04_MILESTONE-2.0-mytask-vision.md` | 一个未被采纳的替代性 V2 方案（绑定 MyTask 链上任务市场生态） | [`../protocol-v2.md`](../protocol-v2.md) |
| `2026-04_DEPLOYMENT-strfry.md` | strfry relay 部署规划 | [`../protocol-v2.md`](../protocol-v2.md) §8（fork khatru）+ `scripts/deploy-relay.sh` |
| `2026-04_LOCAL_RELAY-strfry.md` | strfry 本地部署（无 Docker） | 同上 |
| `2026-04_DEPLOY_V2-strfry-cloudflare.md` | strfry + Cloudflare Tunnel 部署指南 | 同上 |
| `2026-04_REFACTOR_COMPLETE.md` / `_PLAN.md` / `_STEPS.md` | 扁平结构 → 标准 Go 项目布局的一次性重构记录 | 重构已完成，见 `CLAUDE.md` 当前架构描述 |
| `2026-04_TEST_RESULTS.md` | 2026-04-12 对 relay.aastar.io 的一次性人工测试快照 | [`../FULL_TEST_GUIDE.md`](../FULL_TEST_GUIDE.md) + `test_e2e.sh` 等脚本 |
| `2026-04_TEST_REPORT.md` | 旧版 `make test-all`（复制测试到 `build/nak-src/`）的测试报告 | `CLAUDE.md` 的 Testing 一节 |
| `2026-04_git-commit-log.md` | 项目早期某个时间点的 `git log` 快照 | 直接看 `git log` |
| `2026-04_quick-start-nak-strfry.md` | 基于 `nak` CLI + strfry 的早期快速开始 | [`../USER_MANUAL.md`](../USER_MANUAL.md) + 主仓库 `README.md` |
| `2026-04_nostr-cli-tools-research.md` | 选型 `nak` 作为基座的研究依据 | 历史决策记录，`nak` 现已不再被任何代码依赖 |
| `2026-04_ACCEPTANCE_REPORT.md` | 2026-04-12 验收测试报告（PR #10 归档） | 同 `TEST_RESULTS`，见上 |

活跃文档索引见 [`../protocol-v2.md`](../protocol-v2.md) §13「相关文档」和 [`../milestones/roadmap-v2.md`](../milestones/roadmap-v2.md) 「相关文档」。
