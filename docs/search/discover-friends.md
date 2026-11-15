# 发现同一 relay 上的人，以及陌生人消息

状态：设计已定（2026-11-15 作者拍板，见文末「已定的决定」），按 F1～F8 实现中。基线是 main `b3226480`。

## 目标

1. **发现**：找到同一个 relay 上的其他人。
2. **可见性**：每个身份可以选**公开**（能被搜到）或**隐藏**（搜不到）。**默认公开**，CLI 和 TUI 都有一个切换开关。
3. **搜索**：现阶段只按标签（关键词）搜。默认标签是 `友好`。以后改成同时匹配诉求（intent）和标签，复用 `internal/social/intent`。
4. **陌生人消息**：搜到人以后可以直接发消息。对方第一次看到时，可以选择加为好友。不加的话，双方仍是陌生人，**每一方最多给对方发 2 条**。
5. **每日上限**：每个身份**在任意 24 小时内，最多和 10 个新的陌生人开启对话**。
6. CLI 和 Agent24 用同一套能力（`--json` 加 `hyphae contract`）。

不做：按诉求匹配、relay 互联、支付、邀请陌生人进群。邀请陌生人进群以后再计数。

## 和 T01 的关系

T01 关卡规定：wire 格式冻结前，新的高层行为不进生产路径。这个功能是作者直接要求现在上线的，所以按下面的方式处理：

- **不分配新 kind。** 名片沿用现有的 kind 30078，配自己的 `d`/`c` 标签。加好友的确认走现有的 agent 消息通道，作为控制消息，配自己的 `c` 标签。这里完全不碰 intent、trust、referral 的 wire 草案。
- T01-A 冻结时，这两个新格式都写进它的旧事件矩阵，到时候再迁移或重新映射。
- **已知冲突**：T01-B 候选（`t01-public-query-candidate.md`）把标签限定为 ASCII，`[a-z][a-z0-9._-]`，`友好` 过不了。名片标签和 T01 声明里的标签是两个不同的对象。T01 冻结前，要么放宽那条规则，要么把两种标签永久分开。

## 数据模型

### 名片（公开数据）

名片和现有的 agent profile（`d=ProfileDTag`）互相独立，这样隐藏名片不会顺带把 agent 能力也下线。

| 字段 | 取值 |
|---|---|
| kind | 30078（addressable：同一作者的新名片替换旧的） |
| tags | `["d","hyphae-card"]`、`["c","hyphae-card"]`、`["v","1"]`，每个标签一个 `["t",<tag>]`，`["expiration",<now+30d>]` |
| content | 明文 JSON：`{"v":1,"name":"...","tags":["友好","火锅"],"about":"..."}`，`about` 可选 |

- **标签规则**：先 trim，再做 Unicode NFKC（全角 `Ｆ` 和半角 `F` 归为同一个），再用 `golang.org/x/text/cases.Fold` 做大小写折叠（不用 `strings.ToLower`，它处理不了 `ß` 这类字符）。`x/text` 已经是间接依赖。形近字（confusables）不处理。每个标签 1～32 个字符，不能含空白、逗号、`#` 或控制字符。规范化之后去重，最多 10 个。公开时如果没给标签，就用 `["友好"]`。`t` 标签必须和 content 里的 `tags` 数组完全一致，否则读取方丢弃这张名片。
- **名字**：1～64 个字符。默认用身份的昵称，可以改。
- **过期**：`expiration` 设在 30 天后（NIP-40），daemon 运行期间每 7 天刷新一次。长期不上线的人会从结果里消失，死账号不会越积越多。读取方也自己丢弃过期名片，不依赖 relay。不跑 daemon 的人，名片到期后会悄悄消失，所以 `friend visibility` 和 `friend list` 都显示名片的到期时间，快到期时提示重新发布。
- **默认公开的发布时机**：daemon 启动时，如果设置是公开（默认），就发布名片；执行 `friend visibility` 或 `friend tags` 时立即发布。第一次自动公开时，CLI/TUI 提示一次：「你的名片（昵称 + 标签 `友好`）已公开，可以被同一 relay 上的人搜到；用 `hyphae friend visibility hidden` 或 TUI 的 `/visibility` 隐藏」。
- **隐藏**：发布一张同 `d` 的**墓碑**名片（content 为 `{"v":1,"hidden":true}`，没有 `t` 标签），同时发 NIP-09 删除。墓碑在任何支持 addressable 事件的 relay 上都有效，删除则要看 relay 支不支持。隐藏只能让新的搜索找不到你，**收不回别人已经拿到的副本**，CLI 会如实提示。本地记下名片发布到过哪些 relay，隐藏时向每一个都发墓碑。

