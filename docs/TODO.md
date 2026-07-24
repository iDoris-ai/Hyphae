# 后续开发任务

## 高优先级

### 1. 私钥加密 🔐
- [ ] 添加密码保护 keystore.json
- [ ] 使用 AES-256-GCM 加密 nsec
- [ ] 密码提示和验证
- [ ] 密码修改功能

## 中优先级

### 2. Agent 资料完善 🧑‍💼
- [ ] 资料评分/信誉系统
- [ ] 资料验证 (Web of Trust)
- [ ] 多 relay 同步策略

### 3. 本地消息持久化 💾
- [x] SQLite 数据库
- [x] 存储发送/接收的消息
- [x] 消息搜索
- [x] 历史记录管理

### 4. 新消息提醒 🔔
- [x] 后台轮询
- [x] 桌面通知
- [ ] 未读消息计数
- [x] 声音提醒

## 低优先级

### 5. UI 改进
- [x] TUI 聊天界面
- [ ] 消息时间线
- [ ] 联系人分组

### 6. 高级功能
- [x] 群聊
- [ ] 文件传输 (Blossom)
- [ ] 消息撤回

## 借鉴 Buzz (block/buzz) 调研的短期事项

> 来源：[`buzz-comparison-analysis.md`](./buzz-comparison-analysis.md) 第 8 节「短期」。
> 这几项是低成本、不依赖 V2 协议重构、可以现在就做的工程改进，不改变产品方向。

- [ ] CLI 增加 `--json` 输出模式（stdout 纯 JSON、stderr 结构化错误对象、区分退出码：成功/用户错误/网络错误/鉴权错误/写冲突），为 CLI 被 Agent24 等其他 Agent 工具化调用做准备
- [ ] `internal/storage` 增加 `audit_log` 表：SHA-256 append-only 哈希链，记录 identity 创建、消息发送/接收、群组增删成员、daemon 自动回复等关键动作
- [ ] `internal/group`（及 contact/membership 模型）显式引入角色概念，至少区分 Human / Agent-Bot，为后续「Agent 是一等成员」打基础

## V1 真实自测发现的问题（2026-07-24）

> 来源：`docs/milestones/roadmap-v2.md` M1 测试状态一节。跑了 `go test ./... -cover` + 本地 relay 全链路真实自测后发现，均不影响核心功能，但值得顺手修掉。

- [ ] `cmd/agent-speaker/main.go` 的 `color.NoColor = false` 是硬编码，不判断 stdout 是否被重定向就强制输出 ANSI 颜色码，管道/脚本消费 CLI 输出会看到转义符乱码（和上面的 `--json` 模式一起做最省事）
- [ ] `history conversation --with <name>` 要求 `<name>` 必须在 contact 列表里，`agent msg --to <name>` 的解析更宽松，两个命令行为不一致，容易被误认为是 bug——统一一下解析规则
- [ ] `internal/nostr`、`internal/common`、`internal/daemon` 三个包单元测试覆盖率分别是 0%/0%/0.5%（其余包普遍 27%-86%），其中 `internal/daemon` 是 outbox 重试/自动回复的核心逻辑，值得补几个关键路径的单元测试
- [ ] daemon 的 outbox 里有历史遗留的失效队列条目（重试全部失败），加一个 `agent-speaker storage outbox` 诊断/清理子命令，方便定位和清理陈旧数据
