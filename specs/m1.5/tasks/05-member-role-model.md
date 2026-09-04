# Task 05：成员角色模型（Human / Agent）

> 规模：M · depends_on：无
> 借鉴来源：`docs/buzz-comparison-analysis.md` §7 第 2 条，参考 Buzz `buzz-db` 的 `Owner/Admin/Member/Guest/Bot` membership 角色思路，但大幅简化——不需要一次性做完整权限系统，只做"这是人还是 Agent"的标记，为后续（M2 owner-attestation、M3/M4 的 Agent 自主行为）打基础。

## 目标

当前 `internal/identity` 的 contact 和 `internal/group` 的成员都是扁平的"名字 + npub"，没有区分对方是人类还是一个 Agent。加一个 `role` 字段，**只做标记，不做权限强制**（权限控制留给后续里程碑，尤其是 M2 的 owner-attestation 机制）。

## 接口

```
hyphae contact add --nickname X --npub Y [--role human|agent]   # 默认 human
hyphae contact list                                              # 输出增加 ROLE 列
hyphae group add-member --name G --user X                        # role 从 contact 记录继承，无需重复指定
hyphae group list                                                # 成员列表展示里增加角色标记
```

## 设计

`Role` 是一个简单的字符串枚举，不是权限位（不要设计成 bitmask 或复杂的 RBAC，那是过度设计——现阶段只是标记）：

```go
type Role string
const (
    RoleHuman Role = "human"
    RoleAgent Role = "agent"
)
```

`pkg/types.Contact` 加 `Role Role` 字段（默认值 `human`，保证向后兼容——老数据没有这个字段时应该被当成 `human`，不能因为加字段炸掉已有 keystore/DB）。`pkg/types.GroupMember`（或等价类型，看现有命名）同理。

## 数据

- `pkg/types.Contact`：加 `Role` 字段，`json:"role,omitempty"`（keystore.json 里的 contact 结构）
- `internal/group` 的 SQLite 表：`ALTER TABLE group_members ADD COLUMN role TEXT NOT NULL DEFAULT 'human'`，走现有迁移机制
- keystore.json 的旧数据没有 `role` 字段时，反序列化应该自然落到 Go 的零值/默认值，不需要写专门的迁移脚本（JSON 反序列化对新增可选字段天然兼容），但要写一个测试验证这一点

## 流程

无复杂状态机——纯 CRUD 字段扩展。`contact add` 增加可选 flag；`group add-member` 从对应 contact 记录读 role 存进 group_members 表。

## 验收标准

1. `contact add --role agent` 能正确存储和读出
2. `contact add` 不传 `--role` 时默认是 `human`
3. 读取一份没有 `role` 字段的旧 keystore.json 测试 fixture，反序列化后所有 contact 的 `Role` 应该是 `human`（零值兜底），不报错
4. `group add-member` 正确从 contact 继承 role 到 `group_members` 表
5. `contact list`/`group list` 的输出（human 模式）能看到角色列
6. `go test ./...` 全绿，包括针对旧数据兼容性的新测试

## 实现笔记

