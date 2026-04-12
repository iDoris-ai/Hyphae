# Agent Speaker 用户手册

## 安全警告 ⚠️

### 当前安全状态

| 项目 | 状态 | 说明 |
|-----|------|------|
| 私钥存储 | ⚠️ 文件权限保护 | 存储在 `~/.agent-speaker/keystore.json`，权限 600 |
| 私钥加密 | ❌ 未加密 | 当前为明文存储，建议添加密码保护 |
| 传输加密 | ⚠️ WebSocket TLS | 使用 wss://，但消息内容为明文 |
| 端到端加密 | ❌ 未实现 | 需要添加 NIP-44 加密 |

### 风险说明
1. **私钥泄露**：如果他人获取你的 keystore.json 文件，可以完全控制你的身份
2. **消息监听**：Relay 管理员可以看到所有消息内容
3. **本地安全**：需要保护好本地文件系统

---

## 快速开始

### 1. 安装

```bash
cd /Users/jason/Dev/tools/agent-mouth-cli
go build -o bin/agent-speaker .
```

### 2. 创建身份

```bash
# 创建 Alice 身份并设为默认
./bin/agent-speaker identity create --nickname alice --default

# 创建 Bob 身份
./bin/agent-speaker identity create --nickname bob
```

### 3. 添加联系人

```bash
# Alice 添加 Bob（需要 Bob 的 npub）
./bin/agent-speaker contact add --nickname bob --npub npub1xxx...

# Bob 添加 Alice
./bin/agent-speaker contact add --nickname alice --npub npub1yyy...
```

### 4. 发送消息

```bash
# Alice 发送消息给 Bob
./bin/agent-speaker agent msg --from alice --to bob --content "Hello Bob!"
```

### 5. 查看收件箱

```bash
# Bob 查看消息
./bin/agent-speaker agent inbox --as bob
```

---

## 命令参考

### 身份管理

```bash
# 创建身份
agent-speaker identity create --nickname <name> [--default]

# 列出身份
agent-speaker identity list

# 设置默认身份
agent-speaker identity use --nickname <name>

# 导出私钥（谨慎！）
agent-speaker identity export --nickname <name>
```

### 联系人管理

```bash
# 添加联系人
agent-speaker contact add --nickname <name> --npub <npub>

# 列出联系人
agent-speaker contact list
```

### 消息通信

```bash
# 发送消息
agent-speaker agent msg \
  --from <your-nickname> \
  --to <recipient-nickname> \
  --content "message"

# 查看收件箱
agent-speaker agent inbox \
  --as <your-nickname> \
  [--limit 10]
```

---

## 高级功能

### 使用非默认 Relay

```bash
./bin/agent-speaker agent msg \
  --from alice \
  --to bob \
  --content "Hello" \
  --relay wss://relay.example.com
```

### 查看详细事件

```bash
./bin/agent-speaker agent query \
  --from bob \
  --to alice \
  --decompress
```

---

## 故障排除

### "identity not found"
- 确保已创建身份：`identity list`
- 检查昵称拼写

### "contact not found"
- 确保已添加联系人：`contact list`
- 可以直接使用 npub 代替昵称

### "connection failed"
- 检查网络连接
- 确认 relay 地址正确
- 尝试其他 relay

---

## 更新日志

### v0.22.0 (当前)
- ✅ 昵称系统（隐藏 nsec/npub）
- ✅ 安全密钥存储（文件权限 600）
- ✅ 联系人管理
- ✅ 收件箱功能
- ⚠️ 私钥明文存储（待加密）
- ⚠️ 消息明文传输（待 E2E 加密）

### 计划
- [ ] 私钥密码加密
- [ ] NIP-44 端到端加密
- [ ] 消息本地持久化
- [ ] 新消息提醒

---

*最后更新: 2026-04-12*
