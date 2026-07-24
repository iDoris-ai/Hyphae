# Task 04：审计哈希链（`audit_log` 表）

> 规模：M · depends_on：无
> 借鉴来源：`docs/buzz-comparison-analysis.md` §7 第 6 条，参考 `buzz-audit` 的 SHA-256 append-only chain 设计（但大幅简化——我们是单机单写者，不需要 Buzz 的多租户 + `pg_advisory_lock` 那套）

## 目标

给关键本地操作留一条防篡改的审计轨迹，呼应 Mycelium Protocol「透明是默认值」的价值观，也为将来做信誉统计（M5）提供数据源。**这不是给用户看的功能特性，是给系统自己留证据的基础设施**——不需要花哨的 UI，一张表 + 一个可选的查看命令就够。

## 接口

新增（可选，非本任务硬性要求，若时间允许一起做）：

```
agent-speaker storage audit-log [--limit N] [--verify]
```

`--verify` 模式：从头到尾重算一遍哈希链，报告是否完整（用于检测被篡改或被绕过 API 直接改库的情况）。

## 设计

单进程单写者场景，不需要 Buzz 那种 `pg_advisory_lock`——SQLite 本身写操作是串行的（WAL 模式下单写者），直接在应用层保证"读 last hash → 算 new hash → insert"这三步在同一个 DB transaction 里即可保证链不断。

```go
// internal/storage 或新建 internal/audit
type AuditAction string
const (
    ActionIdentityCreated AuditAction = "identity_created"
    ActionContactAdded    AuditAction = "contact_added"
    ActionMessageSent     AuditAction = "message_sent"
    ActionMessageReceived AuditAction = "message_received"
    ActionGroupCreated    AuditAction = "group_created"
    ActionGroupMemberAdded   AuditAction = "group_member_added"
    ActionGroupMemberRemoved AuditAction = "group_member_removed"
    ActionAutoReplySent   AuditAction = "auto_reply_sent"
)

func LogAction(db *sql.DB, actor, action string, details map[string]any) error
func VerifyChain(db *sql.DB) (ok bool, brokenAtSeq int64, err error)
```

哈希计算：`hash = sha256(seq || ts || actor || action || details_json || prev_hash)`，`seq=0` 时 `prev_hash` 是全零 hash 或空字符串（约定清楚，写进代码注释和这份 spec 里保持一致）。

## 数据

```sql
CREATE TABLE audit_log (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER NOT NULL,          -- unix timestamp
    actor      TEXT NOT NULL,             -- identity nickname 或 npub
    action     TEXT NOT NULL,             -- AuditAction 枚举值
    details    TEXT NOT NULL,             -- JSON blob，具体字段随 action 变化
    prev_hash  TEXT NOT NULL,
    hash       TEXT NOT NULL
);
CREATE INDEX idx_audit_log_actor ON audit_log(actor);
CREATE INDEX idx_audit_log_action ON audit_log(action);
```

走现有的 `internal/storage` 迁移机制加这张表（看一下 `internal/storage/db.go` 现有的迁移是怎么组织的，跟着同样的模式加，不要另起一套迁移框架）。

## 流程

在以下位置调用 `LogAction`（找到对应的现有代码位置，加一行调用，失败时**不要**让审计写入失败影响主流程——审计是 fire-and-forget 语义，参考 Buzz 的"第 10-12 步是 fire-and-forget，失败不影响事件提交本身"）：

- `internal/identity`：创建 identity 后、添加 contact 后
- `internal/messaging`：消息发送成功后、消息接收入库后
- `internal/group`：创建群组、加成员、移除成员、离开群组后
- `internal/daemon`：自动回复发出后

## 验收标准

1. 单元测试：连续写入 N 条记录后，`VerifyChain` 返回 `ok=true`
2. 单元测试：手动改一条记录的 `details` 或 `hash`（模拟篡改），`VerifyChain` 应该在正确的 `seq` 位置报告断链
3. 集成测试：跑一遍 identity create + msg send + group create，之后查 `audit_log` 表应该能看到对应的 3+ 条记录，且 `VerifyChain` 通过
4. 审计写入失败（比如故意让 details 序列化失败）不应该导致上层操作（比如发消息）本身失败——只应该打一条 warning 日志
5. `go test ./...` 全绿

## 实现笔记

