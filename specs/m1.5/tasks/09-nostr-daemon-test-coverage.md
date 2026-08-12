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
- **发现并修复了一个真实的、影响面很大的 pre-existing bug：`verify`/`publish`/`relay info` 三个命令的位置参数（positional argument）从来没有真正生效过**。这三个命令的 `Action` 里原来写的是 `c.String("event")`/`c.String("json")`/`c.String("relay_url")`——但这几个名字对应的是 `Arguments`（位置参数），不是 `Flags`；`Command.String(name)` 只查 flag，永远查不到位置参数的值，所以这三行代码从写下来那天起就一直返回空字符串。叠加另一个问题：`cli.StringArg{Name: "..."}` 不显式设置 `Max` 字段时默认是 `0`，框架的 `Parse()` 一看到 `Max == 0` 直接打印一条 warning 然后放弃解析这个参数——两个问题加在一起，`hyphae verify '{"id":...}'`、`hyphae publish '{...}'`、`hyphae relay info wss://xxx`（都是这几个命令自己 `--help` 里给的示例）**在真实使用里全都完全不工作**，`verify`/`publish` 永远报"required"错误，`relay info` 永远悄悄忽略传入的 URL、连到默认 relay。用编译好的二进制实际跑过这三个命令，确认修复前后的行为差异（修复前：`relay info ws://127.0.0.1:1` 实际连的是 `wss://relay.aastar.io`；修复后：正确连到 `ws://127.0.0.1:1` 并快速失败）。
  - 这个 bug 严重、影响面广（三个命令的核心用法完全瘫痪），但修复本身极小且不改变任何"预期外"的行为——只是让代码做到它自己文档里说的事：(1) 给三处 `cli.StringArg` 补上 `Max: 1`（声明"最多一个值"，同时也修正了 `--help` 里的 usage 提示格式）；(2) 改成用 `Destination: &pkgLevelVar` 接收解析后的值，`Action` 里从这个变量读，而不是 `c.Args().First()`（这个我一开始以为够用，实测同样拿不到值——这个 urfave/cli v3 beta 版本对单值 Argument 的唯一可靠取值方式就是 `Destination` 指针，`c.String`/`c.Args().First()` 都不行，用一个独立的 `/tmp` 隔离 repro 直接验证过这个结论，不是猜的）。按照验收标准 4 的但书（"bug 极小且修复本身不改变任何对外行为"）在本任务里直接修了，没有拆成单独 TODO。
- `internal/daemon` 里把 `watchOneRelay` 每个事件循环体里内联的"是否要自动回复"判断（`autoReply && decryptedOK && !isAutoReplyMessage(content)`）和 `sendAutoReply` 里内联的回复文案拼接（`fmt.Sprintf("[auto-reply] %s received your message: %s", ...)`）分别抽成了 `shouldAutoReply(...)` 和 `buildAutoReplyText(...)` 两个纯函数——这是验收标准 3 要求的显式防循环测试能够脱离真实 relay 连接单测的前提，行为完全不变（原来是什么表达式，抽出来还是什么表达式）。
- **`TestWatchInbox_ReceivesEventMarksSeenAndAutoReplies` 用一个进程内的最小 NIP-01 relay 模拟真实收发**，而不是只测抽出来的纯函数：起一个 `httptest.NewServer` + `gorilla/websocket`（已经是项目依赖）的最小 relay，只认识"收到一个 REQ 就回一条预置的 EVENT + EOSE，然后保持连接开一小段时间再关闭"——`watchOneRelay` 的 `for evt := range sub.Events` 只有在客户端库检测到底层连接真的断开时才会退出（这个断开会级联触发 subscription 取消、关闭 `sub.Events`），所以"写完立刻关连接"会跟 OS 实际把字节刷出去的时机竞争（试过，真的会导致客户端什么都收不到），"保持连接开一段时间再关"能稳定复现，比等真实的 `subscribeWindow`（3 秒）快得多。这个测试完整跑通了"收到消息 → 标记已读 → 判断要不要自动回复 → 真的发出自动回复 → 写入本地历史"整条链路，不只是判断逻辑本身。
- 这个测试还顺带验证了一个容易搞错的点：验证"自动回复被记录"时，`GetConversation` 返回的是**两条**消息（原始收到的消息 + 自动回复本身），不是一条——因为 `watchOneRelay` 会先把原始收到的消息存一次，`sendAutoReply` 再存一次回复；一开始断言"应该有且只有 1 条"，实测发现真实是 2 条才意识到这个。

### Codex review（Tier 1，第三次尝试才跑通）

前两次 Codex review 请求都卡在 job 队列的 "starting" 阶段几分钟没有任何进展（跟这次的 diff 内容无关，是 Codex 服务/companion 本身当时的问题），各自 cancel 后第三次 `--fresh` 才真正跑起来。跑完之后提了 2 个 Medium，都是真实问题，已修复：

