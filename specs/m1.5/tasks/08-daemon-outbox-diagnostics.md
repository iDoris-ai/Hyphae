# Task 08：daemon outbox 诊断/清理命令

> 规模：S · depends_on：无 · 发现来源：2026-07-24 真实自测（daemon 启动时报告"9 pending messages... 0 sent, 9 failed"，怀疑是历史遗留脏数据，缺一个排查手段）

## 目标

当前 outbox 只有"daemon 自动重试"这一种交互方式，出问题时用户完全没办法看清楚"到底卡了什么"。加一个只读诊断命令 + 一个显式清理命令。

## 接口

```
agent-speaker storage outbox list                    # 列出所有 outbox 条目，含状态/失败次数/年龄/目标 relay
agent-speaker storage outbox list --failed-only       # 只看失败的
agent-speaker storage outbox clear --failed           # 清掉所有失败次数超过阈值的条目（需要二次确认或 --yes）
agent-speaker storage outbox retry --id <id>          # 手动触发单条重试（不用等 daemon 的 60s 周期）
```

## 设计

读现有 `internal/messaging` 里 outbox 的存储结构（`outbox.json` 或已经在 SQLite 化——先确认现状，这个任务不改变 outbox 的存储位置/格式，只加读取和清理的命令）。`list` 命令直接读现有结构展示；`clear --failed` 需要一个"失败次数超过 N 次才算可清理"的阈值（默认给个合理值比如 5，可以加 `--min-failures` 覆盖）；`retry --id` 复用 daemon 内部现有的单条重试逻辑（如果现在是私有函数，酌情导出或重构成可复用的形式，不要复制粘贴一份重试逻辑出来）。

## 数据

不新增存储结构，复用现有 outbox 数据；如果现有 outbox 条目里没有记录"失败原因"字段，评估要不要顺手加一个（能帮助诊断，但如果改动现有 outbox 结构影响面较大，可以先只加 list/clear，把"记录失败原因"拆成一条新的 TODO，不要在这个 S 规模任务里顺带做大改动）。

## 流程

`list`/`retry` 是只读或幂等操作，风险低。`clear --failed` 是破坏性操作（删除数据），必须有确认步骤——没传 `--yes` 时先打印将要删除的条目数量和一个 `y/N` 交互确认，不能无提示直接删。

## 验收标准

1. 用测试 fixture（手工构造几条不同状态的 outbox 条目）验证 `list`/`list --failed-only` 输出正确
2. `clear --failed` 在没有 `--yes` 时会等待确认，不会静默删除；`--yes` 时正确清除，且不影响未超过失败阈值的条目
3. `retry --id` 正确触发单条重试，不影响其他条目
4. 用这个命令实际跑一遍本地环境里那 9 条历史失败记录，确认能看清楚它们的状态（这一步是本任务的"真实验收"，不只是跑通单元测试）
5. `go test ./...` 全绿
