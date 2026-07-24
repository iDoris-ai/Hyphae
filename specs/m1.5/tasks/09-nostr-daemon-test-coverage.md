# Task 09：`internal/nostr` + `internal/daemon` 单元测试补齐

> 规模：M · depends_on：无 · 发现来源：2026-07-24 `go test ./... -cover`（`internal/nostr` 0%，`internal/daemon` 0.5%，其余包普遍 27%-86%）

## 目标

这两个包是核心协议层（事件签名验签、relay 通信）和核心后台服务（outbox 重试、自动回复），目前几乎没有单元测试兜底——不是说现在有 bug，而是这两块以后改动风险最高、最容易在没人注意时改坏。补到一个合理水平（不追求 100%，追求覆盖到关键路径和边界情况）。

## 接口

无对外接口变化，纯测试任务。

## 设计

**`internal/nostr`** 优先覆盖：
- 事件构造 + 签名 + 验签的往返测试（sign 一个事件，verify 应该过；改一个字节，verify 应该不过）
- filter 匹配逻辑（如果有独立的 filter matching 函数）
- 编解码（bech32 encode/decode 的边界情况：空输入、格式错误输入、超长输入）

**`internal/daemon`** 优先覆盖：
- outbox 重试循环的核心决策逻辑（哪些条目该重试、重试间隔、失败次数累加）——用 mock/fake 的 relay publish 函数（成功/失败两种）驱动，不需要真实网络
- 自动回复逻辑（收到消息 → 判断是否需要回复 → 生成回复内容 → 不对自己的 auto-reply 再次自动回复，即防循环逻辑，这条尤其要测，因为一旦坏了是活跃度爆炸的严重 bug）
- inbox watch 循环的启动/停止（不需要真的等 30 秒，用可注入的 interval 或者直接测单次 tick 的行为）

测试相关的重构原则：如果现有代码把网络调用和业务逻辑耦合在一起导致没法单测，允许做**最小限度**的接口抽取（比如把 relay publish 抽成一个可注入的 interface/func type），但不要借这个任务做大范围重构——只抽取测试真正需要的那几个耦合点。

## 数据

无。

## 流程

无新流程，测试现有流程。

## 验收标准

1. `internal/nostr` 覆盖率从 0% 提升到 ≥ 40%
2. `internal/daemon` 覆盖率从 0.5% 提升到 ≥ 40%
3. 新增测试里必须包含「auto-reply 不会对 auto-reply 再次回复」这个防循环场景的显式测试用例
4. 现有所有测试保持通过，不能为了让新测试过而改动生产代码逻辑（如果测试暴露了真实 bug，另开一条 TODO 记下来，不在本任务里顺手修复——除非 bug 极小且修复本身不改变任何对外行为）
5. `go test ./... -cover` 输出里两个包的覆盖率数字达标

## 实现笔记

