# Task 02：CLI 全局 `--json` 输出模式

> 规模：M · depends_on：01（先修 ANSI 颜色，避免 JSON 输出里混进转义符）
> 借鉴来源：`docs/buzz-comparison-analysis.md` §7「强烈建议借鉴」第 1 条，参考 `buzz-cli` 的 JSON in/JSON out 设计

## 目标

让 agent-speaker CLI 能被其他程序（Agent24、脚本、未来的 workflow 引擎）当作工具调用，而不只是给人看的。核心诉求：**加了 `--json` 之后，stdout 只有合法 JSON，人类可读的装饰性输出（emoji、颜色、表格）全部关闭；出错时 stderr 是结构化 JSON，退出码语义化。**

## 接口

新增一个**全局** flag（挂在根 `cli.Command` 上，所有子命令继承）：

```
agent-speaker --json <subcommand> ...
```

也支持环境变量兜底：`AGENT_SPEAKER_OUTPUT=json`（flag 优先于环境变量）。

退出码约定（参考 buzz-cli，但按本项目实际错误类型精简）：

| 码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 用户输入错误（参数缺失/格式错误） |
| 2 | 网络/relay 错误 |
| 3 | 鉴权/密钥错误（keystore 密码错、npub/nsec 格式错） |
| 4 | 其他未分类错误 |
| 5 | 写冲突（并发写入本地 SQLite 冲突等） |

## 设计

新增 `internal/common/output.go`，定义一个輸出抽象：

```go
type OutputMode int
const (
    OutputHuman OutputMode = iota
    OutputJSON
)

type Result struct {
    OK      bool        `json:"ok"`
    Data    any         `json:"data,omitempty"`
    Error   string      `json:"error,omitempty"`   // 机器可读错误码，如 "not_found"/"network_error"
    Message string      `json:"message,omitempty"` // 人类可读错误描述
}

func Emit(mode OutputMode, humanFn func(), data any) // 成功路径：human 模式调用 humanFn()（保留现有 fmt.Printf 逻辑），json 模式序列化 data 到 stdout
func EmitError(mode OutputMode, code string, err error) int // 失败路径：human 模式打印到 stderr，json 模式打印结构化 JSON 到 stderr，返回对应退出码
```

**不要求一次性把所有命令都迁移**——这是一个框架 + 先迁移 3-5 个最常用的命令作为示范（建议：`identity list`、`agent msg`、`agent inbox`、`history stats`、`profile discover`），其余命令留在 `docs/TODO.md` 里记一条"待迁移到 --json 框架的命令清单"，后续任务或后续里程碑逐步补齐，不阻塞本任务验收。

## 数据

JSON 输出的 `data` 字段结构跟着各命令已有的返回类型走（比如 `identity list --json` 的 `data` 就是 `[]types.Identity` 序列化后的样子），不需要重新设计 schema——直接复用 `pkg/types` 里已有的 struct，加 `json` tag（很多可能已经有了，检查一下）。

## 流程

1. 根 `cli.Command` 增加 `--json` `cli.BoolFlag`（`Persistent` 或者用 `Before` hook 把值塞进 `context`）
2. 各命令的 `Action` 里，从 `context` 取出 output mode，分流到 `Emit`/`EmitError`
3. 错误路径：命令返回 `error` 时，`main()` 里统一捕获，用 `EmitError` 输出并 `os.Exit(code)`（而不是现在的 `os.Exit(1)` 一刀切）——这一步顺便也是给 buzz-cli 的退出码分级抄作业

## 验收标准

1. `agent-speaker identity list --json | jq .` 能正确解析，不报错
2. `agent-speaker agent msg --json` 在成功/失败两种情况下都输出合法 JSON 到正确的流（成功到 stdout，失败到 stderr）；用一个不存在的 `--from` 触发 user_error（退出码 1），用一个未解锁的加密 keystore 触发 auth_error（退出码 3）——**已修订**：`--relay` 指向不可达地址不会触发硬错误，`agent msg` 的既有设计是"发不出去就存进 outbox 排队重试并返回成功"，不是失败路径，所以拿它来演示 network_error（退出码 2）不合适，改用能自然触发的 user_error/auth_error 场景验证退出码分级机制本身工作正常
3. human 模式（不加 `--json`）的现有行为完全不变——这是硬性要求，不能因为加框架破坏了现有输出格式
4. `go test ./...` 全绿，新增 `internal/common/output_test.go` 覆盖 `Emit`/`EmitError` 的核心分支，包括每个 ErrCode → 退出码的映射