- `pkg/types.Role` 是纯字符串枚举（`RoleHuman`/`RoleAgent`），`String()` 把零值 `""` 当 `human` 处理——这就是向后兼容的关键：旧 keystore.json/group_members 行没有 role 字段时，Go 的 JSON 反序列化和 SQLite 的 `DEFAULT 'human'` 各自兜底，不需要专门的迁移脚本。`TestLegacyContactWithoutRoleFieldDefaultsToHuman` 直接反序列化一段没有 `role` 键的旧 JSON 验证这一点。
- `group_members` 表用 `CREATE TABLE IF NOT EXISTS`（内建 `role TEXT NOT NULL DEFAULT 'human'`）+ 幂等的 `ALTER TABLE ... ADD COLUMN`（忽略 "duplicate column name" 错误）两条路径覆盖：全新库和已存在的旧库都能正确升级。`TestGroupMembersTableMigrationIsIdempotent` 验证 `migrate()` 能重复调用不出错。
- `group create`/`group add-member` 的 role 继承：先用 `identity.ResolveRecipient` 解析昵称拿到 npub（跟 `agent msg`/`history conversation` 共用同一条解析路径），再额外查一次 `identity.GetContact`——只有走 contact 记录的成员才有 role 可继承，走 identity 昵称或裸 npub 解析出来的成员（没有 contact 记录）就默认 human，这是 `ResolveRecipient` 本身解析优先级（contact → identity → 裸 npub）决定的，不是本任务引入的新行为。
- `group list` 只在组内存在至少一个 agent 成员时才显示 `(N human, M agent)` 的细分，全人类的组仍然只显示成员数——这是刻意的最小侵入性展示选择，不影响老用户已经习惯的输出格式。
- Live smoke test（用编译好的二进制在真实 `~/.hyphae/` 环境里跑）：`contact add --role agent`、`contact add --role bogus`（正确报错并以非零码退出）、`group create`/`group add-member` 的 role 继承、`group list` 的 human/agent 细分展示全部验证通过；测试过程中产生的 `role-test-bot` contact 和 `role-test-group-*`/`add-member-role-test-*` group 已在提交前清理干净，不留痕迹在真实 keystore/DB 里。
- 顺带发现并用单独的 PR（[#17](https://github.com/iDoris-ai/hyphae/pull/17)）修复了一个跟本任务无关的遗留问题：任务 4（审计哈希链，PR #16）合并后，`specs/m1.5/README.md` 里的状态行没有同步成 `done`，之前是本地未提交的改动，切分支时被顺带带到了本分支——已经拆出去单独提交，本分支的 diff 里不包含这行。

### Codex review（Tier 1）第一轮

Codex 提了 1 个 Medium + 2 个 Low，都已修复：

1. **Medium — SQLite 迁移的并发安全**：本任务给 `group_members` 加的 `ALTER TABLE ADD COLUMN` 每次 CLI 调用都会跑一遍（`migrate()` 不是只跑一次），如果两个进程同时启动，DDL 可能撞上 `database is locked`/`SQLITE_BUSY` 而不是预期的 "duplicate column name"。这不是本任务独有的问题（`migrate()` 里所有 `CREATE TABLE IF NOT EXISTS` 都有同样的race window），但本任务给这条已经有风险的启动路径又加了一条 DDL，所以在 `internal/storage/db.go` 的 `InitDB()` 里加了 `PRAGMA busy_timeout = 5000`（跟任务 4 `internal/audit` 包的做法一致）。诚实地说：这只对拿到该 PRAGMA 的那个连接生效，`database/sql` 的连接池理论上仍可能派发出没设置过这个值的新连接（任务 4 就是因为这个原因才在 `audit` 包额外加了 `SetMaxOpenConns(1)`）——这里没有对主 storage DB 做同样的收紧，因为那会影响 daemon 等其他调用方的并发特性，超出本任务范围；`busy_timeout` 是按比例的缓解，不是 100% 消除竞态的保证，写清楚在这里供后续参考。
2. **Low — `group list` 静默吞掉角色查询错误**：`GetGroupMembersWithRoles` 失败时原来直接 fallback 成纯数字、不提示。改成失败时打印 `⚠️` 警告到 stderr（保持跟 audit 日志失败提示一致的风格），成功路径行为不变。
3. **Low — 角色校验只在 CLI 层做**：`AddContactWithRole`、`group.CreateGroup`、`group.AddMember` 这几个内部/导出函数原来会无条件持久化任意字符串。加了 `types.Role.IsValid()`（零值 `""` 故意判定为无效，跟 `String()` 的零值兜底语义分开——`IsValid` 是"显式赋值校验"，`String()` 是"展示时的老数据兜底"，两者不冲突）并在这三个写入点做校验，拒绝时不产生部分写入（`CreateGroup` 在插入 group 行之前就检查完 roles map）。新增 `TestAddContactWithRoleRejectsInvalidRole`、`TestAddMemberRejectsInvalidRole`、`TestCreateGroupRejectsInvalidRole` 覆盖。
