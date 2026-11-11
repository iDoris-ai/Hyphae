# 搜索分支:agent 社交协议

分支 `search` 是"让 agent 代替人做主动社交"这条线的集成分支。这里放方向分析、协议草案和开发计划。

| 文档 | 内容 |
|---|---|
| [analysis-20261110.md](analysis-20261110.md) | 方向分析:relay 适合什么、当前代码的差距、四个方向、已定的决策 |
| [protocol-draft.md](protocol-draft.md) | 协议草案:意图、信任、引荐三组消息的字段和状态机 |
| [plan.md](plan.md) | 开发计划:切片、PR 顺序、与 T01 契约收口的关系 |

## 状态

- 协议是**草案**,wire 格式和 kind 编号没有冻结。冻结要走 [T01 契约收口](../agent/t01-contract-gates.md)。
- 代码在 `internal/social/` 下,目前只有纯逻辑:数据模型、校验、状态机。不接 relay,不改 CLI,不进生产路径。
- 第四个方向(relay 互联、漂流瓶)明确推后,见分析文档。
