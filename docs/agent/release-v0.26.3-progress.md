# v0.26.3 阶段发布 + Agent24 联调进度

规划来源：[Hyphae 现状与阶段发布计划](https://claude.ai/artifact/FxkWCxHbFXqgjqski5oxLE)（2026-10-24）。本文件是执行进度的权威记录：每完成一个子任务就更新状态、证据和时间。会话中断后从第一个非 DONE 的任务继续。

状态取值：TODO / DOING / DONE / BLOCKED / SKIPPED。时间为清迈时间（UTC+7）。

## 已定的默认决策

| 事项 | 决定 | 来源 |
|---|---|---|
| 版本号 | `144555ca` 发 `v0.26.3`；`v0.26.2` tag 保持不动，不补 Release | 报告推荐，用户 2026-10-24 说"开始吧" |
| Agent24 lock 升级 PR | 由本会话开，代码改动交给 Sonnet 子代理写 | 同上 |
| 上游 NIP-44 两项修复 | 不在本计划内 | 同上 |
| 合并规则 | clestons 批准精确 head、必需 CI 绿、MERGEABLE 后 `gh pr merge --merge`；不 self-approve，不 `--admin` | handoff-20261024 |

## 任务

| ID | 步骤 | 子任务 | 负责 | 状态 | 证据 / 备注 |
|---|---|---|---|---|---|
| T0 | 准备 | 建立本进度文档 | Hyphae | DONE | 本文件 |
| T1.1 | 1 验证 | 在 `144555ca` 上重跑三条 M1 exit 测试 | Hyphae | DONE | 2026-10-24：`go test -tags integration ./tests -run '…三条…' -count=1` 三条 PASS（3.63s / 3.33s / 4.29s）。代码与 `144555ca` 相同，当前分支只多文档提交。Go 1.27.2 |
| T1.2 | 1 验证 | `go vet`、`go test ./... -race`、`go test -tags integration ./tests` | Hyphae | DONE | 2026-10-24：`go vet ./...` 无输出；`go test ./... -race -count=1` 全部 ok；integration 全套 ok（98s）；M1/Offline 加 `-race` ok |
| T2.1 | 2 发布 | 准备 Go 1.26.4 工具链（本机缓存已无） | Hyphae | TODO | |
| T2.2 | 2 发布 | 干净 checkout `144555ca`，用 `scripts/build_release_artifacts.py` 构建 bundle，两次构建 hash 一致 | Hyphae | TODO | |
| T2.3 | 2 发布 | `scripts/verify_artifact_bundle.py` 校验 bundle | Hyphae | TODO | |
| T2.4 | 2 发布 | 打 tag `v0.26.3` 指向 `144555ca` 并推送 | Hyphae | TODO | |
| T2.5 | 2 发布 | 创建 GitHub Release，上传 assets，写 release notes | Hyphae | TODO | |
| T3.1 | 3 joint-ci | 手动触发 joint-ci candidate 模式对准 `v0.26.3` | Hyphae | TODO | |
| T4.1 | 4 lock 升级 | 读 Agent24 LOCK-UPGRADE-20261005 先例，确定要改的文件 | Agent24 | TODO | |
| T4.2 | 4 lock 升级 | 新分支改 `hyphae.lock.json`（`source_sha`、两平台 hash）和 `binary.rs` 断言 | Agent24 | TODO | Sonnet 写 |
| T4.3 | 4 lock 升级 | 本地 `cargo test -p agent24-comm` 和 lock verify | Agent24 | TODO | |
| T4.4 | 4 lock 升级 | push、开 PR、等 CI 和 clestons 评审、合并 | Agent24 | TODO | |
| T5.1 | 5 联调 | `joint_round1` | Agent24 | TODO | |
| T5.2 | 5 联调 | `send_history_e2e` | Agent24 | TODO | |
| T5.3 | 5 联调 | `daemon_joint` | Agent24 | TODO | |
| T6.1 | 6 混合版本 | 旧 `671c584` 二进制与 `v0.26.3` 双向收发 | 两边 | TODO | |
| T7.1 | 7 基线 | Agent24 合入后更新 `.github/joint-ci-baseline.json`，开 PR | Hyphae | TODO | |

## 日志

- 2026-10-24：建立文档。`v0.26.2` tag 在 `9c1b9f5a`，无 Release；Agent24 main `f16e65a9` 锁 Hyphae `671c584`。
