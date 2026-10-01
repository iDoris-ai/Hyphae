# E-M1：daemon 补收状态接口候选

日期：2026-10-01。状态：根代理设计候选，尚未实现或冻结。依据 Hyphae `a4aa606` 的 `watchInbox`、`watchOneRelayWithHooks`、`relayquery.Walk` 及 Agent24 已合并的 COMM-0 G2。此接口只报告基础通信，不承载请求授权或执行。

## 已有证据与缺口

现有 daemon 以日志报告扫描失败，`--json` 不产生结构化运行消息流。每次扫描返回新增持久化消息数与合并错误；分页层另有 Pages/Fetched/FinishedHint，但调用点目前丢弃这些统计。普通 EOSE、短页或零条新消息都不能证明历史永远完整，更不能证明对端确认或任务执行。

Agent24 COMM-4b 暂时只显示 unknown/incomplete，COMM-0 请求 JSON-lines 状态。候选先提供固定位置的原子 JSON 快照，便于 CLI/UI 轮询并避免和现有 stdout 日志混合；是否接受此接口替代首轮 JSON-lines，需 Agent24 确认。不得在确认前假设 Agent24 已支持它。

## 接口与字段

候选命令 `daemon --write-status` 显式启用，默认关闭，写入当前 HOME 下 `.hyphae/daemon-status.json`。没有任意路径参数，避免状态写入覆盖 keystore/outbox。文件 0600、目录 0700，写入使用同目录唯一临时文件、fsync、rename；同 HOME 生命周期锁覆盖生产写入。状态不能作为执行授权或导入锁的替代。

```json
{
  "schema": "hyphae-daemon-status/1",
  "generation": "运行时生成的随机标识",
  "pid": 123,
  "identity_npub": "公开接收身份",
  "started_at": "RFC3339Nano UTC",
  "updated_at": "RFC3339Nano UTC",
  "process": "running",
  "scan": {
    "sequence": 1,
    "state": "idle",
    "started_at": "RFC3339Nano UTC",
    "ended_at": "RFC3339Nano UTC",
    "new_messages": 0,
    "relays": [
      {"index": 0, "state": "available", "pages": 1,
       "fetched": 0, "new_messages": 0, "processing_failures": 0,
       "finished_hint": false, "error": null}
    ]
  }
}
```

- generation 在每次实际 daemon 启动时随机生成，sequence 每轮加一；计数均指当前轮，不能累计当作历史总量。消费者绑定受监管进程的 generation、pid 和公开身份，重启后不沿用旧快照。
- process 为 starting/running/stopping/stopped；未获得身份及成功初始化状态之前的启动失败由既有退出码表达，不伪造运行快照。SIGKILL 无法写 stopped，旧文件必须视为待核对。
- scan 为 unknown/scanning/idle/incomplete/canceled。idle 只表示该轮已返回且各 relay 的当前查询可用，不使用 complete、synced 或 delivered。
- relay state 为 unknown/scanning/available/incomplete/canceled。未访问的 relay 保持 unknown；父取消不能把它们改为 available。relay 用配置内的 index 标识，不写 URL、查询凭据或日志字符串。
- pages/fetched/finished_hint 来自实际 Walk 统计；new_messages 只统计存储返回 newly-stored 的事件；processing_failures 来自实际处理失败。重复事件不增加 new_messages，已有明文不进快照。
- error 只取闭集 query_failed/processing_failed/canceled 或 null；网络错误和消息正文不进入状态。两类失败并存仍是 incomplete，详细诊断保留现有日志机制。
- finished_hint 仅表示 relay 提供显式 NIP-67 finish；即使为 true，也不提升为跨时间、跨 relay 或对端送达证明。
- 时间字段未知时为 null，计数为零；generation、schema 和身份始终存在。计数、序号和字段类型需验证，不能接受未知 schema 为健康。

## 生命周期与失败规则

1. 在 daemon 生命周期锁及身份/relay 校验成功后初始化新 generation，写 starting/unknown；开始轮询前写 running。
2. 每轮前重置 scan，sequence 加一并写 scanning；逐 relay 写其开始和结束状态，保留前面 relay 的实际计数。
3. 全部当前查询无错误且父上下文未取消，写 idle；有任一查询或处理失败则 incomplete；父取消则 canceled。分页限额、断连、无 EOSE 和回调失败都不能走成功路径。
4. SIGINT/SIGTERM 或父取消按现有取消机制退出，先写 stopping，最后写 stopped。不为状态新增后台计时器或网络探测。
5. opt-in 状态初始化或后续持久化失败应返回明确 I/O 错误并停止继续扫描；不可吞掉错误让 UI 使用陈旧健康快照。消费者收到退出或读取失败后清除其运行判断，不能仅看文件更新时间。
6. 状态是观测记录，不能驱动自动重发、重新执行或授权，也不能当完整数据库或 exactly-once 证明。

## 两项独立小任务

- S1：类型、转换与原子写入。独立模块，尚不接生产 daemon；测试 round-trip、私有权限、写失败、完整快照替换、临时文件清理，不改全局输出。
- S2：daemon 调用点及统计传递。在已验收的生命周期锁和 S1 上开发，保留原 watch 函数兼容入口；关闭 flag 时保持既有输出。每项生产差异单独计算，不能把依赖组合提交为大 PR。

S2 必须在真实本地 relay 验证空历史、125 条补收、重启零新增、单 relay 断线、多配置中一成功一失败、无 EOSE、存储失败、取消与 kill。读者同时轮询原子快照，任何时刻都须可解析为完整 JSON；跨进程重启 generation 改变。真实 relay 监听被环境禁止时保留失败日志，不能用合成状态测试替代运行验收。

Agent24 仍负责进程存活、单次 relay probe 与基础 UI 状态。快照中的 available 不等于持续 connected；三种证据分别显示。接线前回填双方源码、Go 配方、binary hash，以及 Agent24 对此候选接口的确认。