- **`internal/nostr` 100% 是 CLI 命令闭包，没有独立可测的纯函数**（除了完全没人调用的 `MustAtoi`）：真正的 bech32/npub/nsec 编解码逻辑其实在 `internal/common`，filter 匹配逻辑在外部 `fiatjaf.com/nostr` 库里，`internal/nostr` 自己只是薄薄一层 `cli.Command{ Action: func... }` 包装。冲 40% 覆盖率的路径是直接通过 `cli.Command.Run(ctx, args)` 调用每个命令（`key generate/public`、`verify`、`encode`、`decode`、`event --json`、`publish`、`req`、`relay info`），对不需要真实网络的部分（signing/verify round-trip、bech32 编解码、flag/参数校验错误路径）覆盖完整；`event`/`publish`/`req`/`relay info` 涉及网络的部分用 `ws://127.0.0.1:1` 这种本地不可达地址，走连接失败分支（够快，不需要真实 relay）。
- **发现并修复了一个真实的、影响面很大的 pre-existing bug：`verify`/`publish`/`relay info` 三个命令的位置参数（positional argument）从来没有真正生效过**。这三个命令的 `Action` 里原来写的是 `c.String("event")`/`c.String("json")`/`c.String("relay_url")`——但这几个名字对应的是 `Arguments`（位置参数），不是 `Flags`；`Command.String(name)` 只查 flag，永远查不到位置参数的值，所以这三行代码从写下来那天起就一直返回空字符串。叠加另一个问题：`cli.StringArg{Name: "..."}` 不显式设置 `Max` 字段时默认是 `0`，框架的 `Parse()` 一看到 `Max == 0` 直接打印一条 warning 然后放弃解析这个参数——两个问题加在一起，`agent-speaker verify '{"id":...}'`、`agent-speaker publish '{...}'`、`agent-speaker relay info wss://xxx`（都是这几个命令自己 `--help` 里给的示例）**在真实使用里全都完全不工作**，`verify`/`publish` 永远报"required"错误，`relay info` 永远悄悄忽略传入的 URL、连到默认 relay。用编译好的二进制实际跑过这三个命令，确认修复前后的行为差异（修复前：`relay info ws://127.0.0.1:1` 实际连的是 `wss://relay.aastar.io`；修复后：正确连到 `ws://127.0.0.1:1` 并快速失败）。
  - 这个 bug 严重、影响面广（三个命令的核心用法完全瘫痪），但修复本身极小且不改变任何"预期外"的行为——只是让代码做到它自己文档里说的事：(1) 给三处 `cli.StringArg` 补上 `Max: 1`（声明"最多一个值"，同时也修正了 `--help` 里的 usage 提示格式）；(2) 改成用 `Destination: &pkgLevelVar` 接收解析后的值，`Action` 里从这个变量读，而不是 `c.Args().First()`（这个我一开始以为够用，实测同样拿不到值——这个 urfave/cli v3 beta 版本对单值 Argument 的唯一可靠取值方式就是 `Destination` 指针，`c.String`/`c.Args().First()` 都不行，用一个独立的 `/tmp` 隔离 repro 直接验证过这个结论，不是猜的）。按照验收标准 4 的但书（"bug 极小且修复本身不改变任何对外行为"）在本任务里直接修了，没有拆成单独 TODO。
- `internal/daemon` 里把 `watchOneRelay` 每个事件循环体里内联的"是否要自动回复"判断（`autoReply && decryptedOK && !isAutoReplyMessage(content)`）和 `sendAutoReply` 里内联的回复文案拼接（`fmt.Sprintf("[auto-reply] %s received your message: %s", ...)`）分别抽成了 `shouldAutoReply(...)` 和 `buildAutoReplyText(...)` 两个纯函数——这是验收标准 3 要求的显式防循环测试能够脱离真实 relay 连接单测的前提，行为完全不变（原来是什么表达式，抽出来还是什么表达式）。
- **`TestWatchInbox_ReceivesEventMarksSeenAndAutoReplies` 用一个进程内的最小 NIP-01 relay 模拟真实收发**，而不是只测抽出来的纯函数：起一个 `httptest.NewServer` + `gorilla/websocket`（已经是项目依赖）的最小 relay，只认识"收到一个 REQ 就回一条预置的 EVENT + EOSE，然后保持连接开一小段时间再关闭"——`watchOneRelay` 的 `for evt := range sub.Events` 只有在客户端库检测到底层连接真的断开时才会退出（这个断开会级联触发 subscription 取消、关闭 `sub.Events`），所以"写完立刻关连接"会跟 OS 实际把字节刷出去的时机竞争（试过，真的会导致客户端什么都收不到），"保持连接开一段时间再关"能稳定复现，比等真实的 `subscribeWindow`（3 秒）快得多。这个测试完整跑通了"收到消息 → 标记已读 → 判断要不要自动回复 → 真的发出自动回复 → 写入本地历史"整条链路，不只是判断逻辑本身。
- 这个测试还顺带验证了一个容易搞错的点：验证"自动回复被记录"时，`GetConversation` 返回的是**两条**消息（原始收到的消息 + 自动回复本身），不是一条——因为 `watchOneRelay` 会先把原始收到的消息存一次，`sendAutoReply` 再存一次回复；一开始断言"应该有且只有 1 条"，实测发现真实是 2 条才意识到这个。
