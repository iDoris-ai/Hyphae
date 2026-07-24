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

## 实现笔记

- **`pkg/types.AgentProfile` 当时并没有 `Rating` 字段，`Capabilities[].Price` 也不存在**：spec 的"数据"一节写的是"复用现有字段"，但实际上任务 6 只加了 `Mode`/`Tags`，价格信息一直在 `RateSheet.Rates[].Price`（按 service 定价，不是按 capability），`Rating` 则从来没在 schema 里出现过。这是本任务实现时才发现的 spec 假设跟代码现状的落差，不是本任务引入的问题。处理方式：
  - **价格过滤**：直接复用已有的 `RateSheet.Rates[].Price`——一个 profile 只要有任意一条 rate 落在 `[price-min, price-max]` 区间内就算匹配，不新增字段。
  - **评分过滤**：`pkg/types.AgentProfile` 新增 `Rating *float64`（指针是为了区分"没设置评分"和"评分恰好是 0"）。这是 structured-only 字段，同一套 `Validate()` mode-vs-字段校验（任务 6 引入的机制）也把它算进去，加了对应测试。**这个字段目前只是自评分**（profile 自己声明的分数），不是第三方计算的信誉分——没有评价/信誉系统能算出这种分数，那是另一个话题（见 `docs/protocol-v2.md` 里 CityRep 那部分），本任务不涉及。目前也没有给 `profile publish` 加 `--rating` flag——spec 给的接口示例只提到 `discover --rating-min`，没提到 publish 侧怎么设置评分，所以先只加 schema 字段（可以通过 `--json-file` 设置，用于测试/未来铺垫），不额外发明一个跟本任务无关的 publish flag。
- **没有 `Manager` 类型**：spec 的设计示例写的是 `func (m *Manager) Discover(filter DiscoverFilter) (...)`，但这个仓库里从来没有叫 `Manager` 的类型（现有模式是 `internal/profile/db.go` 的 `DB`，方法名如 `ListProfiles`/`SearchProfiles`）。没有照抄一个跟现有代码风格不一致的新类型，而是把 `DiscoverFilter` 实现成一个纯逻辑类型（`internal/profile/filter.go`，`Matches(profile) bool` 方法），在 `profile discover` 的 Action 里直接用它过滤已经从 relay 拿到的 profile 列表——跟 spec"内存过滤即可"的要求一致，只是不需要一个新的容器类型来承载它。
- **只加到 `profile discover`，没碰 `profile search`**：spec 的"接口"一节给的具体例子只有 `profile discover --capability ... --price-min ...`，目标一节提到"discover/search 目前只能按关键词搜索"这句话本身也不准确（`discover` 现在压根没有关键词搜索，那是 `search` 专属的）。为了不臆测超出接口示例范围的需求，这次只把过滤加到 `discover`（对着 relay 现拿的结果过滤），`search`（本地 SQLite 关键词搜索）维持原样不动。
- Live smoke test：起本地 `scripts/minirelay.go`，发布了 3 个不同身份的 profile（`seo-e2e-bot`：structured/seo/price 200/rating 4.8/available；`writing-e2e-bot`：structured/writing/price 50/rating 3.5/busy；`simple-e2e-bot`：simple 模式）。依次验证：不传 filter 能看到全部；`--capability seo` 只留下 seo 的两条；`--capability seo --price-min 100 --price-max 300` 组合条件正确；`--price-min 500` 全部排除掉；`--rating-min 4.5` 正确排除评分较低和无评分的记录；`--online-only` 正确排除 busy 的记录；全程 `simple-e2e-bot` 在按 capability/price/rating 过滤时都被正确排除、没有报错或 panic。测试产生的 profile 记录已在提交前从本地 `~/.agent-speaker/messages.db` 清理。

### Codex review（Tier 1）第一轮

Codex 提了 2 个真实问题，都已修复：

1. **（已修复）价格比较把 `float64` 截断成 `int`**：`matchesPriceRange` 原来写的是 `price := int(rate.Price)` 再跟 `int` 边界比较——一条 `price: 100.99` 的 rate 会被截断成 `100`，错误地满足 `--price-max 100`（正确行为应该是被排除，因为 100.99 > 100）。改成直接用 `rate.Price`（`float64`）跟 `float64(*min)`/`float64(*max)` 比较，不再有精度损失。加了 `TestDiscoverFilterMatches_PriceIsNotTruncatedToInt` 覆盖这个边界。
2. **（已修复）"simple/tagged 模式排除结构化过滤"原来只是"字段恰好是空的"这个副作用，没有真正检查 `Mode`**：relay 上的数据不是我们自己 `Validate()` 过的可信数据——`EventToProfile`/`AgentProfileFromJSON` 反序列化时并不会调用 `Validate()`，所以一个声明 `mode:"simple"` 但仍然带着 `capabilities`/`rate_sheet`/`rating` 字段的畸形（或恶意构造的）relay 事件，原来的实现会因为字段"恰好存在"而把它当成结构化 profile 匹配上。改成在 `Matches()` 里显式检查：只要请求了 Capability/Price/Rating 中任意一个过滤条件，就先判断 `profile.Mode.Effective() == types.ModeStructured`，不是就直接排除，不再依赖字段是否为空这个隐含假设。`OnlineOnly` 不属于这类"仅 structured 生效"的过滤（spec 里没有这么说，`Availability` 字段本身也不是 structured 专属），所以没有加这层 mode 检查。加了 `TestDiscoverFilterMatches_MalformedProfileGatedByMode` 覆盖这个场景。
