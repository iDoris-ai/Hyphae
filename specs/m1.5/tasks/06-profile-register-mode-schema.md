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

## 实现笔记

- **验收标准 4 的决定：error，不是 warning。** `contact add --role bogus`（任务 5）已经用 error 处理过类似的"用户传了不兼容的参数"场景，这里保持一致——`--mode simple`/`--mode tagged` 撞上结构化专属 flag（`--description`/`--capability`/`--availability`/`--rate`/`--currency`，用 `c.IsSet(...)` 判断用户是否真的显式传了，而不是命中了 flag 自带的默认值）时直接返回清晰的 error 并列出冲突的 flag 名和修复建议，不发布任何东西。选 error 而不是 warning 是因为 warning 之后静默丢弃字段 = 用户以为发布了结构化信息，实际上服务端啥都没收到，这比直接拒绝更容易造成误解。
- `pkg/types.ProfileMode` 完全照抄任务 5 `pkg/types.Role` 的模式：字符串枚举 + `Effective()`（零值 `""` 兜底成 `ModeStructured`，对应 display/兼容路径）+ `IsValid()`（零值故意判定为无效，对应"必须显式选择"的写入路径）。这两个方法分工不重叠，`AgentProfile.Validate()` 里两处都用到：先用 `Mode != "" && !Mode.IsValid()` 拒绝无效的显式值，再用 `Mode.Effective()` 决定该按哪种 schema 校验字段。
- **`Validate()` 里也做了 mode-vs-字段校验**（不只是 CLI flag 层），因为 `profile publish --json-file` 这条路径完全绕过 CLI 的 flag 校验，直接把用户提供的 JSON 反序列化成 `AgentProfile` 再调 `Validate()`——如果只在 CLI 层挡，`--json-file` 传一份 `mode: simple` 但塞满 capabilities 的 JSON 会直接绕过去。这是任务 5 Codex review 那次"验证只做在 CLI 层不够"的教训直接应用。
- 三种 mode 的 profile 落地到本地 SQLite 时走的是已有的 `profile_json` 整块 JSON blob 字段（`internal/profile/db.go` 的 `StoreProfile`/`GetProfile`），不需要加新列——`Mode`/`Tags` 字段跟其余字段一样序列化进这个 blob，读回来自然带上，没有单独处理。
- Live smoke test：起本地 `scripts/minirelay.go`，分别用 `--mode simple`/`--mode tagged`/默认 structured 三种方式 publish，再用 `profile discover --json` 读回来，逐条核对 JSON 内容跟 spec 里的三份 schema 示例完全一致（`simple` 只有 `name`+`mode`；`tagged` 多了 `tags`；`structured` 是完整字段）；也验证了 `--mode simple --description ...`、`--mode tagged --capability ...`、`--mode bogus` 三种非法组合都被清晰拒绝、非零退出码。测试产生的 profile 记录已在提交前从本地 `~/.agent-speaker/messages.db` 清理。
- `pkg/types/profile.go` 加字段导致 `AgentProfile` struct 的列对齐被 gofmt 要求重新计算——顺手跑了 `gofmt -w` 这一个文件（连带修正了这个文件里 pre-existing 的 `Availability` 常量块对齐问题，不是本任务引入的，但既然文件已经在改就一起交给 gofmt 处理了）。
