# Agent Speaker 验收测试指南

## 1. 测试环境设计

### 1.1 测试角色定义

我们设计 3 个测试角色来验证点对点、组播和广播通信：

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           测试网络拓扑                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   ┌──────────────┐          ┌──────────────────┐          ┌──────────────┐  │
│   │   Alice      │          │   公共 Relay     │          │    Bob       │  │
│   │  (发起人)     │◄────────►│  wss://relay.    │◄────────►│  (接收人)     │  │
│   │              │          │  damus.io        │          │              │  │
│   │  npub1alice  │          └──────────────────┘          │  npub1bob    │  │
│   │  (用户A)      │                                        │  (用户B)      │  │
│   └──────┬───────┘                                        └──────┬───────┘  │
│          │                                                       │          │
│          │              ┌──────────────────┐                     │          │
│          └─────────────►│  本地 Mini Relay  │◄────────────────────┘          │
│                         │  (可选缓存层)      │                                │
│                         └────────┬─────────┘                                │
│                                  │                                          │
│                         ┌────────▼─────────┐                                │
│                         │    Charlie       │                                │
│                         │   (观察者)        │                                │
│                         │  npub1charlie    │                                │
│                         │  (用户C)          │                                │
│                         └──────────────────┘                                │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 测试用户配置

#### 用户 A: Alice (发起人)

```yaml
name: Alice
role: 发起人/发送者
npub: npub1alice9gf3j5q7z8x2v4n6m1p0k9l8j7h6g5f4e3d2c1b0a9z8y7x6w5v4u3t2s1r
nsec: nsec1alice3gf3j5q7z8x2v4n6m1p0k9l8j7h6g5f4e3d2c1b0a9z8y7x6w5v4u3t2s1r
pubkey_hex: 79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798
functions:
  - 发送压缩消息
  - 创建群聊
  - 查询网络状态
```

#### 用户 B: Bob (接收人)

```yaml
name: Bob
role: 接收人/回复者
npub: npub1bob7gf3j5q7z8x2v4n6m1p0k9l8j7h6g5f4e3d2c1b0a9z8y7x6w5v4u3t2s1r
nsec: nsec1bob1gf3j5q7z8x2v4n6m1p0k9l8j7h6g5f4e3d2c1b0a9z8y7x6w5v4u3t2s1r
pubkey_hex: c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5
functions:
  - 接收并解压消息
  - 发送回复
  - 批量查询
```

#### 用户 C: Charlie (观察者)

```yaml
name: Charlie
role: 观察者/第三方
npub: npub1charliegf3j5q7z8x2v4n6m1p0k9l8j7h6g5f4e3d2c1b0a9z8y7x6w5v4u3t2s1r
nsec: nsec1charlie3gf3j5q7z8x2v4n6m1p0k9l8j7h6g5f4e3d2c1b0a9z8y7x6w5v4u3t2s1r
pubkey_hex: 3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d
functions:
  - 监听公共频道
  - 验证消息不可读(加密)
  - 启动本地 relay
```

### 1.3 测试环境准备

#### 方式 1: 单机多用户测试

```bash
# Terminal 1 - Alice
export NOSTR_SECRET_KEY="nsec1alice..."
./bin/agent-speaker agent msg --to $(cat bob_npub.txt) "Hello Bob!"

# Terminal 2 - Bob
export NOSTR_SECRET_KEY="nsec1bob..."
./bin/agent-speaker agent timeline --decompress

# Terminal 3 - Charlie (启动本地 relay)
./bin/agent-speaker agent relay start --port 7777
```

#### 方式 2: 多机网络测试

```
机器 A (Alice): 192.168.1.101
机器 B (Bob):   192.168.1.102
机器 C (Charlie): 192.168.1.103

公共 Relay: wss://relay.damus.io
```

## 2. 测试用例设计

### 2.1 点对点通信 (P2P)

#### TC-001: 基础消息发送
```
场景: Alice 发送未压缩消息给 Bob
前置: 双方已生成密钥对
步骤:
  1. Alice: ./agent-speaker agent msg --to <bob_npub> --compress=false "Hello"
  2. Bob:   ./agent-speaker agent query --authors <alice_npub>
期望: Bob 收到明文 "Hello"
```

#### TC-002: 压缩消息发送
```
场景: Alice 发送压缩消息给 Bob
前置: 同上
步骤:
  1. Alice: ./agent-speaker agent msg --to <bob_npub> --compress=true "Long message..."
  2. Bob:   ./agent-speaker agent query --decompress
期望: Bob 自动解压并显示原文
```

#### TC-003: 双向通信
```
场景: Alice 和 Bob 互相发送消息
步骤:
  1. Alice -> Bob: "Hi Bob"
  2. Bob -> Alice: "Hi Alice"
  3. 双方查看 timeline
期望: 双方都能看到完整对话历史
```

