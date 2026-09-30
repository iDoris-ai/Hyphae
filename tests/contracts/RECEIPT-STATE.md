# T01-C 回执状态图与镜像修订候选

本测试夹具参考 [固定候选文档 94ad0e0](https://github.com/iDoris-ai/Hyphae/blob/94ad0e053bdc79e691d2408768d371bc19ec59f7/docs/agent/t01-authorization-recovery-candidate.md) 中的状态图和回执修订段落。文档及本测试都不是已冻结的 wire/schema 规范，也不构成运行授权。

## 状态图

测试分别实现 `directAllowed(from,to)` 和 `reachable(from,to)`。前者只判断图中是否有一条直接边；后者判断图上是否存在路径，并把零边路径视为可达。执行端只能按直接边改变持久化状态，不能把请求方镜像的可达规则当作执行端启动依据。图中的 20 条状态间边为：

| 当前状态 | 允许的下一状态 |
| --- | --- |
| `received` | `approval_pending`, `ready`, `rejected`, `expired` |
| `approval_pending` | `ready`, `rejected`, `expired` |
| `ready` | `run_registered`, `rejected`, `expired` |
| `run_registered` | `running`, `failed`, `expired`, `unknown` |
| `running` | `succeeded`, `failed`, `unknown` |
| `unknown` | `running`, `succeeded`, `failed` |

`[*] -> received` 是初始状态，不计入 20 条边。`succeeded`、`failed`、`rejected`、`expired` 是终态，没有出边。`reachable` 对同一状态返回 true，但这不会增加直接边，也不会让终态接受更高 revision。

## 镜像判定候选

Fixture 输入是假设上游已经验签、解析、通过 schema，并把 receipt correlation 结果与完整性检查明确传入的规范化记录。`correlation_ok` 和 `complete_snapshot` 是输入前置事实，不是在此重新验证作者、事件字段、签名或 schema。`semantic_id` 是调用者已经核对的完整回执语义的不透明标识；它不实现或冻结 JCS、摘要、receipt schema，也不验证标识是否真的覆盖了所有 payload 字段。

本参考判定器按以下次序处理，所有拒绝与诊断结果都返回原 prior：

1. 检查 prior/incoming 状态已知，seq 是非负安全整数；初始 prior 必须是 `received/seq=0`、无 run、空 semantic id。incoming 的 seq 必须至少为 1，semantic id 非空。要求 `run_registered`、`running`、`unknown`、`succeeded`、`failed` 带 run id；`received`、`approval_pending`、`ready`、`rejected` 不带 run id；`expired` 可无 run 或保留已有 run。缺少 correlation/completeness 标志也属于结构无效。
2. `correlation_ok=false` 返回 `correlation_rejected`。
3. `complete_snapshot=false` 返回 `incomplete_snapshot`。因此不完整的低 seq 输入仍报告不完整，不以 stale 掩盖。
4. 若 incoming 提供的非空 run id 与 prior 已绑定 run 不同，返回 `run_mismatch`。低 seq 的旧 pre-run 状态可以不带后来分配的 run id；低 seq 的不同非空 run 仍先诊断为不匹配。
5. 低 seq 返回 `stale`。同 seq 只有 state、run id、semantic id 三者完全相同才返回 `duplicate`；任一不同都返回 `revision_conflict`。投递签名或 event id 不属于此规范化记录，本测试不验证重签投递。
6. 通过前述结构、关联、完整性和 run 检查后，prior 若已是终态，任何更高 seq 都返回 `terminal_locked`，包括更高 seq 重报同状态、同语义的情况。
7. 更高 seq 的非终态 prior：已有 run 不可丢失；随后要求新状态从 prior 沿状态图可达。路径可以跳过丢失的中间快照，也允许非终态同状态的零边可达。此候选把较高 seq 的同状态完整快照当作新镜像；semantic id 的含义和变化是否合法仍由上层 schema/语义投影核对。
8. 通过检查后整体替换为 incoming 的规范化记录。

以上顺序和 run 字段边界是本次参考测试的保守候选细化，供双端评审，不代表原文已冻结该细节。尤其 `run_registered -> failed` 只用于检查状态图；样例不伪造执行器失败事实，也不模拟 run、副作用、持久化或恢复事务。

## 共享夹具与边界

`receipt-state-fixtures.json` 使用 `hyphae-receipt-state-fixtures/1`，包含全部 20 条直接边、5 个区分直接边与路径可达性的图用例，以及 46 个具名镜像场景。样例覆盖 `seq=9007199254740991` 可接受以及更大值拒绝。预期决策和预期 prior/更新后记录均静态保存在 JSON 中；测试运行时不会从判定器推导预期值。

夹具覆盖初始 snapshot、高 seq 跨越中间状态、unknown 恢复、低 seq、不完整与关联前置、同 seq 幂等/冲突、语义和 run 绑定、run 缺失/替换、终态锁定、不可达状态和 safe-integer 边界。整数词法、Nostr 验签、receipt schema、canonicalization/hash、授权、真实执行器结果、run 查询、副作用计数、数据库事务和回执投递都不在本测试范围。通过这些静态样例不能证明生产入口执行相同检查，也不能证明 T01-E/T07 或任何跨仓验收通过。