1. **Medium（已修复）——`startFakeRelay` 靠 `time.Sleep(1s)` + 关闭连接来结束 subscription，不够确定性**：原来的想法是"关闭 websocket 连接会让客户端检测到连接断开，级联触发 subscription 取消"，但这依赖 OS/网络层的连接关闭检测时机，Codex 建议改成显式发一条 NIP-01 的 `CLOSED` 消息（`fiatjaf.com/nostr` 库的 `Subscription.handleClosed` 收到 CLOSED 后会同步调 `sub.cancel(...)`，直接触发 `close(sub.Events)`，不依赖任何连接层面的检测延迟）——改完之后发现了一个更深的坑：`Subscription.dispatchEvent`（`fiatjaf.com/nostr` 库内部）投递事件是**异步**的（`go func() { select { case sub.Events <- evt: ...; case <-sub.Context.Done(): ... } }()`），如果 CLOSED 发得太快、把 `sub.Context` 提前 cancel 掉，这个 select 里 "订阅已取消" 分支可能赢过 "事件送达" 分支，导致事件被**静默丢弃**——这不是我瞎猜的，是把 CLOSED 消息紧跟在 EVENT/EOSE 后面发送时，测试立刻就复现了"完全没收到消息"的失败。第一次的应对是加一个 50ms 缓冲再发 CLOSED——这本身也过了 Codex 第一轮，但第二轮 Codex 又指出这只是把竞态窗口缩小，没有消除（调度器繁忙/CI 负载下理论上还是会偶发丢事件），建议改成"等一个真正可观察的信号，确认事件已经处理完，再放行 CLOSED"。**第二轮修复**：`startFakeRelay` 加一个 `closeWhenReady <-chan struct{}` 参数，写完 EVENT/EOSE 之后阻塞在这个 channel 上，不再自己猜时间；测试这边把 `watchInbox` 放到一个 goroutine 里跑（因为它会一直阻塞到 `sub.Events` 关闭，也就是要等 CLOSED 发出去之后才会返回），用 `require.Eventually` 轮询 `messaging.GetInbox(...)`（SQLite 支持安全的并发读写，不像 `seen` 是没锁的裸 map）确认事件已经真正落盘了，再 `close(closeWhenReady)` 放行 CLOSED，再等 `watchInbox` 的 goroutine 真正返回之后才去读 `seen.Has(...)`（这时候已经没有并发写入了，读才是安全的）。这样彻底消除了竞态，不再依赖任何猜测的睡眠时间；用 `-race` 跑了 5 次、正常跑了 20 次全部通过，没有一次失败也没有 race 报警。
2. **Medium（已修复）——三个新增的 `internal/daemon` 测试各自 `t.Setenv("HOME", tmpDir)` + `messaging.InitStorage()`，但 `InitStorage()` 是"第一次调用生效，后面直接返回"的单例（`if store != nil { return nil }`），同一个测试二进制里谁先调用谁说了算，后调用的测试其实在悄悄共享第一个测试的 store/临时目录，不是真的各自独立**：目前这几个测试凑巧没因为这个而失败（因为各自用的都是随机生成的身份/npub 做查询过滤，天然不会撞车），但这是巧合掩盖的设计缺陷，不是真正的隔离。修复：在 `internal/messaging/store.go` 加了一个新导出函数 `ResetStoreForTest()`（复用 `internal/messaging` 自己 `store_test.go` 里原来就有的 `resetStore` 逻辑，顺手把那个也改成调这个新函数、去掉重复代码），让 `internal/daemon` 这种**跨包**的测试也能正确重置单例、拿到真正独立的 store。Codex 第二轮确认这个修复本身没问题，也顺带扫了一遍仓库里其他调用 `InitStorage()` 的地方，确认没有其他测试有同样的漏洞。

### Codex review 第三轮

Codex 因为自己沙盒里 `go test` 跑不了（`mkdir ... operation not permitted`），没能亲自复现"用 `-race`/多次跑没问题"这个结论，只能基于读代码静态分析；但基于代码走查，指出了一个我们两轮都没注意到的真实问题：

- **Medium（已修复）——`require.Eventually` 失败时，后面"放行 fake relay"的代码会被跳过，导致 fake relay 的 handler 永远卡住、`t.Cleanup(srv.Close)` 可能永久 hang**：`require.Eventually` 断言失败会调 `t.FailNow()`（内部走 `runtime.Goexit()`），这会跳过当前函数里这一行之后的所有代码——包括显式调用 `close(closeWhenReady)`/释放 fake relay 的那一步。没有这个释放，fake relay 的 handler 就永远卡在等 `closeWhenReady` 那一行，而 `httptest.Server.Close()`（在 `t.Cleanup` 里）会等所有在途请求结束，所以会跟着一起卡死——测试不是"失败"而是"整个测试进程可能挂起"，比普通的测试失败严重得多。修复：把"释放 fake relay"包成一个 `sync.OnceFunc`（保证重复调用安全），通过 `defer` 在函数级别兜底注册，并且注意 `defer` 的注册顺序（LIFO 展开时必须是"先放行 fake relay，再等 watchInbox 那个 goroutine 结束，最后再 cancel context"，不能反过来，否则会变成"等一个永远不会被放行的东西"这种新的死锁）。修复后专门验证过失败路径：故意让 `Eventually` 的判断条件永远为 false，确认测试能在 ~2 秒（`Eventually` 自己的超时）内正常失败退出，而不是挂起等到 `go test` 的整体超时（之前没加这个安全网时，这一步理论上会依赖 `ctx` 的 5 秒超时兜底 `watchInbox` 的 goroutine，但 fake relay 这边完全没有兜底，`httptest.Server.Close()` 可能因此挂起更久）。
