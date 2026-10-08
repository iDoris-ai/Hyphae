# M1 群聊最小设计与验收

状态：2026-10-07 这是 M1 群聊设计与验收基线。当前 protocol-only 批次仅实现严格 envelope codec 与签名/加密入站验证；持久化状态、消息历史、fanout、CLI/TUI、单 inbox watcher 路由和真实三用户 relay 联调仍未实现。M1 群聊整体仍未完成；只有真实收发和界面验收通过后才能改状态。

## 消息与信任边界

沿用现有 Agent 单收件消息传输。每条逻辑群消息为每位其他成员单独生成 NIP-44 密文和已签名 event：单一 `p` 收件人、不同 event ID/密文/唯一 `d`，共享随机 `logical_id`。复用现有 outbox，失败重试原 event/id；部分发送逐收件人报告，relay ACK 不称为送达。群 ID、正文和群元数据放在收件人加密的 envelope 中；不引入共享群密钥。

envelope 使用专属 typed magic 和明确版本，再解析严格字段（消息类型、版本、group ID、logical ID、body）。普通私聊中的任意 JSON 仍是私聊；识别到未知群协议版本则拒绝，不将原始 JSON 展示为正文。每条群消息不携带 roster，发送者无权通过消息自报或覆写成员。

M1 固定初始 roster，creator 是唯一 authority，不委派 admin。信任状态绑定 creator 公钥、随机 group ID、每位受邀者独有的随机 invite ID、canonical roster hash。roster 去重、规范化并稳定排序后计算 hash。邀请、接受和激活都由 NIP-44 单收件加密；事件签名者必须匹配对应 authority。

## 成员生命周期

1. Creator 建立固定 roster 并逐人发送邀请。收件只创建 pending invite，不自动加入、不显示群聊、不能收发群消息。初次 creator 身份以邀请 event 作者匹配其声明公钥为锚；这是 TOFU，只证明签名作者身份，不证明现实身份。接受提示必须显示 creator npub、联系人是否匹配及完整 roster，不能仅展示群名/昵称；core 不自动创建联系人，也不切换默认身份。受邀者仍须明确接受。
2. 受邀者通过当前身份显式 `group invite accept <invite-id>`；当前身份必须等于邀请目标。接受回执绑定 creator key、group ID、自己的 invite ID、roster hash。拒绝或超时不激活。
3. Creator 校验每份回执的签名、目标和邀请绑定。所有受邀成员都接受同一 roster hash 后，creator 才能逐人发送签名激活；creator 的创建动作视为自身明确同意。每位成员仅在激活与其已接受的邀请完全匹配后转为 active。
4. 不自动 finalize 已接受成员子集。有人拒绝或超时则取消并创建新 group/roster，让新 roster 中每位受邀者重新明确接受。邀请/接受/激活的重放必须幂等且不能让状态倒退。
5. 群消息只有在 event 签名、单收件路由、加密 typed envelope、group ID 和本地 active roster 均通过验证，且 event 作者属于该 roster 时才入群历史。未知 sender、错误 ID/hash、损坏 envelope 或未知版本在写 DM/群历史之前拒绝。
6. `group leave` 是本机退出：停止发送和展示该群消息。它不撤回历史、不抹除其他成员副本，也不保证其他设备停止向该身份发送。M1 成员变化通过新建 group 和新一轮显式接受实现；不承诺远端撤权、历史撤回、动态治理、密钥轮换或 admin 委派。

状态变化与去重需事务化：先 durable 写入再标记 seen、通知界面或报告处理成功。收到 group envelope 不得作为 DM 正文保存，也不得作为 auto-reply 输入。

后续 state consumer 必须先拒绝零值/非 `VerifyIncoming` 构造的 `VerifiedIncoming`，再按信封类型逐条核对签名作者：invite/activate/cancel 的 sender 必须是已绑定 creator；accept/decline 的 sender 必须是对应 invitee。还要核对 event recipient 等于 envelope target，并把 creator + group ID + invite ID + roster hash 与本机已保存 invitation 完整绑定；invite/activate 还须比对规范完整 roster。协议层的 `Decode` 成功仅代表格式和自包含 roster hash 正确，不代表发送者已获授权，控制 envelope 目标也必须由 state 查表验证。

