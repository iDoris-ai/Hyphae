# Spec — 数据模型与协议细节

> **权威文档是 [`../protocol-v2.md`](../protocol-v2.md) §4-§6。** 本文件是给 `run` 循环用的速查,不是第二份真相。
> M3 的 behavior 信封细节以本文件 + `protocol-v2.md` §4 为准;M4 及之后的细节等 D1-D5 拍板后再补。
> 最后更新:2026-09-03

## Behavior 信封(M3 核心)

```json
{
  "kind": "<T3.5.2 分配的独立 kind,不再复用 30078>",
  "tags": [
    ["c", "agent-v2"],
    ["b", "<behavior>"],
    ["z", "zstd"]
  ],
  "content": "<zstd(JSON 行为体)>"
}
```

| behavior | 用途 | payload 摘要 | M3 范围 |
|---|---|---|---|
| `register` | 花名册注册 | `{ name, mode, tags, structured? }` | ✅ 实现收发 |
| `publish` | 广播消息 | `{ body, broadcast: { fee, radius } }` | ✅ 实现收发 |
| `inquire` | 询价/询能力 | `{ target, capability, params }` | ✅ 实现收发 |
| `subscribe` | 关注 npub 或标签 | `{ filter }` | ✅ 实现收发 |
| `tip` | 打赏 | `{ to, amount, ref_event? }` | ⚠️ **只定 schema**,执行留 M7 |
| `drifting-bottle` | 漂流瓶 | `{ topic_vec, content, ttl, fee, threshold }` | ⚠️ **只定 schema**,执行留 M4 |

### register 三模式

| mode | payload |
|---|---|
| `simple` | `{ name: "alice", mode: "simple" }` |
| `tagged` | `{ name: "alice", mode: "tagged", tags: ["dev","go","AI"] }` |
| `structured` | 完整 schema,含 capabilities / rates / availability / rating |

M2(原 M1.5)已在 `internal/profile` 实现了这三模式的 profile 侧,M3 是把它收进 behavior 信封。

### Owner-attestation 预留字段(F3.4)

```
["auth", "<owner-pubkey-hex>", "<conditions>", "<sig-hex>"]
```

**事件作者仍是 Agent 自己的 key**,`auth` 只是授权证据,不是身份覆盖。owner 身份对接 **AAstar AirAccount**(而非泛化 pubkey)。M3 只做数据结构 + 签名/校验函数,**不接真实 SDK**。

> 现在预留的理由:等 M4/M7 做 `tip`/`drifting-bottle` 时才发现协议要推翻重来,代价高得多。

## 本地持久化

| 路径 | 内容 | 约束 |
|---|---|---|
| `~/.hyphae/keystore.json` | 身份 + 联系人;nsec 用 AES-256-GCM 加密(密码经 scrypt N=32768) | 权限强制 600;**原子写 + 唯一临时文件名** |
| `~/.hyphae/messages.db` | SQLite(WAL, foreign keys, synchronous=NORMAL) | 含 `audit_log` SHA-256 append-only 哈希链 |
| `~/.hyphae/outbox.json` | 待重试的发送队列 | ⚠️ **当前并发写不安全**,见 T3.5.1 |
| `~/.hyphae/profile.enc` | 三层加密 profile | M4 计划 |

### keystore 校验 token(跨持久化边界,改动需极度小心)

`verifyToken` 会被**加密后写进** `keystore.json` 的 `Verification` 字段。改这个常量 = 让所有已加密的 keystore 拒绝正确密码。PR #33 踩过一次,现在的实现同时接受 legacy token 并静默升级。**任何未来的改名/改版都必须保留旧值。**

## 字段级隐私(M4)

| 类别 | 本地存储 | 出 relay | 出网络 |
|---|---|---|---|
| `public` | 明文 | ✅ 明文 | ✅ |
| `match-only` | AES 加密 | ❌ | 只出向量摘要 |
| `private` | AES 加密 | ❌ | ❌ **永不出本机** |

## 待补(等决策)

M4 的 TTL 转发计费格式、邀请券数据结构、漂流瓶向量维度与算法 —— 全部等 `protocol-v2.md` §10 的 D1/D2/D4/D5 拍板,见 `tasks.md` 的 T4.0。