## 实现笔记

- urfave/cli v3.0.0-beta1（本项目锁定的版本）里让 flag 在所有子命令层级都能用的字段叫 **`Local`**（默认 `false` 就是"персистent/全局"），不是常见的 `Persistent: true`——命名和直觉相反，读了 vendored 源码的 `TestPersistentFlag` 测试才搞清楚。根 `--json` flag 不用设任何字段就已经在每个子命令都能用（`agent-speaker --json identity list` 和 `agent-speaker identity list --json` 都行）。
- 迁移 `agent inbox` 时发现一个连带的小问题：原代码在存入本地历史**之前**就把 "🔓 "/"🔒 " 这类展示用 emoji 拼进了 `content` 字符串里再调 `StoreIncomingMessage`，也就是说消息历史里存的是带 emoji 前缀的脏数据（而 `history` 系列命令走的是另一条路径，从来没有这个问题，用的是独立的 `encrypted` 列 + 展示时另加符号）。做 JSON 输出天然要求把"数据"和"展示装饰"分开，所以顺手把这个不一致修掉了——现在 `agent inbox` 存历史时也是干净内容，`encrypted`/`decrypted` 作为独立字段。这个行为变化超出了本任务严格意义上的范围，但是做 JSON 分离的直接、不可避免的结果，没有单独拆任务，在这里如实记录。
- 5 个迁移命令里，只有 `agent msg`/`agent inbox`/`profile discover` 天然有能清楚归类成 user_error/auth_error 的错误路径，已经用 `common.NewExitError` 接上；`identity list`/`history stats` 目前唯一的失败模式是本地存储读取失败，归类成默认的 other_error（退出码 4）是合理的，没有勉强找别的分类。

### Codex review 发现并修复的问题（第一轮）

1. **环境变量兜底只在 main() 的错误处理里生效，5 个命令的成功路径都直接读 `c.Bool("json")`，没读 `AGENT_SPEAKER_OUTPUT`**——这是设计文档写的要求（flag 优先于环境变量），但实现时漏掉了成功路径。修复：在 `internal/common/output.go` 加了 `JSONMode(c *cli.Command) bool` 统一入口，5 个命令 + `main.go` 的 `Before` hook 全部改用这一个函数，不再各自读 flag。补了 4 个针对 `JSONMode` 本身的单元测试（flag 优先、env 兜底、都不设时默认 false、env 设成非 "json" 的值时不生效）。
2. **`agent inbox` 静默丢弃 `GetSecretKey`（以及连带发现的 `GetPublicKey`）的错误**，加密且未解锁的 keystore 会拿到一个零值密钥继续跑，解密失败但命令本身报"成功"退出。修复：`GetPublicKey` 失败一律硬错误（构造 filter 必须要用到）；`GetSecretKey` 只在 `--decrypt=true` 时失败才报 `auth_error`（`--decrypt=false` 时压根不需要这把钥匙，不应该因为 keystore 锁着就拒绝跑）。这个具体的加密 keystore 场景没有做真实的端到端测试（本地环境里现成的身份都没加密码），但改动逻辑和已经验证过的 `agent msg` 里同一模式（`GetSecretKey` 失败 → `ErrCodeAuth`）完全一致，加上 `exitCodeFor`/`EmitError` 的映射本身有单测覆盖。

Codex 同时指出 `specs/m1.5/README.md` 里 Task 1 状态改成 done 混进了这个 PR——这是刻意的（`LOOP_PLAYBOOK.md` 允许把上一个任务的收尾状态更新并进下一个任务的第一个 commit），不是遗漏，PR 描述里会说明。