## 单一收件器与界面接口

复用 TUI worker 的单一 `WatchAgentInbox`，不再创建第二条 relay 订阅或重复解密。当前 watcher 在发 update 前直接写 DM，且 update 未带足够路由元数据。群实现需要在 event/签名/收件人校验和解密后、DM 持久化前插入可组合 router/store；以下是未来接口草案，不修改 worker 当前 API：

```go
type VerifiedIncoming struct {
    EventID, SenderNpub, RecipientNpub string
    Kind int
    Tags nostr.Tags
    CreatedAt int64
    Plaintext string // 已按当前 recipient 解密
    IsEncrypted bool
}

type InboxRouter interface {
    Route(context.Context, VerifiedIncoming) (RoutedMessage, error)
}

type InboxStore interface {
    StoreDirect(context.Context, VerifiedIncoming, string) error
    StoreGroup(context.Context, VerifiedIncoming, GroupEnvelope) error
    StoreInvite(context.Context, VerifiedIncoming, GroupInvite) error
}
```

Router 只分类并返回安全的展示类型/会话 ID/body；store 负责幂等 durable 写。单一 watcher 对路由结果写入对应 sink，成功后再发 typed update。独立 `internal/tui/group_model.go` 消费群历史和 watcher 更新；root/app 层装配 store、sender、watcher callback 和 model。`internal/group` 不依赖 `internal/tui`，TUI 只依赖窄接口，避免 import cycle。无需外部 daemon。

protocol-only 批次的 `VerifyIncoming` 构造 opaque 值：它验证 kind 30078、event ID/签名、唯一单收件 `p` 与当前身份，再复用 Agent 的 `messaging.DecodeMessageContent` 和共享 `wireevent` 分类/解码；返回值不保留密钥或密文。未来 router 接线仍须放在统一 watcher 的 DM 持久化之前，保证群 envelope 不落入 DM。协议层拒绝非法 UTF-8/NUL，不记录可控错误正文或原始敏感 payload；终端控制字符的显示安全由 UI 负责。

## 代码范围与验收

当前 protocol-only 批次已落地：`internal/groupchat/protocol.go`、`verified.go` 及对应测试，提供严格 typed codec 和签名验证后的 opaque 入站值。此批没有状态数据库、群消息历史或 fanout transport。后续 state/messages 批次再实现固定 roster 生命周期与幂等历史；fanout 批次再添加逐收件人 event 构造与 sender/outbox 注入边界。整个设计不修改 legacy `internal/group` 表。

剩余实现范围：统一 inbox watcher 在 DM 写入前路由 typed group envelope；命令层提供显式 accept/decline/send/leave；新增独立 TUI 群模型并由 app/root callback 装配；将 sender 注入现有 Agent outbox；三隔离 HOME + 本地 relay 真实三用户验收。成员修改不可只更新一台机器的 SQLite 并假称同步。

验收至少证明：

- Alice 建三人 roster，Bob/Carol 收到 pending invite；二人未明确 accept 前不入群、不显示、不收发。仅两份有效 accept 到齐后才激活；拒绝/超时不能自动缩小 roster。
- 三人交替发唯一正文，所有 active 成员看到发言人/正文正确且恰一次；重启和事件重放后不重复、不回滚；群 UI 不显示 envelope 元数据。
- fanout 每个收件人得到不同密文/event ID、单一 `p`；断线后重试原 event ID，收件只存一份。relay 捕获内容不含正文、群名或 roster。
- 任意 JSON 私聊仍显示为普通私聊；错误身份 accept、未知 sender、伪造/篡改 roster、错 creator/group/invite/hash、跨群重放、重复或未邀请的 accept、坏 envelope、未知版本均失败关闭，且不写入 DM/群历史或改变状态。
- 本机 leave 后该端停止发送和展示，并明确告知无远端撤权/撤历史保证。

现有 `test_group_e2e.sh` 不构成群聊证据：它用单一 HOME、逐个发送未加密 DM、吞掉发送错误，且未核实收件、持久化或 UI。M1 保持未完成，直到上述真实三用户收发与 UI 验收通过。