### 2.2 组播通信 (Group)

#### TC-004: 创建群组消息
```
场景: Alice 给 Bob 和 Charlie 同时发消息
步骤:
  1. Alice 查询两人 npub
  2. Alice: 发送带多个 p-tag 的消息
  3. Bob 和 Charlie 分别查询
期望: 两人都收到相同消息
```

#### TC-005: 群组回复
```
场景: 群组内成员回复
步骤:
  1. Alice 发起群组话题
  2. Bob 回复所有人
  3. Charlie 回复所有人
期望: 形成线程式对话
```

### 2.3 广播通信 (Broadcast)

#### TC-006: 公共频道消息
```
场景: Alice 发送公共广播
步骤:
  1. Alice: 发送不带特定 p-tag 的消息
  2. Bob, Charlie: 查询公共时间线
期望: 所有人都能看到
```

### 2.4 网络拓扑测试

#### TC-007: 通过公共 Relay 通信
```
场景: 双方只连接公共 Relay
配置:
  Alice: --relay wss://relay.damus.io
  Bob:   --relay wss://relay.damus.io
步骤:
  1. Alice 发送消息到公共 Relay
  2. Bob 从公共 Relay 查询
期望: 通信成功
```

#### TC-008: 本地 Mini Relay
```
场景: Charlie 启动本地 Relay，Alice 直连
步骤:
  1. Charlie: ./agent-speaker agent relay start --port 7777
  2. Alice:   ./agent-speaker agent msg --relay ws://192.168.1.103:7777
  3. Charlie: 查看本地存储
期望: 消息存储在 Charlie 本地
```

#### TC-009: 离线消息
```
场景: Bob 离线，Alice 发送消息，Bob 上线后接收
步骤:
  1. Bob 离线
  2. Alice 发送消息到公共 Relay
  3. Bob 上线查询历史
期望: Bob 收到离线期间的消息
```

### 2.5 边界测试

#### TC-010: 大消息测试
```
场景: 发送超大消息(>10KB)
步骤:
  1. 生成 10KB 随机文本
  2. Alice 压缩发送
  3. Bob 接收解压
期望: 消息完整无损
```

#### TC-011: 高频发送
```
场景: 短时间内发送多条消息
步骤:
  1. Alice 循环发送 100 条消息
  2. Bob 批量查询
期望: 无丢失，顺序正确
```

#### TC-012: 网络中断恢复
```
场景: 通信中网络中断
步骤:
  1. Alice 开始发送
  2. 断开网络 10 秒
  3. 恢复网络
期望: 自动重连，消息最终送达
```

## 3. 三人通信场景

### 场景 A: 三角通信

```
Alice -> Bob
  ↓       ↓
 Charlie <-┘

流程:
1. Alice 发消息给 Bob
2. Bob 转发/回复给 Charlie
3. Charlie 回复给 Alice
4. 形成闭环
```

### 场景 B: 星型拓扑

```
     Alice
    /   |   \
   /    |    \
 Bob  Charlie  Dave

Alice 作为中心节点，与多人通信
```

### 场景 C: 网状拓扑

```
Alice <-> Bob
  ↕       ↕
Charlie <-┘

任意两人可直接通信，无需中转
```

## 4. 测试数据准备

### 4.1 测试消息模板

```json
{
  "simple": "Hello",
  "long": "Lorem ipsum dolor sit amet...",
  "json": "{\"type\":\"agent\",\"version\":\"v1\"}",
  "unicode": "你好世界 🌍 こんにちは",
  "code": "function test() { return true; }"
}
```

### 4.2 测试脚本

```bash
#!/bin/bash
# test-p2p.sh - 点对点测试

ALICE_SEC="nsec1alice..."
BOB_PUB="npub1bob..."

echo "[Alice] 发送消息给 Bob..."
./agent-speaker agent msg \
  --sec "$ALICE_SEC" \
  --to "$BOB_PUB" \
  --relay wss://relay.damus.io \
  "Test message from Alice"

echo "[Bob] 查询消息..."
./agent-speaker agent query \
  --authors "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798" \
  --kinds "30078" \
  --decompress
```

## 5. 验收标准

| 测试项 | 通过标准 | 优先级 |
|--------|---------|--------|
| TC-001 | 消息准确送达 | P0 |
| TC-002 | 压缩/解压正确 | P0 |
| TC-003 | 双向通信正常 | P0 |
| TC-004 | 组播无遗漏 | P1 |
| TC-007 | 公共 Relay 可用 | P0 |
| TC-008 | 本地 Relay 可启动 | P1 |
| TC-010 | 大消息完整 | P1 |
| TC-011 | 高频无丢失 | P2 |

---
*文档版本: 1.0*
*更新日期: 2026-04-08*
