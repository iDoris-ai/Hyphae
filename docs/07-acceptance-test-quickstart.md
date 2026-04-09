# 验收测试快速开始

## 环境准备

### 1. 确保项目已构建

```bash
make build
```

### 2. 设置测试环境

```bash
# 生成测试账户 (Alice, Bob, Charlie)
./scripts/setup-test-env.sh

# 或强制重新生成
./scripts/setup-test-env.sh --force
```

这会创建 `.env` 文件，包含：
- 3 个测试账户的密钥对
- Relay 配置
- 测试参数

### 3. 检查配置

```bash
cat .env
cat .gitignore | grep env  # 确保 .env 不会被提交
```

## 运行验收测试

### 一键运行全部测试

```bash
./scripts/acceptance-test.sh
```

### 预期输出

```
========================================
Agent Speaker 全面验收测试
========================================

测试账户:
  Alice:   npub1alice...
  Bob:     npub1bob...
  Charlie: npub1charlie...

========================================
测试 TC-001: 基础消息发送
========================================
场景: Alice 发送未压缩消息给 Bob
...
✅ TC-001 通过

========================================
测试报告
========================================
通过: 12
失败: 0
总计: 12

🎉 所有测试通过!
```

## 手动测试

### 单用户测试

```bash
# 加载环境变量
source .env

# Alice 发送消息给 Bob
./bin/agent-speaker agent msg \
  --sec "$ALICE_NSEC" \
  --to "$BOB_PUB" \
  --relay "$RELAY_PUBLIC" \
  "Hello Bob!"

# Bob 查询消息
./bin/agent-speaker agent query \
  --authors "$ALICE_PUB" \
  --kinds "30078" \
  --relay "$RELAY_PUBLIC" \
  --limit 5
```

### 多终端测试

**Terminal 1 - Alice:**
```bash
source .env
./bin/agent-speaker agent msg --sec "$ALICE_NSEC" --to "$BOB_PUB" "Hi Bob"
```

**Terminal 2 - Bob:**
```bash
source .env
./bin/agent-speaker agent query --authors "$ALICE_PUB" --decompress
```

**Terminal 3 - Charlie:**
```bash
source .env
./bin/agent-speaker agent relay start --port 7777
```

## 测试场景说明

| 测试ID | 场景 | 验证点 |
|--------|------|--------|
| TC-001 | 基础消息发送 | 消息准确送达 |
| TC-002 | 压缩消息发送 | 压缩/解压正确 |
| TC-003 | 双向通信 | 双方能互相发送 |
| TC-004 | 批量查询 | 能查询多条消息 |
| TC-005 | 查看时间线 | 时间线正常显示 |
| TC-006 | 本地 Relay | 能启动本地 relay |
| TC-007 | 公共 Relay | 通过公共 relay 通信 |
| TC-008 | 密钥生成 | 密钥生成正确 |
| TC-009 | npub 编解码 | 编码解码正确 |
| TC-010 | 事件生成 | 事件结构正确 |
| TC-011 | Filter 生成 | Filter 结构正确 |
| TC-012 | 压缩功能 | 压缩解压功能正常 |

## 故障排查

### 网络超时

```bash
# 检查网络连接
ping relay.damus.io

# 使用代理
export https_proxy=http://127.0.0.1:7890
./scripts/acceptance-test.sh
```

### 权限错误

```bash
# 确保脚本可执行
chmod +x scripts/*.sh
```

### 密钥错误

```bash
# 重新生成测试环境
./scripts/setup-test-env.sh --force
```

## 日志查看

```bash
# 查看所有测试日志
ls -la /tmp/tc*.log

# 查看具体测试日志
cat /tmp/tc001_send.log
cat /tmp/tc004_query.log
```

## 下一步

测试通过后，可以：
1. 部署到多台机器进行真实网络测试
2. 启动本地 relay 进行 P2P 测试
3. 进行压力测试（高频发送）
