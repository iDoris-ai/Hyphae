# Task 02：CLI 全局 `--json` 输出模式

> 规模：M · depends_on：01（先修 ANSI 颜色，避免 JSON 输出里混进转义符）
> 借鉴来源：`docs/buzz-comparison-analysis.md` §7「强烈建议借鉴」第 1 条，参考 `buzz-cli` 的 JSON in/JSON out 设计

## 目标

让 hyphae CLI 能被其他程序（Agent24、脚本、未来的 workflow 引擎）当作工具调用，而不只是给人看的。核心诉求：**加了 `--json` 之后，stdout 只有合法 JSON，人类可读的装饰性输出（emoji、颜色、表格）全部关闭；出错时 stderr 是结构化 JSON，退出码语义化。**

## 接口

新增一个**全局** flag（挂在根 `cli.Command` 上，所有子命令继承）：

```
hyphae --json <subcommand> ...
```

也支持环境变量兜底：`HYPHAE_OUTPUT=json`（flag 优先于环境变量）。

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

1. `hyphae identity list --json | jq .` 能正确解析，不报错
2. `hyphae agent msg --json` 在成功/失败两种情况下都输出合法 JSON 到正确的流（成功到 stdout，失败到 stderr）；用一个不存在的 `--from` 触发 user_error（退出码 1），用一个未解锁的加密 keystore 触发 auth_error（退出码 3）——**已修订**：`--relay` 指向不可达地址不会触发硬错误，`agent msg` 的既有设计是"发不出去就存进 outbox 排队重试并返回成功"，不是失败路径，所以拿它来演示 network_error（退出码 2）不合适，改用能自然触发的 user_error/auth_error 场景验证退出码分级机制本身工作正常
3. human 模式（不加 `--json`）的现有行为完全不变——这是硬性要求，不能因为加框架破坏了现有输出格式
4. `go test ./...` 全绿，新增 `internal/common/output_test.go` 覆盖 `Emit`/`EmitError` 的核心分支，包括每个 ErrCode → 退出码的映射

## 实现笔记

- urfave/cli v3.0.0-beta1（本项目锁定的版本）里让 flag 在所有子命令层级都能用的字段叫 **`Local`**（默认 `false` 就是"персистent/全局"），不是常见的 `Persistent: true`——命名和直觉相反，读了 vendored 源码的 `TestPersistentFlag` 测试才搞清楚。根 `--json` flag 不用设任何字段就已经在每个子命令都能用（`hyphae --json identity list` 和 `hyphae identity list --json` 都行）。
- 迁移 `agent inbox` 时发现一个连带的小问题：原代码在存入本地历史**之前**就把 "🔓 "/"🔒 " 这类展示用 emoji 拼进了 `content` 字符串里再调 `StoreIncomingMessage`，也就是说消息历史里存的是带 emoji 前缀的脏数据（而 `history` 系列命令走的是另一条路径，从来没有这个问题，用的是独立的 `encrypted` 列 + 展示时另加符号）。做 JSON 输出天然要求把"数据"和"展示装饰"分开，所以顺手把这个不一致修掉了——现在 `agent inbox` 存历史时也是干净内容，`encrypted`/`decrypted` 作为独立字段。这个行为变化超出了本任务严格意义上的范围，但是做 JSON 分离的直接、不可避免的结果，没有单独拆任务，在这里如实记录。
- 5 个迁移命令里，只有 `agent msg`/`agent inbox`/`profile discover` 天然有能清楚归类成 user_error/auth_error 的错误路径，已经用 `common.NewExitError` 接上；`identity list`/`history stats` 目前唯一的失败模式是本地存储读取失败，归类成默认的 other_error（退出码 4）是合理的，没有勉强找别的分类。

### Codex review 发现并修复的问题（第一轮）

1. **环境变量兜底只在 main() 的错误处理里生效，5 个命令的成功路径都直接读 `c.Bool("json")`，没读 `HYPHAE_OUTPUT`**——这是设计文档写的要求（flag 优先于环境变量），但实现时漏掉了成功路径。修复：在 `internal/common/output.go` 加了 `JSONMode(c *cli.Command) bool` 统一入口，5 个命令 + `main.go` 的 `Before` hook 全部改用这一个函数，不再各自读 flag。补了 4 个针对 `JSONMode` 本身的单元测试（flag 优先、env 兜底、都不设时默认 false、env 设成非 "json" 的值时不生效）。
2. **`agent inbox` 静默丢弃 `GetSecretKey`（以及连带发现的 `GetPublicKey`）的错误**，加密且未解锁的 keystore 会拿到一个零值密钥继续跑，解密失败但命令本身报"成功"退出。修复：`GetPublicKey` 失败一律硬错误（构造 filter 必须要用到）；`GetSecretKey` 只在 `--decrypt=true` 时失败才报 `auth_error`（`--decrypt=false` 时压根不需要这把钥匙，不应该因为 keystore 锁着就拒绝跑）。这个具体的加密 keystore 场景没有做真实的端到端测试（本地环境里现成的身份都没加密码），但改动逻辑和已经验证过的 `agent msg` 里同一模式（`GetSecretKey` 失败 → `ErrCodeAuth`）完全一致，加上 `exitCodeFor`/`EmitError` 的映射本身有单测覆盖。

