# Task 06：Profile register 三模式 schema

> 规模：M · depends_on：无
> 依据：`docs/protocol-v2.md` §4.2「注册三种模式」，`docs/milestones/roadmap-v2.md` M1.5 花名册协议一节

## 目标

当前 `profile publish` 只有一种"结构化"发布方式。按 protocol-v2.md 的设计，花名册注册应该支持三档：`simple`（只有名字）、`tagged`（名字+标签）、`structured`（完整 capabilities/价格/评分等，即现状）。这是为 M1.5 花名册发现机制打基础——不是所有 Agent 都需要/愿意暴露完整的结构化信息。

## 接口

```
hyphae profile publish --mode simple --name "Alice"
hyphae profile publish --mode tagged --name "Alice" --tags dev,go,AI
hyphae profile publish --mode structured --name "Alice" --capability "seo:..." --rate "audit:page:50" ...  # 现有行为，mode 默认值
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
- Live smoke test：起本地 `scripts/minirelay.go`，分别用 `--mode simple`/`--mode tagged`/默认 structured 三种方式 publish，再用 `profile discover --json` 读回来，逐条核对 JSON 内容跟 spec 里的三份 schema 示例完全一致（`simple` 只有 `name`+`mode`；`tagged` 多了 `tags`；`structured` 是完整字段）；也验证了 `--mode simple --description ...`、`--mode tagged --capability ...`、`--mode bogus` 三种非法组合都被清晰拒绝、非零退出码。测试产生的 profile 记录已在提交前从本地 `~/.hyphae/messages.db` 清理。
- `pkg/types/profile.go` 加字段导致 `AgentProfile` struct 的列对齐被 gofmt 要求重新计算——顺手跑了 `gofmt -w` 这一个文件（连带修正了这个文件里 pre-existing 的 `Availability` 常量块对齐问题，不是本任务引入的，但既然文件已经在改就一起交给 gofmt 处理了）。

### Codex review（Tier 1）第一轮

Codex 提了 2 个 Medium + 2 个"流程性"意见，处理如下：

1. **Medium（已修复）——`Validate()` 对 simple/tagged 的字段校验漏了 `Availability`/`Version`/"非空但没有 rates 的 RateSheet"**：原来的 `hasStructuredFields` 只查 `Description`/`Capabilities`/`RateSheet.Rates`/`Contact`，一份 `--json-file` 传入的 `{"name":"x","mode":"simple","availability":"busy"}` 能绕过去。spec 里 simple/tagged 的 JSON schema 示例是穷尽性的（`{name, mode}`/`{name, mode, tags}`），不是"举例但不限于"，所以收紧成把 `Availability != ""`、`Version != ""`、`RateSheet != nil`（不再要求 `len(Rates) > 0`）都算进结构化字段里。补了 3 个测试用例覆盖这三种漏网场景。
2. **Medium（不修，记录分歧）——simple/tagged 发布的 JSON 里仍然带 `updated_at`**：`AgentProfile.UpdatedAt` 没有 `omitempty` 且发布前一定会被设成当前时间，所以就算加上 `omitempty` 也不会消失（它从来不是零值）。这个字段是发布时间戳这类元数据，不是"结构化 vs 简单"这个 mode 概念要区分的东西——simple/tagged 的 schema 例子里没写 `updated_at` 更多是"这是 mode 特有字段的示意图"而不是"完整 wire format 定义"（对比：structured 例子本身也用 `...` 省略了很多字段）。保留 `updated_at` 是有意决定，不是遗漏。
3. **"不相关改动"（不修，记录理由）——diff 里带了 `specs/m1.5/README.md`（task 5 状态 + generateGroupID 遗留问题）和 `LOOP_PLAYBOOK.md`（protected branch 说明）的改动**：这是本分支的第一个 commit，按 `LOOP_PLAYBOOK.md` 已经写明的约定（"可以合并进下一个任务的第一个 commit"）故意bundle 进来的，为的是避免每个任务之间都单独开一个纯文档的小 PR（参考任务 4→5 之间产生的 PR #17 那次开销）。不是范围蔓延，是既定流程的一部分。
4. **确认无误——"default `--mode structured` 输出现在多了 `"mode":"structured"` 字段，跟改动前不是 byte-for-byte 一致"**：这是任务本身设计里明确要求的（"structured（现状 + mode 字段）"——spec 原文就是要求给 structured 也加上 mode 字段），不是本任务引入的意外副作用。现有的回归测试（`TestProfileToEvent`/`TestEventToProfile` 等)验证的是具体字段的值而不是"不能出现新字段"，这些测试全部通过；acceptance criteria 2 的"行为完全不变"指的是 CLI 调用方式和已有字段的语义不变，不是要求 JSON 里不能新增字段（那样任务本身就做不成）。

### Codex review（Tier 1）第二轮

确认第一轮修复到位、上面 4 点分歧站得住脚，同时发现 2 个新问题：

5. **Medium（已修复）——`mode=tagged` 没有强制要求至少一个 tag**：一个 `--mode tagged` 但没传 `--tags`（或 `--json-file` 传 `mode:"tagged"` 不带 `tags`）的 profile，跟 `simple` 模式没有任何区别，等于白选了 tagged——违背了 tagged 模式存在的意义。在 CLI 层（`--tags` 清洗后为空就报错，提示改用 `--mode simple`）和 `Validate()` 里（`ModeTagged` 分支追加 `len(p.Tags) == 0` 检查，覆盖 `--json-file` 路径）都补上了这个约束，加了一个测试用例。
6. **Low（不修，记录理由）——JSON-file 手工传入非 nil 但空的 slice（比如 simple 模式塞一个 `"tags": []`）能绕过基于 `len(...) > 0` 的检查**：确实技术上能过 `Validate()`，但 `Tags`/`Capabilities` 都带 `json:"...,omitempty"`，Go 的 `encoding/json` 对 slice 的 `omitempty` 判定就是看 `len == 0`（不看 nil 与否）——所以就算 `Validate()` 放行了这种输入，重新 `Marshal` 发布出去的 JSON 里这些空 slice 字段本来就会被省略，实际发布到 relay 上的内容仍然完全符合 schema。也就是说这个漏洞只存在于"内存里的 `AgentProfile` 结构"层面，不会真正体现在协议输出里，投入产出比不划算，先不修。

### Codex review（Tier 1）第三轮

针对第 5 点修复的一次聚焦复查：CLI 层没问题（`--tags` 先过 `cleanTags`——trim + 去掉空字符串——再判断是否为空），但 `Validate()` 里当时只检查了 `len(p.Tags) == 0`，`--json-file` 传 `"tags": ["", "   "]` 这种"有元素但都是空白"的输入能绕过去，跟 CLI 侧的语义不一致。补了 `hasNonBlankTag`（trim 后至少一个非空才算数）替换掉 `Validate()` 里原来的 `len() == 0` 判断，加了一个专门测试用例（`["", "   "]` 应该被拒绝）。
