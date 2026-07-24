# Task 05：成员角色模型（Human / Agent）

> 规模：M · depends_on：无
> 借鉴来源：`docs/buzz-comparison-analysis.md` §7 第 2 条，参考 Buzz `buzz-db` 的 `Owner/Admin/Member/Guest/Bot` membership 角色思路，但大幅简化——不需要一次性做完整权限系统，只做"这是人还是 Agent"的标记，为后续（M2 owner-attestation、M3/M4 的 Agent 自主行为）打基础。

## 目标

当前 `internal/identity` 的 contact 和 `internal/group` 的成员都是扁平的"名字 + npub"，没有区分对方是人类还是一个 Agent。加一个 `role` 字段，**只做标记，不做权限强制**（权限控制留给后续里程碑，尤其是 M2 的 owner-attestation 机制）。

## 接口

```
agent-speaker contact add --nickname X --npub Y [--role human|agent]   # 默认 human
agent-speaker contact list                                              # 输出增加 ROLE 列
agent-speaker group add-member --name G --user X                        # role 从 contact 记录继承，无需重复指定
agent-speaker group list                                                # 成员列表展示里增加角色标记
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