Codex 同时指出 `specs/m1.5/README.md` 里 Task 1 状态改成 done 混进了这个 PR——这是刻意的（`LOOP_PLAYBOOK.md` 允许把上一个任务的收尾状态更新并进下一个任务的第一个 commit），不是遗漏，PR 描述里会说明。

### 后台 review bot（第一轮 PR review）发现并修复的问题

比自己那两轮 review 更狠地挑出了一个真正的架构问题——**REQUEST_CHANGES**：

1. **【Confirmed High】JSON 错误路径实际上不可靠，这直接违背了这个 PR 的核心目的**（脚本最需要的是"能可靠判断失败"，而不是"成功时输出好看"）。根因：`main.go` 里 `jsonMode` 是一个包级全局变量，只在根命令的 `Before` hook 里赋值一次——但 `Before` 触发的时机在**子命令自己的 flag 还没解析完**之前，所以如果 `--json` 出现在子命令名后面（`hyphae agent msg --json`），根命令这时候根本还没"看到"这个 flag，全局变量被定格成 `false`。等到 `main()` 最外层捕获错误、调用 `common.EmitError(jsonMode, err)` 时，用的还是这个过时的快照——不管命令真正跑起来后 `c.Bool("json")` 是不是 `true`。live 复现：`hyphae agent msg --json`（缺必填 flag）打印人类可读的 `Error: ...`，而 `hyphae --json agent msg`（flag 放最前面）才输出 JSON——这正是脚本最依赖的"参数错误"这类场景，偏偏最容易踩坑。同时，urfave/cli 框架自己抛出的"必填 flag 缺失"错误是一个包内未导出类型，从不是 `common.ExitError`，`errors.As` 匹配不上，兜底成 `other_error`（退出码 4）而不是更准确的 `user_error`（退出码 1）。
   **修复**：删掉 `Before` hook 和包级全局变量，改成 `common.JSONModeFromArgs(args []string) bool`——直接扫描原始 `os.Args`（外加 `HYPHAE_OUTPUT` 环境变量兜底），完全不依赖 cli 库任何时序假设：不管 `--json` 出现在命令行哪个位置、错误发生在哪一层，只要 argv 里有它就认。`main()` 的错误处理直接用 `common.JSONModeFromArgs(os.Args)`。另外加了 `classifyUnwrappedError`，对 urfave/cli 已知的 `Required flag(s) ... not set` 错误消息做前缀匹配（因为那个类型未导出、没法 `errors.As`），归类成 `user_error`——明确写成消息前缀启发式而不是类型判断，库版本升级如果改了措辞会失效，但目前锁定的 `v3.0.0-beta1` 版本消息格式是确定的。
2. **【Confirmed Medium】用户传入的、长得像密钥的输入会原样回显进错误消息**，比如 `--to` 传错传成一个 nsec，`"'<那段nsec>' is not a known nickname or valid npub"` 会把这个 nsec 原封不动地混进 `--json` 的 `message` 字段（还有 human 模式的 `Error:` 行）。这个场景本身要求用户/脚本出错在先（把私钥当 npub 传），风险不算特别高，但这个工具明确是给脚本调用的，stderr 常常会被脚本落盘或转发到别处保存，值得防御性处理。**修复**：加了 `redactSecrets`，用正则匹配 bech32 `nsec1...` 的字符集（bech32 字母表排除了 `1/b/i/o`，可以精确匹配，不会像"泛化替换 64 位十六进制"那样把 event ID/pubkey 之类的非密钥值也误伤），在 `EmitError` 里对最终要打印的 message 统一做一遍替换（human/json 两种模式都过一遍，不只是 JSON）。

以上两处修复都补了对应的单元测试（`JSONModeFromArgs`：flag 任意位置/`--json=true|false`/env 兜底/都没有时默认 false；`classifyUnwrappedError`：required-flag 消息→user_error、未知消息→other_error；`redactSecrets`：nsec 被替换、无密钥的普通消息原样不变），并且逐条 live 复现验证过（`--json` 放子命令后面现在能正确输出 JSON 且退出码是 1；redaction 真的在输出里生效）。

**顺手核实但明确不在本 PR 修的问题**：review 时用一个全新（零消息）身份跑 `history stats` 复现了一个预先存在、和本 PR 无关的真实 bug——`internal/storage/message.go` 的 SQL 扫描把 NULL 列转 `int` 会崩溃（`sql: Scan error ... converting NULL to int is unsupported`），human/json 两种模式都会崩。已经独立复现确认（不是盲信 review 意见），记进了 `docs/TODO.md`，不在这个 PR 里顺带修。
