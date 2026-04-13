# Agent Speaker 用户手册

> 版本: v0.22.0+ | 最后更新: 2026-04-13

---

## 安装方法

### 方法一：直接下载 Release（推荐）

```bash
# 1. 下载最新版本
curl -L -o agent-speaker.tar.gz \
  https://github.com/AuraAIHQ/agent-speaker/releases/download/v0.22.0/agent-speaker-darwin-arm64.tar.gz

# 2. 解压
tar -xzf agent-speaker.tar.gz

# 3. 移动到系统目录
sudo mv agent-speaker /usr/local/bin/
sudo chmod +x /usr/local/bin/agent-speaker

# 4. 验证安装
agent-speaker --version
```

### 方法二：使用安装脚本

```bash
# 一键安装
curl -fsSL https://raw.githubusercontent.com/AuraAIHQ/agent-speaker/main/install.sh | bash

# 或者使用 wget
wget -qO- https://raw.githubusercontent.com/AuraAIHQ/agent-speaker/main/install.sh | bash
```

### 方法三：Clone 并编译

**前提条件：**
- Go 1.21 或更高版本
- Git

```bash
# 1. Clone 仓库
git clone https://github.com/AuraAIHQ/agent-speaker.git
cd agent-speaker

# 2. 切换到最新分支
git checkout refine  # 或 main

# 3. 编译
go build -o bin/agent-speaker .

# 4. 安装到系统（可选）
ln -sf $(pwd)/bin/agent-speaker /usr/local/bin/agent-speaker
```

### 方法四：Docker

```bash
# 拉取镜像
docker pull ghcr.io/auraaihq/agent-speaker:latest

# 运行
docker run --rm -it \
  -v ~/.agent-speaker:/root/.agent-speaker \
  ghcr.io/auraaihq/agent-speaker:latest \
  agent-speaker identity create --nickname alice
```

---

## 快速开始（两角色测试）

### 场景：在同一台机器上模拟 Alice 和 Bob 通信

```bash
# 1. Alice 创建身份
agent-speaker identity create --nickname alice --default

# 2. Bob 创建身份（另一个终端或当前终端）
agent-speaker identity create --nickname bob

# 3. 获取 Bob 的公钥（复制输出中的 npub）
agent-speaker identity export --nickname bob | grep "Npub:"

# 4. Alice 添加 Bob 为联系人
agent-speaker contact add --nickname bob --npub <bob的npub>

# 5. Bob 获取 Alice 的公钥并添加
agent-speaker identity export --nickname alice | grep "Npub:"
agent-speaker contact add --nickname alice --npub <alice的npub>

# 6. Alice 发送消息给 Bob
agent-speaker agent msg \
  --from alice \
  --to bob \
  --content "嗨 Bob！帮我设计 Logo，预算 500。"

# 7. Bob 查看收件箱
agent-speaker agent inbox --as bob

# 8. Bob 回复
agent-speaker agent msg \
  --from bob \
  --to alice \
  --content "收到！明早给你初稿。"

# 9. Alice 查看回复
agent-speaker agent inbox --as alice

# 10. 查看历史记录
agent-speaker history conversation --with bob
```

---

## 完整测试步骤

### 运行自动测试脚本

```bash
# 在项目目录下
./test_two_person_complete.sh
```

### 多角色测试（3人以上）

```bash
# 创建多个身份
agent-speaker identity create --nickname alice --default
agent-speaker identity create --nickname bob
agent-speaker identity create --nickname charlie

# 互相添加联系人
# Alice 添加 Bob 和 Charlie
agent-speaker contact add --nickname bob --npub <bob_npub>
agent-speaker contact add --nickname charlie --npub <charlie_npub>

# 发送群聊风格消息
agent-speaker agent msg --from alice --to bob --content "项目讨论"
agent-speaker agent msg --from alice --to charlie --content "项目讨论"

# 各自查看
agent-speaker agent inbox --as bob
agent-speaker agent inbox --as charlie
```

---

## 命令参考

### 身份管理

```bash
# 创建身份
agent-speaker identity create --nickname <name> [--default]

# 列出所有身份
agent-speaker identity list

# 设置默认身份
agent-speaker identity use --nickname <name>

# 导出密钥（谨慎使用）
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
# 发送加密消息（默认开启加密）
agent-speaker agent msg \
  --from <your-nickname> \
  --to <recipient-nickname> \
  --content "message text"

# 发送明文消息（不推荐）
agent-speaker agent msg \
  --from alice --to bob \
  --content "plaintext" \
  --encrypt=false

# 查看收件箱
agent-speaker agent inbox --as <your-nickname>

# 实时监控新消息
agent-speaker watch --as alice --notify --sound
```

### 历史记录

```bash
# 查看与某人的对话
agent-speaker history conversation --with bob --limit 50

# 消息统计
agent-speaker history stats

# 搜索消息
agent-speaker history search --query "Logo"
```

---

## 安全说明

### 密钥存储

| 项目 | 位置 | 权限 | 说明 |
|------|------|------|------|
| 密钥目录 | `~/.agent-speaker/` | 700 | 仅所有者访问 |
| 密钥文件 | `keystore.json` | 600 | 仅所有者读写 |
| 消息历史 | `messages.json` | 600 | 仅所有者读写 |

### 加密状态

| 层级 | 算法 | 状态 |
|------|------|------|
| 身份密钥 | secp256k1 | ✅ 协议强制 |
| 传输层 | WebSocket TLS | ✅ 自动 |
| 端到端加密 | XChaCha20-Poly1305 | ✅ 默认开启 |

⚠️ **警告**：
- 私钥目前为明文存储（建议添加系统全盘加密）
- 消息明文存储在本地（历史记录）

---

## 故障排除

### "command not found"

```bash
# 检查安装
which agent-speaker

# 如果没有输出，添加以下到 ~/.zshrc 或 ~/.bashrc
export PATH="/usr/local/bin:$PATH"

# 或使用完整路径
/Users/jason/Dev/tools/agent-mouth-cli/bin/agent-speaker
```

### "identity not found"

```bash
# 检查身份列表
agent-speaker identity list

# 重新创建
agent-speaker identity create --nickname alice
```

### "contact not found"

```bash
# 检查联系人
agent-speaker contact list

# 直接输入 npub 也可以
agent-speaker agent msg --from alice --to <npub> --content "hi"
```

### Relay 连接失败

```bash
# 检查网络
curl -I https://relay.aastar.io

# 使用其他 relay
agent-speaker agent msg \
  --from alice --to bob \
  --content "hi" \
  --relay wss://relay.damus.io \
  --relay wss://relay.nostr.band
```

---

## 更新日志

### v0.22.0 (当前)
- ✅ 昵称系统（隐藏 nsec/npub）
- ✅ NIP-44 端到端加密（XChaCha20-Poly1305）
- ✅ 本地消息持久化
- ✅ 桌面通知提醒
- ✅ 消息历史搜索

### 计划
- [ ] 私钥密码加密
- [ ] TUI 聊天界面
- [ ] 群组聊天
- [ ] 文件传输

---

## 获取帮助

```bash
# 查看帮助
agent-speaker --help
agent-speaker agent --help
agent-speaker identity --help

# 查看版本
agent-speaker --version
```

---

**项目地址**: https://github.com/AuraAIHQ/agent-speaker
**问题反馈**: https://github.com/AuraAIHQ/agent-speaker/issues