### 关系（本地，按身份）

存在一张新的 SQLite 表里：`peer_relation (identity, peer, state, sent, received, first_contact_at, updated_at)`。

| state | 含义 |
|---|---|
| `stranger` | 既不是联系人，也没被拉黑。`sent`、`received` 分别记两个方向的消息数。 |
| `friend` | 任意一方接受了。`keystore.json` 里现有的联系人都算好友，不用迁移。 |
| `blocked` | 对方的消息直接丢弃，不通知；你也不能给对方发。 |

「忽略」不是单独的状态。它只是清掉待处理的请求提示，关系仍是 `stranger`。

接收计数按事件 ID 去重，relay 重放或离线补收同一事件不会重复计数。

开启对话另记一张表：`stranger_initiation (identity, peer, at)`。给一个从没给你发过消息的人发出第一条时，写一行。

## 规则

1. **每对上限**：双方是陌生人时，A 最多给 B 发 2 条，B 最多给 A 发 2 条，两个方向分开计。第 3 条在本地拒绝，错误码 `stranger_message_limit`。
2. **接收方**：陌生人的第 1 条显示为「陌生人请求」，提供接受、忽略、拉黑三个选项。第 2 条正常显示。第 3 条及以后照样存下来留记录，但不显示、不通知，只在 `friend pending` 里显示一个被压下的条数。这一层用来对付改过的客户端和绕过限制的发送方。
3. **接受**：接受后把对方加为联系人（昵称取对方名片上的名字，重名时加一段短 npub 后缀），并发一条 `friend_accept` 控制消息。对方客户端收到后，那一侧的关系也变成 `friend`，上限解除。任意一方接受就够了；双方都发了请求时，接受其中任何一个，两人就成为好友。
4. **每日上限**：只有「**和一个新的陌生人开启对话**」才计数，滚动 24 小时内最多 10 次。下面这些不计：
   - 给同一个陌生人发第 2 条；
   - 回复先给你发消息的陌生人；
   - 给好友发消息。

   到上限后发送被拒，错误码 `stranger_daily_limit`，同时告诉你最早那一次什么时候滑出窗口（`resets_at`）。
5. **所有发送路径走同一道检查**：`agent msg`、TUI、daemon 自动回复、outbox 重试，以及 `groupchat create/invite`（邀请陌生人进群也算开启对话，否则它就是绕过上限的后门）。同一条逻辑消息的重试不重复计数（沿用现有的 `logical_id`）。**自动回复是个坑**：两个 daemon 互相自动回复，会把 2 条额度自动用光。所以默认情况下，自动回复不回陌生人。
6. **时钟**：发送方用发送时的本地时钟计算窗口。接收方按到达顺序计数，用的是自己**收到**消息的时间，从不用发送方的 `created_at`。整个社交栈反复得到的就是这条教训。
7. **同一台机器上的多个身份**各自计数。在公共 relay 上，客户端拦不住一个人开很多身份，只能靠 relay 侧和以后的邀请制。
8. **隐藏 ≠ 拒绝联系**：隐藏的人仍然能收到已经知道他 npub 的陌生人的消息，规则一样。「只收好友消息」（丢弃所有陌生人消息）以后再做。

## 执行层（什么能保证，什么不能）

| 层 | 能执行 | 拦不住 |
|---|---|---|
| 发送方客户端（hyphae） | 上面所有规则 | 改过的客户端 |
| 接收方客户端 | 每对上限（超过 2 条就压下）、拉黑；这就是用户真正看到的 | 看不到每日计数，因为它只看得到发给自己的消息 |
| 我们的 relay（`hyphae-relay`，khatru `RejectEvent`） | 二期（见下）：每个作者每天的新收件人数，以及每对的消息数 | 一个人用多个密钥（女巫） |

relay 侧规则（二期）：relay 把「Y 从没给 X 发过任何东西」当作陌生人。`friend_accept` 本身就是一条 Y 发给 X 的消息，所以它自然会解锁这一对。这比客户端规则粗，陌生人回一条也会解锁，但作为兜底够用了。防女巫要靠以后的邀请制（`internal/social/trust`），再往后是 AirAccount。**在公共 relay 上，只有客户端这两层。**

## 控制消息