- `internal/audit` 没有依赖 `internal/identity`——虽然接口设计初稿设想直接复用 `identity.EnsureKeyStore()` 拿 keystore 目录路径，但 `internal/identity` 的 CLI 命令（`commands.go`）也需要调 `audit.LogAction`，而 `commands.go` 跟 `keystore.go` 是同一个包，一旦 `internal/audit` 反过来 import `internal/identity` 就是循环依赖。解决办法：在 `internal/audit` 里复制了 `keyStoreDir()` 这 3 行路径拼接逻辑（`~/.agent-speaker`），不导入 `identity`。这样 `internal/audit` 对内部包零依赖，可以被 `identity`/`messaging`/`group`/`daemon`/`storage` 任意导入而不会成环。
- 挂了 8 个埋点（首版实现笔记曾误写成 6 个，Codex review 时数出实际是 8 个，已更正），都是 fire-and-forget（写审计失败只打 `⚠️` warning，不影响主流程返回值）：`identity create`、`contact add`、`agent msg` 发送成功后、`agent inbox` 收到消息入库后、`group create`、`group add-member`、`group leave`（用的是 `ActionGroupMemberRemoved`，因为底层调的是同一个 `RemoveMember`，只是自己移除自己）、daemon 的 `sendAutoReply`。**`group remove-member` 命令本身现在是个占位符**（`"not yet implemented"`），没有真实逻辑可埋点，等它真正实现了再补审计调用。
- 可选的查看命令 `agent-speaker storage audit-log [--limit] [--verify]` 也做了，`--verify` 模式下链断裂会返回非零退出码（方便脚本判断）。
- Live 端到端跑了一遍全部 6 个埋点（本地 relay + 真实 identity/group/daemon），`storage audit-log --verify` 确认链完整；又手动改了一行 `details` 模拟篡改，确认 `--verify` 能在正确的 seq 位置报出断裂，之后清空了测试产生的审计记录，没有留着一条"损坏"的链在本地环境里。
- **已知的、可接受的并发限制**：`LogAction` 在一个事务里做"读最后一条 → 算 hash → 插入"，如果同一进程内两个 goroutine真的并发调用（比如 daemon 同时从多个 relay 收到消息触发并发 auto-reply），SQLite 的写序列化 + 显式 `seq` 主键冲突会让其中一次插入失败——按 fire-and-forget 设计，这次会被"丢一条审计记录 + 打 warning"，**不会**导致链损坏（不是数据一致性问题，只是覆盖率问题）。这个限制在包注释里写清楚了，符合 spec 里"单写者"的设计假设，没有为了处理这个边界情况引入锁或重试机制（真出现这种并发场景的概率对单机 CLI 工具来说很低，不值得加复杂度）。

### Codex review 发现并修复的问题（Codex 配额这轮已恢复，正常走 Tier 1）

1. **【Medium，真实 bug】`getDB()` 原来用 `sync.Once` 永久缓存首次初始化失败**。对一次性 CLI 命令没影响（每次调用都是全新进程），但对 daemon 这种长跑进程是真问题——如果第一次审计写入恰好撞上一次瞬时故障（比如磁盘短暂繁忙），会导致审计日志在这个 daemon 进程剩下的整个生命周期里静默失效，没有任何重试。**修复**：改成 `sync.Mutex` + "只缓存成功"的模式（`db` 变量在没有一次完全成功的初始化之前始终是 `nil`），每次调用失败都会在下一次调用时重新尝试，不会永久锁死。补了 `TestGetDBRetriesAfterFailure`（用 `HOME` 指向一个非目录的文件模拟"首次失败"，再指回一个正常目录验证能恢复）。
2. **【顺带发现】初始化失败路径存在资源泄漏**：`sql.Open` 成功后如果后续的 `PRAGMA`/建表语句失败，原代码没有 `Close()` 已经打开的连接就直接返回。跟着上面那次重构一起修了（失败分支都补了 `conn.Close()`）。
3. **【Low/理论问题，但修复成本很低就顺手修了】`computeHash` 原来用 `fmt.Sprintf("%d|%d|%s|%s|%s|%s", ...)` 做哈希前像，不是自定界编码——理论上 `actor`/`action`/`details` 里如果包含 `|` 字符，可能让两个不同的元组拼出同一个前像字符串（进而算出相同哈希）。Codex 认为在当前调用方式下不构成实际可利用的风险（`action` 都是固定常量、不含 `|`），但修复代价很低：改成对一个固定字段顺序的 struct 做 `json.Marshal` 后再哈希——JSON 会转义字符串字段里的引号/反斜杠，字段边界不会因为内容本身而错位。补了 `TestComputeHashNoDelimiterAmbiguity`，验证一组"按朴素拼接会前像碰撞"的输入在新方案下哈希不同。
4. **文档更正**：实现笔记原来写"6 个埋点"，Codex 数出来实际是 8 个（漏数了 `group add-member`），已更正。
5. **顺手做的格式化**：Codex 提到 `internal/group/commands.go`、`internal/storage/commands.go` 需要 `gofmt -w`——这是这两个文件本来就有的 import 顺序问题（跟审计逻辑无关，之前几轮任务里也遇到过，是这个代码库的历史遗留），照 Codex 建议跑了一遍 `gofmt -w`，属于纯格式修正、无行为变化。
