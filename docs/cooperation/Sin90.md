# Sin90 × Hyphae / Agent24

状态：Hyphae 侧选定的真实外部 OS 样例，基础挂载机制已实测通过，完整通信授权与生命周期仍待对应仓库落实；T12/T13 尚未验收。2026-09-30 本地干净审阅基线：`a61ab99443efe91432487000625dfce437660c85`，仓库 `Sin90`。本轮只运行已有测试并记录协作约定；跨仓实现由用户推动。

## 已有实现

`domain-os.yml` 声明 `sin90@0.5.0`，`impl_kind=out_of_process_provider`，启动 `bin/sin90 module`，路由命名空间 `/api/v1/sin90`。`src/main.rs` 的 `Module`/默认入口复用 Agent24 OS SDK 握手；standalone serve 用于开发，不能代替实际 Agent24 挂载验收。

清单请求 events/memory/approval/scheduler/models 内核能力。未声明 `model_access`，缺省 local_only；这只约束模型落点，不表示允许跨节点访问模块。未声明 workspace/fs 权限，不能把本样例描述为文件工作区权限已验收。

`src/http/mod.rs` 已有 `GET /today`，挂载后位于 `/api/v1/sin90/today`。初次单步样例选此只读领域查询，测试只使用合成任务/日程数据；业务写入、AI 分类/提议/摘要和 scheduler 回调不加入初次远端能力范围。

## 对应仓库需要落实

| 负责仓库 | 小任务 | 验收门槛 |
|---|---|---|
| Agent24 | 按 OS package 流程发现、加载和停用固定 Sin90 包 | 外部路径、版本不兼容、manifest 摘要不一致、握手失败和重连有确定结果；不重复挂载 |
| Agent24 + Sin90 | 固定只读 today 能力的名称、版本、参数、结果和权限映射 | 请求绑定签名发送者、目标、能力及版本、有效期；路径和参数不能扩大到任意 HTTP 调用 |
| Agent24 | 授权后登记 run，并调用已挂载模块 | 无授权不调用；停用/退出后返回明确不可用；重复请求核对既有执行状态 |
| Sin90 | 提供可复现的合成数据样例与兼容结果字段 | 结果只含授权允许披露的字段；不读取或返回用户真实规划数据 |
| Hyphae | 搬运请求与关联回执 | 不直接持有模块凭据或调用领域 API；回执重发不启动第二次执行 |

具体能力标识、参数 schema 与结果投影在 T01 冻结；本文不创造一个已可调用的 wire 能力。查询只读也可能披露个人资料，仍须通过 Agent24 授权与数据披露检查。

模块清单能力、模型 local_only 策略、领域读写角色和 Nostr 执行授权各有责任。不得因其中一项通过而跳过其他检查，不得把收到的 `X-A24-*` header 当作远端调用者自带权限。

## 验收证据