走现有的 agent 消息通道：kind 30078、`p` 标签、NIP-44 加密。标签是 `["c","hyphae-ctl"]` 和 `["v","1"]`，明文是 `{"v":1,"type":"friend_accept","answers":"<对方发给我的第一条消息的事件 ID>"}`。

编码写死：明文 JSON 直接 NIP-44 加密，**不压缩**，没有 `z` 标签，也没有 `d` 以外的其他标签（`d` 用和普通消息相同的派生方式，保证每条控制消息坐标唯一）；明文不超过 1024 字节，超过就丢弃。

- 因为 `c` 的值不同，按 `c=AgentTag` 订阅的旧客户端收不到它，也就不会显示一团原始 JSON。
- 接收方验签，并确认它是发给自己的。只有同时满足下面三条，accept 才生效：
  - 是对方本人签名的；
  - `answers` 指向**我发给对方**、并且已记在本地的一条消息；
  - 当前关系是 `stranger`（不是 `blocked`）。

  否则一律忽略。这样能挡住三种情况：给素不相识的人发伪造的「接受」来绕过上限；拉黑后再解除时，重放旧的 accept；relay 重放。控制消息本身不占 2 条额度。
- 新客户端要多订阅一个过滤器（`c=hyphae-ctl`、`p=我`）。现有收件过滤器按 `c=AgentTag` 取消息（`internal/messaging/agent.go` 的 `BuildAgentMessageFilter`），所以旧客户端确实收不到控制消息。
- 类型未知或版本更高时，丢弃并记日志，绝不当普通消息显示。

## CLI

全部放在新的顶层命令 `friend` 下，Agent24 只需要认一个命名空间。每条命令都接受 `--as <昵称>`、`--password-stdin`、`--relay` 和 `--json`。

```
hyphae friend visibility public|hidden [--name N] [--tags 友好,火锅] [--about S]
hyphae friend tags --set 友好,火锅          # 当前是公开的话会重新发布
hyphae friend find --tag 友好 [--tag 火锅] [--limit 20] [--timeout 10]
hyphae friend pending                       # 等我处理的陌生人请求
hyphae friend accept|ignore|block|unblock --npub npub1...
hyphae friend list                          # 好友 + 聊过的陌生人，附剩余额度和名片到期时间
hyphae friend quota                         # 今天还能开启几个新陌生人，以及 resets_at
hyphae agent msg --from alice --to npub1... --content ...   # 现有命令，现在要过上面的规则
```

- `find` 向 relay 发 `{"kinds":[30078],"#c":["hyphae-card"],"#t":[tags...],"limit":N}`，过滤在 relay 上完成。必须带 `#c`：30078 是通用应用数据，公共 relay 上别的应用也会带 `t` 标签，只按 `#t` 过滤的话，`limit` 会先被它们占满。本地再检查：`c=hyphae-card`、名片没过期、content 和标签一致。去掉自己、被拉黑的人和墓碑。多个 `--tag` 表示**命中任意一个即可**。结果先按命中的标签数排，再按 `created_at` 排（按时间比较只发生在同一作者的名片之间；跨作者时它只是展示顺序）。`find` 本身不限次数，但每次最多返回 100 条。注意：搜索是读操作，relay 能看到你搜了什么标签。
- `agent msg --to` 本来就接受 npub，所以不必先把陌生人加成联系人。

### `--json` 输出（沿用现有信封 `{"ok":true,"data":...}`）

```json
// friend find
{"ok":true,"data":{"results":[{"npub":"npub1...","name":"小王","tags":["友好","火锅"],
  "matched":["友好"],"relation":"stranger","card_updated_at":1763190000}]}}
// friend pending
{"ok":true,"data":{"pending":[{"npub":"npub1...","name":"小王","first_message":"...",
  "received":2,"suppressed":0,"first_seen_at":1763190000}]}}
// friend quota
{"ok":true,"data":{"daily_limit":10,"used":3,"remaining":7,"resets_at":1763276400,
  "per_stranger_limit":2}}
```

新的稳定错误码加进 `StableErrorCodes` 和 `hyphae contract`，全部映射到退出码 `ExitUserError`：

| 错误码 | 什么时候 | `data` |
|---|---|---|
| `stranger_message_limit` | 已经给这个陌生人发了 2 条 | `{"peer","sent":2,"limit":2}` |
| `stranger_daily_limit` | 24 小时内已经开启了 10 个新陌生人 | `{"used":10,"limit":10,"resets_at"}` |
| `peer_blocked` | 你拉黑了对方（先解除拉黑） | `{"peer"}` |
| `invalid_tag` | 标签不符合规则 | `{"tag","reason"}` |

