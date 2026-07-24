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