2026-09-30 在独立 detached worktree 运行已有 [挂载黑盒测试](https://github.com/iDoris-ai/Sin90/blob/a61ab99443efe91432487000625dfce437660c85/tests/agent24_mount_blackbox.rs#L906)，固定 Sin90 `a61ab99443efe91432487000625dfce437660c85` 与 Agent24 `7009294834b2251beac438f3190aae073742c5dd`，Rust/Cargo 均为 `1.98.1`。设置 `AGENT24_CHECKOUT` 指向隔离 Agent24 树，HOME 和模块数据使用临时目录：

```bash
cargo test --locked --test agent24_mount_blackbox \
  sin90_mounts_under_a_real_agent24_daemon \
  -- --ignored --exact --test-threads=1
```

实际结果：`1 passed; 0 failed; 0 ignored; 4 filtered out`，测试用时 `65.31s`。测试先启动未安装包的真实 Agent24 daemon，确认没有 Sin90；安装原始 manifest 和真实 Sin90 二进制后重启 daemon，确认模块挂载。经真实代理读取 today，检查 must_do/deep_block/inbox/carryover；以合成数据执行代表性 area/task/capture 调用，验证 capture 缺 actor key 为 403、有效 actor key 为 201，并通过实际 WebSocket 收到对应 task id 的 `task.created`。

| 验证对象 | SHA-256 |
|---|---|
| 原始 `domain-os.yml` | `6974521c95413e49890a84720357901314a3433b6c069ff54c837bb232d42576` |
| 实测 Sin90 二进制 | `5215dd43a8b744bffcd0c9389374f203b80e778f8b9e0263814f9579d121bfca` |
| 实测 Agent24 daemon 二进制 | `7ac72023a58b55b761578b089164fa0a4b95e31250c1701a81f72e0146f4c0c9` |
| Sin90 临时验证锁文件 | `4cfc4b8845032858265cbd01ebf2ac1828eda6e252da64dc777523c925e86ac1` |
| Agent24 已提交 `rust/Cargo.lock` | `a681c67ef28303fe68378601690e13ffe3b5eb2a02afc8a7d148b8ca8697f682` |

Sin90 该提交未提供 Cargo.lock，首次 `--locked` 因此未运行测试；随后仅在验证树生成测试用锁文件，归档摘要后锁定依赖执行。该锁文件不代表上游发布锁文件或发布构建可复现性。测试内部构建 Agent24 daemon 的命令未带 `--locked`，其已提交锁文件前后摘要不变；双方 manifest 和源码未改动。测试后清理临时 HOME、子进程及本轮生成的 Sin90 锁文件，验证树与用户原仓均保持干净。

本次只验收已有挂载/代理/事件机制和一个 actor 权限样例。未使用真实用户数据、模型或外部 API；未验证模块停用、退出重连、版本不兼容、manifest 篡改、Nostr 请求授权或 run 恢复，不能据此记 T12/T13 或四仓闭环完成。

### 模块内核能力往返

同一组固定源码、原始 manifest 和隔离 HOME 下，另选已有 `kernel_clients_roundtrip`：

```bash
cargo test --locked --test agent24_mount_blackbox \
  kernel_clients_roundtrip -- --ignored --exact --test-threads=1
```

实际运行 1 项，`1 passed; 0 failed; 0 ignored; 4 filtered out`，`51.62s`。经真实安装、重启和代理，只调用一次测试专用路由：核对 Offer.provides 精确包含 `_a24/memory/private/`、`_a24/approval/`、`_a24/scheduler/`；memory 记忆后召回找到同一条记录；approval 返回 Pending 和 id；scheduler 创建返回 Created、查询可见、删除返回 Deleted、再次查询不可见。Pending 只证明审批登记，不表示审批通过或动作已执行。

此项使用独立 `target/test-hooks-debug/debug/sin90`，SHA-256 为 `9ed0454d15a2ca1ddfa231522ac882fd7fdb4c07639924634198ab46b3adc2fc`；仅该测试构建含调试路由，不能把它作为生产分发包。原生产二进制、Agent24 daemon、manifest 和两份锁文件的摘要保持上表值，测试用 Sin90 锁文件在验证后再次移除。临时 memory/approval/scheduler 记录、HOME 与测试子进程已清理；没有模型或外部 API 调用。

本项补齐已授予能力的正向往返证据，未验证未授予能力的拒绝、卸载重连、模型/远端执行授权或恢复；T12/T13 及四仓验收状态保持不变。

T12/T13 应记录加载、停用、退出后重连、版本不兼容、非法摘要、未授权查询、越权路径、重复请求和结果回传。只读查询可证明真实模块调用及关联，不能单独证明有副作用任务的幂等；T16/T19 仍要用受控副作用计数验证执行恢复。

待回填：Sin90/Agent24 PR 链接、能力声明、授权策略、参数/结果 schema、双方版本和实际验收输出。Hyphae 侧选定样例不代表对应仓库已确认或四仓闭环已通过。
