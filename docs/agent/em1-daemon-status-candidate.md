# E-M1：daemon 补收状态接口候选

日期：2026-10-01。状态：根代理 CLI 接线设计，待实现与双方验收；不表示高层契约冻结。依据 Hyphae `a4aa606` 的 `watchInbox`、`watchOneRelayWithHooks`、`relayquery.Walk` 及 Agent24 已合并的 COMM-0 G2。此接口只报告基础通信，不承载请求授权或执行。

## 已有证据与缺口

现有 daemon 以日志报告扫描失败，`--json` 不产生结构化运行消息流。每次扫描返回新增持久化消息数与合并错误；分页层另有 Pages/Fetched/FinishedHint，但调用点目前丢弃这些统计。普通 EOSE、短页或零条新消息都不能证明历史永远完整，更不能证明对端确认或任务执行。

Agent24 COMM-4b 暂时只显示 unknown/incomplete；已合并 COMM-0 G2 请求 JSON-lines 状态。本设计按该接口实现：`daemon --json` 的 stdout 为逐行完整 JSON 状态快照，普通日志转到 stderr。此前提出的原子状态文件不作为本轮接口，避免要求 Agent24 改用未约定的轮询方式。Agent24 尚未实现此流的消费端，仍须更新制品锁并联合验收。

## 接口与字段

`daemon --json` 或既有 JSON 环境变量启用状态流；人工模式保留当前输出。每条 stdout 记录为 `{"ok":true,"data":<完整快照>}`，以单一换行结束；不夹杂启动文字、正文、自动回复或清理日志。状态流只报告基础通信。启动前错误继续使用既有 stderr 错误信封和退出码；运行中的 incomplete/canceled 属于成功报告实际观察，不能把 `ok:true` 当健康。stderr 保留人工诊断，现有日志可能含明文，Agent24 仍须将日志设为 0600。实例局部输出对象/上下文传递日志目标，不更换全局 os.Stdout，不引入共享可变 writer。

```json
{
  "schema": "hyphae-daemon-status/1",
  "generation": "运行时生成的随机标识",
  "pid": 123,
  "event_sequence": 1,
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

- generation 在每次实际 daemon 启动时随机生成；event_sequence 随每条记录递增，scan.sequence 每轮加一。计数均指当前轮，不能累计当作历史总量。消费者绑定该次子进程管道、generation、pid 和公开身份，重启后不沿用旧快照。
- process 为 starting/running/stopping/stopped；未获得身份及成功初始化状态之前的启动失败由既有退出码表达，不伪造运行快照。SIGKILL 无法写 stopped；EOF、进程退出或流解析失败后，消费者清除 running 判断，不能沿用最后一条记录。
- scan 为 unknown/scanning/idle/incomplete/canceled。idle 只表示该轮已返回且各 relay 的当前查询可用，不使用 complete、synced 或 delivered。
- relay state 为 unknown/scanning/available/incomplete/canceled。未访问的 relay 保持 unknown；父取消不能把它们改为 available。relay 用配置内的 index 标识，不写 URL、查询凭据或日志字符串。
- pages/fetched/finished_hint 来自实际 Walk 统计；new_messages 只统计存储返回 newly-stored 的事件；processing_failures 来自实际处理失败。重复事件不增加 new_messages，已有明文不进快照。
- error 只取闭集 query_failed/processing_failed/canceled 或 null；网络错误和消息正文不进入状态。两类失败并存仍是 incomplete，详细诊断保留现有日志机制。
- finished_hint 仅表示 relay 提供显式 NIP-67 finish；即使为 true，也不提升为跨时间、跨 relay 或对端送达证明。
- 时间字段未知时为 null，计数为零；generation、schema 和身份始终存在。计数、序号和字段类型需验证，不能接受未知 schema 为健康。

## 生命周期与失败规则

1. 在 daemon 生命周期锁及身份/relay 校验成功后初始化新 generation，输出 starting/unknown；开始轮询前输出 running。
2. 每轮前重置 scan，scan.sequence 加一并输出 scanning；逐 relay 输出其开始和结束状态，保留前面 relay 的实际计数。
3. 全部当前查询无错误且父上下文未取消，输出 idle；有任一查询或处理失败则 incomplete；父取消则 canceled。分页限额、断连、无 EOSE 和回调失败都不能走成功路径。
4. SIGINT/SIGTERM 或父取消按现有取消机制退出，先输出 stopping，最后输出 stopped。不为状态新增后台计时器或网络探测。
5. 状态输出失败（包括短写、broken pipe）应返回不含秘密的明确 I/O 错误并停止继续扫描；不得继续运行却失去状态流。消费者收到退出、EOF 或读取失败后清除其运行判断；单次写入成功也不证明消费者已处理。
6. 状态是观测记录，不能驱动自动重发、重新执行或授权，也不能当完整数据库或 exactly-once 证明。

## 两项独立小任务

- S1：类型、统计转换与 JSON-lines 编码器。独立模块，尚不接生产 daemon；测试每行完整信封、闭集字段、部分失败/取消、普通 EOSE 与 finish 提示区别、短写/写失败、多个记录和无秘密输出。
- S2：daemon 调用点、局部日志目标及统计传递。在已验收的生命周期锁和 S1 上开发，保留原 watch 函数兼容入口；人工模式保持既有输出，JSON 三种开关一致。每项生产差异单独计算，不能把依赖组合提交为大 PR。

S2 必须在真实本地 relay 验证空历史、125 条补收、重启零新增、单 relay 断线、多配置中一成功一失败、无 EOSE、存储失败、取消与 kill。读者逐行读取管道，每行均须可解析为完整 JSON；跨进程重启 generation 改变，取消时未访问 relay 保持 unknown。真实 relay 监听被环境禁止时保留失败日志，不能用合成状态测试替代运行验收。

Agent24 仍负责进程存活、单次 relay probe 与基础 UI 状态。快照中的 available 不等于持续 connected；三种证据分别显示。接线前回填双方源码、Go 配方、binary hash，以及 Agent24 消费端对闭集字段的实际解析和状态映射测试。
