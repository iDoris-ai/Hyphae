# 重构计划：标准 Go 项目结构

## 当前文件映射

| 当前文件 | 目标位置 | 说明 |
|----------|----------|------|
| main.go | cmd/agent-speaker/main.go | 唯一入口 |
| agent.go | internal/messaging/agent.go | 消息核心 |
| daemon.go | internal/daemon/daemon.go | 后台服务 |
| watch_cmd.go | internal/daemon/watch.go | 监控命令 |
| encryption.go | pkg/crypto/nip44.go | 加密算法 |
| keystore.go | internal/identity/keystore.go | 密钥存储 |
| identity_cmd.go | internal/identity/commands.go | 身份命令 |
| message_store.go | internal/messaging/store.go | 消息存储 |
| outbox.go | internal/messaging/outbox.go | 待发队列 |
| notify.go | internal/notify/notifier.go | 通知服务 |
| helpers.go | internal/nostr/helpers.go | Nostr 工具 |
| key.go, encode.go, decode.go, verify.go | internal/nostr/ | 密钥相关 |
| event.go, publish.go, relay.go, req.go | internal/nostr/ | 事件相关 |
| history_cmd.go | internal/messaging/history.go | 历史命令 |

## 重构步骤

### Step 1: 提取类型定义
创建 pkg/types/ 共享类型

### Step 2: 移动加密到 pkg/crypto
可被外部导入

### Step 3: 移动 nostr 工具到 internal/nostr
私有实现

### Step 4: 移动 identity 到 internal/identity

### Step 5: 移动 messaging 到 internal/messaging

### Step 6: 移动 daemon 到 internal/daemon

### Step 7: 移动 main.go 到 cmd/agent-speaker/

### Step 8: 更新 import 和构建脚本
