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
2. `agent-speaker identity create --nickname x --json` 在成功/失败两种情况下都输出合法 JSON 到正确的流（成功到 stdout，失败到 stderr）
3. 故意制造一个网络错误（比如 `--relay ws://localhost:1` 一个没人监听的端口）验证退出码是 2，不是笼统的 1
4. human 模式（不加 `--json`）的现有行为完全不变——这是硬性要求，不能因为加框架破坏了现有输出格式
5. `go test ./...` 全绿，新增 `internal/common/output_test.go` 覆盖 `Emit`/`EmitError` 的核心分支

## 实现笔记

（留空）
