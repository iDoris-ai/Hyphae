# 逐步重构计划

## 目标
将根目录的单包结构迁移到标准 Go 项目布局，每次迁移后测试。

## 迁移顺序（从低到高依赖）

### Step 1: pkg/types - 类型定义
- 无依赖，可被所有包导入
- 包含: Identity, Contact, Message 等基础类型

### Step 2: internal/common - 共享工具函数
- 依赖: 无 (仅标准库)
- 包含: ParseSecretKey, ParsePublicKey, EncodeNpub 等

### Step 3: pkg/crypto - 加密算法
- 依赖: common (解析密钥)
- 包含: EncryptMessage, DecryptMessage

### Step 4: internal/nostr - Nostr 协议封装
- 依赖: common (工具函数)
- 包含: KeyCmd, EventCmd, RelayCmd 等

### Step 5: internal/identity - 身份管理
- 依赖: common (密钥解析), nostr (类型)
- 包含: IdentityCmd, ContactCmd, Keystore

### Step 6: internal/messaging - 消息核心
- 依赖: identity, crypto, common
- 包含: AgentCmd (msg, inbox), HistoryCmd, MessageStore

### Step 7: internal/notify - 通知服务
- 依赖: 无 (独立)
- 包含: DesktopNotification, PlaySound

### Step 8: internal/daemon - 后台服务
- 依赖: messaging, identity, notify
- 包含: DaemonCmd, WatchCmd

### Step 9: cmd/agent-speaker - 主入口
- 依赖: 所有 internal 包
- 包含: main.go, 命令组装

## 测试策略

每个 Step 完成后:
1. 更新主 main.go 导入新包
2. 运行: go build -o bin/agent-speaker .
3. 运行: ./scripts/test_two_person_complete.sh
4. 确保功能正常后再进行下一步
