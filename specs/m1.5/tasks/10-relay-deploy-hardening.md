# Task 10：relay 部署脚本加固（为切换 `relay-khatru` fork 做准备）

> 规模：S · depends_on：无
> 依据：`docs/protocol-v2.md` §8（fork khatru 决策）、`docs/milestones/roadmap-v2.md` M1.5「relay 自部署」一节

## ⚠️ 范围说明（重要）

**创建 `AuraAIHQ/relay-khatru` 这个 GitHub 仓库本身不属于本任务**，那是一次性的人类基础设施操作（建仓库、配 CI、决定归属组织）。本任务只做 agent-speaker 这边、`scripts/deploy-relay.sh` 相关的准备工作，让"未来某天人类建好了 fork 仓库"这件事发生时，切换成本降到改一个环境变量的程度。如果 loop 执行到这个任务时发现需要真的去创建仓库才能验收，**说明任务范围理解错了，应该暂停并跳过這个验收点，只验收脚本本身的健壮性**。

## 目标

`scripts/deploy-relay.sh` 当前克隆 khatru 官方 `basic` 示例（`RELAY_REPO` 环境变量已经预留了切换点，见脚本注释）。加固这个脚本本身的健壮性和可诊断性，不依赖 fork 仓库是否已存在。

## 接口

```
./scripts/deploy-relay.sh check           # 新增：只检查前置条件（go/git 版本、端口占用），不实际部署
./scripts/deploy-relay.sh local [port]    # 现有
./scripts/deploy-relay.sh tunnel [domain] # 现有
```

## 设计

新增 `check` 子命令（或者作为其他模式执行前的自动前置检查，两种实现方式都行，实现时选一个并在这里补记原因）：
- 检查 `go`/`git` 是否可用及版本是否满足要求（读 `rust-toolchain` 类比——这里应该是读某个 Go 版本要求，如果 khatru 上游有 `go.mod` 声明的版本要求，检查是否满足）
- 检查目标端口（默认 3334）是否已被占用，占用了给出清晰提示而不是让 `go run`/`go build` 失败后甩一个难懂的报错
- 检查 `RELAY_REPO` 环境变量指向的仓库是否可达（`git ls-remote` 探测一下，不可达时给出清晰错误而不是让 `git clone` 失败后甩 git 自己的报错）

## 数据

无。

## 流程

`check` 模式：跑完所有检查项，汇总打印一份"✅/❌ 逐项结果"的报告，任何一项失败都不影响脚本退出码为非 0（方便在 CI 或别的脚本里判断）。

## 验收标准

1. `./scripts/deploy-relay.sh check` 在环境正常时全部 ✅，退出码 0
2. 故意占用 3334 端口后跑 `check`，应该明确报告"端口被占用"而不是含糊的错误
3. 故意把 `RELAY_REPO` 设成一个不存在的地址，`check` 应该报告"仓库不可达"
4. 现有的 `local`/`tunnel` 模式行为不变（回归验证：`bash -n scripts/deploy-relay.sh` 语法检查 + 实际跑一次 `local` 模式确认还能正常拉起 relay）
5. 脚本里 `RELAY_REPO` 切换到 fork 仓库的注释和逻辑保持清晰、可直接改一个环境变量值切换，不需要改脚本逻辑本身
