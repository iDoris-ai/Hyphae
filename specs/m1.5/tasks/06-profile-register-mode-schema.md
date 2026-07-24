# Task 06：Profile register 三模式 schema

> 规模：M · depends_on：无
> 依据：`docs/protocol-v2.md` §4.2「注册三种模式」，`docs/milestones/roadmap-v2.md` M1.5 花名册协议一节

## 目标

当前 `profile publish` 只有一种"结构化"发布方式。按 protocol-v2.md 的设计，花名册注册应该支持三档：`simple`（只有名字）、`tagged`（名字+标签）、`structured`（完整 capabilities/价格/评分等，即现状）。这是为 M1.5 花名册发现机制打基础——不是所有 Agent 都需要/愿意暴露完整的结构化信息。

## 接口

```
agent-speaker profile publish --mode simple --name "Alice"
agent-speaker profile publish --mode tagged --name "Alice" --tags dev,go,AI
agent-speaker profile publish --mode structured --name "Alice" --capability "seo:..." --rate "audit:page:50" ...  # 现有行为，mode 默认值
```

不传 `--mode` 时默认 `structured`，**保证现有调用方式完全不受影响**（这是硬性向后兼容要求）。

## 设计

`AgentProfile`（`pkg/types/profile.go`）加一个 `Mode` 字段。序列化到 Kind 0 profile 事件时，`mode` 字段和其余字段一起放进 JSON——不同 mode 下哪些字段必填/可选，按下面的数据 schema 来。`publish` 命令的 flag 校验根据 `--mode` 的值调整（比如 `simple` 模式下 `--capability` 等结构化字段应该被忽略或报错提示，不要静默丢弃用户输入而不提示）。

## 数据

三种 mode 的 JSON schema（对应 `docs/protocol-v2.md` §4.2）：

```json
// simple
{ "name": "alice", "mode": "simple" }

// tagged
{ "name": "alice", "mode": "tagged", "tags": ["dev", "go", "AI"] }

// structured（现状 + mode 字段）
{
  "name": "alice",
  "mode": "structured",
  "capabilities": [...],
  "availability": "available",
  ...
}
```

`pkg/types.AgentProfile` 加：
```go
type ProfileMode string
const (
    ModeSimple     ProfileMode = "simple"
    ModeTagged     ProfileMode = "tagged"
    ModeStructured ProfileMode = "structured"
)
```

老数据（没有 `mode` 字段的 Kind 0 profile，来自尚未升级的客户端或 M1.5 之前发布的 profile）解析时应该视为 `structured`（因为那是之前唯一的模式）。

## 流程

`profile publish` 的 Action：读 `--mode`，根据 mode 决定序列化哪些字段，其余流程（签名、发布到 relay）不变。`profile discover`/`search`（读取侧）需要能正确解析三种 mode 的 profile 事件，不能因为遇到 `simple` 模式的 profile（字段少）就解析失败或崩溃。

## 验收标准

1. 三种 mode 各自 publish 后，用 `profile search`/`discover` 能正确读回来，字段符合对应 schema
2. 不传 `--mode` 的现有调用方式行为完全不变（回归测试，用现有的 profile 相关测试作为基准）
3. 解析一份没有 `mode` 字段的 legacy profile 事件（测试 fixture），应该被当作 `structured` 处理，不报错
4. `simple`/`tagged` 模式下如果用户传了结构化专属的 flag（比如 `--capability`），命令应该给出清晰提示（warning 或 error，二选一，实现时定下来并在这里补记），不能静默丢弃
5. `go test ./...` 全绿
