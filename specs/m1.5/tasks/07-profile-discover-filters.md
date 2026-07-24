# Task 07：Profile discover 过滤条件扩展

> 规模：S · depends_on：**06**（需要 register 三模式 schema 先落地，尤其是 structured 模式里价格/评分字段的最终形态）
> 依据：`docs/milestones/roadmap-v2.md` M1.5 花名册协议一节（沿用原 roadmap Week 5 设计）

## 目标

`profile discover`/`profile search` 目前只能按关键词搜索，加上按能力、价格区间、评分、在线状态过滤，让花名册发现真正可用。

## 接口

```
agent-speaker profile discover \
  --capability seo \
  --price-min 100 --price-max 500 \
  --rating-min 4.5 \
  --online-only
```

所有 flag 都可选、可自由组合（AND 语义——同时满足所有传入的条件）。不传任何 filter flag 时行为等同于现有的 `discover`（列出全部/按现有默认排序）。

## 设计

在 `internal/profile` 现有的查询函数上加一层过滤（内存过滤即可，不需要为了这几个 filter 去改 SQLite 索引结构——花名册数据量级现阶段不需要过度优化）：

```go
type DiscoverFilter struct {
    Capability string
    PriceMin, PriceMax *int
    RatingMin  *float64
    OnlineOnly bool
}

func (m *Manager) Discover(filter DiscoverFilter) ([]types.AgentProfile, error)
```

`Capability` 只对 `structured` 模式的 profile 生效（`simple`/`tagged` 模式没有 capability 字段，直接排除在按 capability 过滤的结果之外，不报错）。`--online-only` 复用现有的 `Availability` 字段判断逻辑（如果已有 presence/heartbeat 机制就复用，没有就按 `Availability == "available"` 判断，不要新建一套在线状态系统）。

## 数据

无新表，复用 `pkg/types.AgentProfile` 现有字段（`Capabilities[].Price`、`Rating`、`Availability`）。

## 流程

CLI flag 解析 → 组装 `DiscoverFilter` → 调用 `Manager.Discover` → 现有的展示逻辑（表格输出）不变，只是数据源多了一层过滤。

## 验收标准

1. 每个 filter 单独用一遍，结果符合预期（写 table-driven 单元测试，覆盖单条件和组合条件）
2. 不传任何 filter，结果和现有 `discover` 行为一致（回归测试）
3. `simple`/`tagged` 模式的 profile 在按 `--capability` 过滤时被正确排除，不报错、不 panic
4. `go test ./...` 全绿
