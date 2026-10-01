# Spec — 数据模型与协议细节

> **权威文档是 [`../protocol-v2.md`](../protocol-v2.md) §4-§6。** 本文件是给 `run` 循环用的速查,不是第二份真相。
> M2 的 behavior 信封细节以本文件 + `protocol-v2.md` §4 为准;M2.5 及之后的细节等 D1-D5 拍板后再补。
> 最后更新:2026-09-03

## Behavior 信封(M2 核心)

> 本节与权威文档 §4 的 kind 定义尚未统一；具体编号和 wire 格式未冻结。收口步骤、字段责任与跨仓验收见 [T01 契约收口](t01-contract-gates.md)，以下占位符不能作为生产实现依据。

```json
{
  "kind": "<M2-F5-T5 分配的独立 kind,不再复用 30078>",
  "tags": [
    ["c", "agent-v2"],
    ["b", "<behavior>"],
    ["z", "zstd"]
  ],
  "content": "<zstd(JSON 行为体)>"
}
```

| behavior | 用途 | payload 摘要 | M2 范围 |
|---|---|---|---|
| `register` | 花名册注册 | `{ name, mode, tags, structured? }` | ✅ 实现收发 |
| `publish` | 广播消息 | `{ body, broadcast: { fee, radius } }` | ✅ 实现收发 |
| `inquire` | 询价/询能力 | `{ target, capability, params }` | ✅ 实现收发 |
| `subscribe` | 关注 npub 或标签 | `{ filter }` | ✅ 实现收发 |
| `tip` | 打赏 | `{ to, amount, ref_event? }` | ⚠️ **只定 schema**,执行留 M5 |
| `drifting-bottle` | 漂流瓶 | `{ topic_vec, content, ttl, fee, threshold }` | ⚠️ **只定 schema**,执行留 M2.5 |

### register 三模式

| mode | payload |
|---|---|
| `simple` | `{ name: "alice", mode: "simple" }` |
| `tagged` | `{ name: "alice", mode: "tagged", tags: ["dev","go","AI"] }` |
| `structured` | 完整 schema,含 capabilities / rates / availability / rating |

M1.5 已在 `internal/profile` 实现了这三模式的 profile 侧,M2 是把它收进 behavior 信封。

### Owner-attestation 预留字段(M2-F4)

```
["auth", "<owner-pubkey-hex>", "<conditions>", "<sig-hex>"]
```

**事件作者仍是 Agent 自己的 key**,`auth` 只是授权证据,不是身份覆盖。owner 身份对接 **AAstar AirAccount**(而非泛化 pubkey)。M2 只做数据结构 + 签名/校验函数,**不接真实 SDK**。

> 现在预留的理由:等 M2.5/M5 做 `tip`/`drifting-bottle` 时才发现协议要推翻重来,代价高得多。

## 本地持久化

| 路径 | 内容 | 约束 |
|---|---|---|
| `~/.hyphae/keystore.json` | 身份 + 联系人;nsec 用 AES-256-GCM 加密(密码经 scrypt N=32768) | 权限强制 600;**原子写 + 唯一临时文件名** |
| `~/.hyphae/messages.db` | SQLite(WAL, foreign keys, synchronous=NORMAL) | 含 `audit_log` SHA-256 append-only 哈希链 |
| `~/.hyphae/outbox.json` | 待重试的发送队列 | ⚠️ **当前并发写不安全**,见 M2-F5-T2 |
| `~/.hyphae/profile.enc` | 三层加密 profile | M2.5 计划 |

keystore 还使用稳定的同目录锁文件 `keystore.json.lock`（权限 0600），使用期间不能删除或替换它。创建身份、切换默认身份、添加联系人、轮换密码和升级 legacy 校验 token 时，程序都会持锁后重新读取并更新 keystore。进程内的 Tokio mutex 不能替代这个跨进程文件锁。外部调用方不要先持有此锁再启动 Hyphae 子进程，否则子进程会等待同一把锁。

直接调用 `SaveKeyStore` 更新已有文件时，传入的 keystore 必须来自 `LoadKeyStore`，并且其加载版本必须与磁盘版本一致。用陈旧版本或手工构造的 snapshot 覆盖已有文件会返回 `write_conflict`（退出码 5）；调用方应重新加载后合并更新，或改用会在锁内读取最新状态的事务业务 API。版本元数据只用于本地冲突检测，不会写入 keystore JSON，因此磁盘 wire 格式不变。

升级到使用此锁的版本前，应先停止所有旧版 Hyphae writer。旧版程序不认识锁文件，不能参与新版本的并发写保护。

### keystore 校验 token(跨持久化边界,改动需极度小心)

`verifyToken` 会被**加密后写进** `keystore.json` 的 `Verification` 字段。改这个常量 = 让所有已加密的 keystore 拒绝正确密码。PR #33 踩过一次,现在的实现同时接受 legacy token 并静默升级。**任何未来的改名/改版都必须保留旧值。**

## 字段级隐私(M2.5)

| 类别 | 本地存储 | 出 relay | 出网络 |
|---|---|---|---|
| `public` | 明文 | ✅ 明文 | ✅ |
| `match-only` | AES 加密 | ❌ | 只出向量摘要 |
| `private` | AES 加密 | ❌ | ❌ **永不出本机** |

## 待补(等决策)

M2.5 的 TTL 转发计费格式、邀请券数据结构、漂流瓶向量维度与算法 —— 全部等 `protocol-v2.md` §10 的 D1/D2/D4/D5 拍板,见 `tasks.md` 的 M2.5-F0-T1。