收件 JSON（`agent inbox`、daemon watch）的每条消息加两个字段：`relation`（`friend|stranger`）和 `stranger_seq`（1、2，或不出现）。Agent24 据此判断什么时候该问用户。

## Agent24 怎么调用

- 通过子进程加 `--json --password-stdin`，和现有契约一样。Agent24 从 `hyphae contract` 得知有 `friend` 可用。
- **建议的默认策略**：
  - 用户要求时，agent 可以执行 `find` 并发出第一条消息。
  - **`accept` 和 `block` 默认要人确认。** Agent24 提供一个策略设置，取值 `ask`（默认）和 `never`。没有自动接受。
  - 自动回复陌生人默认关闭。
- `stranger_*` 错误码是最终结果。agent 不应该换个身份重试，而应该告诉用户还剩多少额度、什么时候重置。
- 以后接入按诉求匹配时，`find` 加 `--intent`，复用 `intent` 包。现有的 `friend` 命令都不变。

## 已核对的实现事实

- 我们的 relay 用的是 khatru + boltdb（`internal/relay/server.go`）。boltdb 把非 hex 的标签值按 md5 建索引（`eventstore/boltdb/helpers.go` 的 `getTagIndexPrefix`），所以 `#t` 过滤对中文标签同样有效。
- 这个 khatru 版本有 `expiration.go`（NIP-40 到期删除）和 `deleting.go`（kind 5 删除）。公共 relay 是否支持，要逐个看。
- 现有的 30078 消息用基于内容哈希的 `d`，profile 用固定的 `ProfileDTag`。`hyphae-card` 不会和它们冲突，两者的分类器都先检查 `c` 标签。

## 交付切片（每片代码 ≤300 行，测试和文档不计）

| # | 内容 | 依赖 |
|---|---|---|
| F1 | `internal/social/contact`：纯逻辑，包括关系状态机、发送检查、接收检查（什么时候压下）和 24 小时窗口计数，配表驱动测试 | — |
| F2 | 存储：两张新表、迁移和读写接口 | F1 |
| F3 | 名片：标签规范化、发布与墓碑、`friend visibility/tags/find` | —（可和 F1 并行） |
| F4a | 发送检查接进 `agent msg` 和 outbox（重试不重复计数） | F2 |
| F4b | 发送检查接进 TUI、daemon 自动回复（默认不回陌生人）、`groupchat create/invite` | F4a |
| F5 | 接收侧：陌生人请求、超过 2 条压下、拉黑、处理 `friend_accept` | F2 |
| F6 | `friend pending/accept/ignore/block/list/quota` 命令、错误码、`hyphae contract` | F4b、F5 |
| F6b | TUI 切换开关：`/visibility` 在公开和隐藏之间切换，状态栏显示当前可见性；陌生人请求在 TUI 里给出接受、忽略、拉黑 | F3、F6 |
| F7 | E2E 脚本（Alice/Bob/Charlie，跑在本地 relay 上），以及给 Agent24 的交接文档 | F6 |
| F8（二期） | `hyphae-relay` 里的 relay 侧限制 | F6 |

验收（在 F7）：
- Alice 公开，标签 `友好`。
- Bob 用 `find --tag 友好` 搜到她，给她发消息。
  - Alice 看到陌生人请求，以及第 1～2 条消息。
  - Bob 的第 3 条在本地被拒。
  - 用改过的客户端发出的第 3 条，在 Alice 那边被压下。
- Alice 执行 `accept`，之后双方可以随便发。
- Charlie 开启第 11 个新陌生人时被拒，错误码 `stranger_daily_limit`。
- Alice 执行 `visibility hidden` 之后，新的 `find` 不再返回她。
- 新身份不做任何设置，daemon 启动后就能被 `find --tag 友好` 搜到（默认公开）。
- Alice 拉黑 Bob 后再解除，Bob 重放旧的 `friend_accept` 不会让他们变成好友，限制仍在。

## 已定的决定（2026-11-15 作者拍板）

1. **默认可见性：公开。** CLI（`friend visibility`）和 TUI（`/visibility`）都提供切换开关。
2. **「2 条」按每个方向各 2 条算**，接收方也能回 2 条。
3. **「一天」是滚动 24 小时**，relay 和客户端用同一条规则。
4. **回复陌生人不算进每日上限**，只有主动开启对话才算。
5. **单独的名片**，不影响 agent profile。
