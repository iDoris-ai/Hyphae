# Agent Speaker 测试指南（普通用户版）

> 本文档面向普通用户，无需编程经验，一步步教你如何测试 relay.aastar.io

---

## 📋 测试前准备

### 1. 安装必要的工具

**Mac 用户：**
```bash
# 打开终端，复制粘贴运行：
brew install curl jq websocat
```

**Windows 用户：**
1. 安装 Git Bash: https://git-scm.com/download/win
2. 打开 Git Bash，运行相同命令

**Linux 用户：**
```bash
sudo apt-get install curl jq websocat
```

### 2. 确认网络环境
- 确保能访问互联网
- 公司网络如果有防火墙，可能需要 VPN

---

## 🧪 测试步骤

### 步骤 1：测试网页能否打开（最基础）

打开浏览器，访问：
```
https://relay.aastar.io
```

**预期结果：**
- ✅ 能看到 strfry 的紫色 logo 和信息页面
- ✅ 显示 "strfry: a nostr relay"

**如果打不开：**
- 检查网络连接
- 尝试用 4G/5G 热点（排除公司网络限制）

---

### 步骤 2：测试 WebSocket 连接（核心功能）

打开终端，运行：

```bash
websocat wss://relay.aastar.io
```

然后粘贴发送（复制下面整行，回车）：
```json
["REQ", "test-1", {"kinds": [1], "limit": 3}]
```

**预期结果：**
```json
["EOSE", "test-1"]
```

**解释：**
- `REQ` = 请求查询
- `EOSE` = 查询结束（即使没数据也会返回这个）
- ✅ 看到 `EOSE` 说明 WebSocket 连接正常

**退出：** Ctrl+C

---

### 步骤 3：查看中继信息（NIP-11）

终端运行：

```bash
curl -s -H "Accept: application/nostr+json" https://relay.aastar.io | jq .
```

**预期结果（类似这样）：**
```json
{
  "name": "strfry default",
  "software": "git+https://github.com/hoytech/strfry.git",
  "version": "no-git-commits",
  "supported_nips": [1, 2, 4, 9, 11, 28, 40, 45, 70, 77]
}
```

**关键信息：**
- `name`: 中继名称
- `software`: 使用的软件（strfry）
- `supported_nips`: 支持的协议特性（11个 NIP）

---

### 步骤 4：查看运行指标（Prometheus）

终端运行：

```bash
curl -s https://relay.aastar.io/metrics | head -20
```

**预期结果：**
```
# HELP nostr_client_messages_total Total number of Nostr client messages by verb
# TYPE nostr_client_messages_total counter
nostr_client_messages_total{verb="CLOSE"} X
nostr_client_messages_total{verb="EVENT"} Y
nostr_client_messages_total{verb="REQ"} Z
```

**这些数字代表：**
- `CLOSE` = 关闭连接的次数
- `EVENT` = 收到的事件（消息）数
- `REQ` = 查询请求数

---

### 步骤 5：使用 agent-speaker CLI 测试（高级）

#### 5.1 下载预编译版本

```bash
# Mac (Apple Silicon)
curl -L -o agent-speaker https://github.com/AuraAIHQ/agent-speaker/releases/latest/download/agent-speaker-darwin-arm64
chmod +x agent-speaker

# Mac (Intel)
curl -L -o agent-speaker https://github.com/AuraAIHQ/agent-speaker/releases/latest/download/agent-speaker-darwin-amd64
chmod +x agent-speaker
```

#### 5.2 生成测试密钥

```bash
./agent-speaker key generate
```

复制输出的密钥（64位十六进制字符串）

#### 5.3 查询中继消息

```bash
./agent-speaker agent query --kinds "1,30078" --limit 5
```

**预期：** 可能返回空（如果是新部署的中继），但不会报错

#### 5.4 发送测试消息

```bash
# 替换 YOUR_SECRET_KEY 为你生成的密钥
./agent-speaker agent msg \
  --sec YOUR_SECRET_KEY \
  --to YOUR_PUBLIC_KEY \
  "Hello from test!"
```

**预期：**
- 显示 "✓ Published to wss://relay.aastar.io"
- 或显示错误（如果密钥格式不对）

---

## 🔍 测试结果判断

### ✅ 全部正常的表现：

1. 网页能打开，显示 strfry 信息
2. WebSocket 连接成功，能看到 EOSE
3. NIP-11 接口返回 JSON 数据
4. Prometheus 指标有数字（不为0）
5. CLI 能连接，不报错

### ⚠️ 需要注意的情况：

| 现象 | 可能原因 | 解决方法 |
|------|---------|---------|
| 查询返回空 | 中继刚部署，数据库为空 | 正常，先发送一些测试消息 |
| WebSocket 连不上 | 防火墙/代理 | 换网络或用手机热点测试 |
| 发送消息被拒 | 密钥格式错误 | 检查密钥是 64 位十六进制 |

---

## 📊 测试记录表

打印或复制下面表格，测试时打勾：

```
测试项目                          结果      备注
─────────────────────────────────────────────────────────
□ 1. 网页能打开 (https://relay.aastar.io)
□ 2. WebSocket 连接成功 (websocat)
□ 3. NIP-11 信息获取正常 (curl)
□ 4. Prometheus 指标正常 (curl)
□ 5. CLI 能生成密钥
□ 6. CLI 能查询消息
□ 7. CLI 能发送消息

测试时间：____年____月____日
测试人员：______________
网络环境：□ 公司网络  □ 家庭网络  □ 手机热点
```

---

## 🆘 常见问题

### Q1: 我看不到任何事件，是不是坏了？
**A:** 不是。如果是新部署的中继，数据库是空的，需要先有人发送消息。这是正常的。

### Q2: 怎么知道我的消息发出去了？
**A:** 运行查询命令，如果看到 `limit` 返回的事件数增加了，说明成功了。

### Q3: 公司和家里的结果不一样？
**A:** 公司网络可能有防火墙限制 WebSocket。以能访问的结果为准。

### Q4: 测试会污染真实数据吗？
**A:** 不会。测试用的 kind 30078 是专门的消息类型，且你可以删除自己的测试消息。

---

## 📞 测试遇到问题？

1. 截图终端输出
2. 记录你运行的命令
3. 联系开发团队

---

## ✅ 测试完成后的确认清单

- [ ] 所有基础连接测试通过
- [ ] 能正常查询消息
- [ ] 能正常发送消息
- [ ]  Prometheus 指标有数据更新
- [ ] 没有报错信息

**如果以上都满足，说明 relay.aastar.io 运行正常！** 🎉
