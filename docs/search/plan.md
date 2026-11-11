# 开发计划

## 约束

- [T01 契约收口](../agent/t01-contract-gates.md)通过之前,新的高层行为不进生产路径。所以现在只写纯逻辑包:不分配 kind,不接 relay,不改 CLI,不改 `cmd/hyphae/main.go`。
- 每个 PR 的代码不超过 300 行(测试和文档不计),提前切片。
- `search` 是集成分支。每个切片在自己的分支和 worktree 里做,合回 `search`。

## 第一批切片(并行)

| 切片 | 分支 | 包 | 范围 |
|---|---|---|---|
| S0 | `search` | `internal/social` | 共用基础:`Envelope`、严格 JSON 解析、十六进制校验 |
| S1 | `search-intent` | `internal/social/intent` | 六种消息的解析和校验;会话状态机 |
| S2 | `search-trust` | `internal/social/trust` | 邀请链;评价的有效性;信誉分 |
| S3 | `search-referral` | `internal/social/referral` | 引荐状态机;从回执推导积分账本和欠款上限 |

每个切片的通过条件:

- `go build ./...`、`go vet ./...`、`gofmt -l` 干净,`go test ./internal/social/...` 通过。
- 草案里每条规则至少有一个正例和一个反例测试。
- 不引入新的第三方依赖。

## 之后的切片(顺序)

1. S2b:背书的校验和选择性披露。
2. 共享样例(fixtures):语言无关的正反例,给 Agent24 的验证器用。
3. T01-A 收口后:分配 kind,接入 relay 收发和本地存储。
4. CLI 命令和 `--json` 契约。
5. 和 Agent24 联调约饭场景:意图 → 握手 → 匹配 → 建群。

## 第一个可演示的闭环

约饭:A 的 agent 发意图,B 的 agent 响应,双方分轮披露,都同意后进同一个群。先在单个 relay 上跑,目标是二三十个真实用户。
