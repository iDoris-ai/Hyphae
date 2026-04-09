# 产品部署指南

本文档记录 agent-speaker 的长期部署规划和各环境的部署方案。

---

## 目录

- [当前阶段：开发测试](#当前阶段开发测试)
- [Relay 部署方案](#relay-部署方案)
- [长期规划](#长期规划)

---

## 当前阶段：开发测试

### 国内用户推荐配置

由于网络环境，国际 Relay（如 damus.io）在国内访问困难。建议按以下优先级选择：

```bash
# 1. 中国/亚洲 Relay（首选）
RELAY_CN=wss://relay.nostr.cn        # 中国
RELAY_HK=wss://relay.nostr.hk        # 香港

# 2. 本地 Relay（开发/离线）
RELAY_LOCAL=ws://localhost:7777      # Docker 本地部署

# 3. 代理模式（临时方案）
export https_proxy=http://127.0.0.1:7890
export http_proxy=http://127.0.0.1:7890
```

### 快速启动本地 Relay

```bash
# 启动本地 relay（Docker）
./scripts/setup-local-relay.sh start

# 查看状态
./scripts/setup-local-relay.sh status

# 停止 relay
./scripts/setup-local-relay.sh stop

# 查看日志
./scripts/setup-local-relay.sh logs
```

### 测试验证

```bash
# 使用本地 relay 测试
./bin/agent-speaker agent msg \
  --relay ws://localhost:7777 \
  --to "$BOB_PUB" \
  "Hello via local relay"

# 使用中国 relay 测试
./bin/agent-speaker agent msg \
  --relay wss://relay.nostr.cn \
  --to "$BOB_PUB" \
  "Hello via CN relay"
```

---

## Relay 部署方案

### 方案对比

| 方案 | 成本 | 可靠性 | 网络要求 | 适用阶段 |
|------|------|--------|----------|----------|
| 公共 Relay (CN) | 免费 | 中 | 国内直连 | 开发测试 |
| 本地 Relay | 免费 | 高 | 局域网 | 开发/离线 |
| 国内 VPS | ~50元/月 | 高 | 国内直连 | 生产环境 |
| 个人 Relay | 免费 | 中 | P2P | 成熟阶段 |

### 1. 公共 Relay（当前）

使用现有的中国/亚洲 Relay：

```bash
# 推荐列表
wss://relay.nostr.cn      # 中国
wss://relay.nostr.hk      # 香港  
wss://nos.lol             # 日本
```

**优点**：零成本、即开即用  
**缺点**：依赖第三方、可能有速率限制

### 2. 本地 Relay（开发）

使用 Docker 在本地部署 strfry：

```bash
cd docker/relay
docker-compose up -d
```

**优点**：完全控制、离线可用、零延迟  
**缺点**：仅本地可用、需要 Docker

**配置详情**：
- 镜像：`dockurr/strfry:latest`
- 端口：`7777`
- 数据目录：`docker/relay/data/`
- 配置文件：`docker/relay/strfry.conf`

### 3. 国内 VPS 部署（生产备选）

在国内云服务器（阿里云/腾讯云）部署：

```bash
# 1. 购买香港/内地节点 VPS
# 2. 安装 strfry
git clone https://github.com/hoytech/strfry.git
cd strfry && make && ./strfry relay

# 3. Nginx 反向代理 + HTTPS
server {
    listen 443 ssl;
    server_name relay.yourdomain.cn;
    
    location / {
        proxy_pass http://localhost:7777;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

**优点**：国内低延迟、自主可控  
**缺点**：需要运维、有成本

---

## 长期规划

### Phase 1: 集中式（现在）

**目标**：验证技术可行性

- ✅ 使用公共中国 Relay
- ✅ 本地 Docker Relay 开发
- ✅ 基础消息协议验证

### Phase 2: 混合式（近期）

**目标**：支持生产环境部署

- 国内 VPS 部署专属 Relay
- 多 Relay 故障转移
- Relay 健康检查机制

```go
// 示例：多 Relay 自动选择
relays := []string{
    "wss://relay.yourdomain.cn",
    "wss://relay.nostr.cn", 
    "ws://localhost:7777",
}
available := healthCheck(relays)
publishTo(available[0])
```

### Phase 3: 个人 Relay（中期）

**目标**：每个用户自带轻量级 Relay

```bash
# 用户启动个人 relay
./bin/agent-speaker relay start --port 7777

# 自动发现机制
# - 通过 Nostr metadata 宣告个人 relay 地址
# - DHT 网络发现其他用户 relay
# - 消息存在发送方本地，接收方主动拉取
```

**技术方案**：
- 嵌入式 strfry（轻量级）
- WebRTC 点对点连接
- IPFS/Filecoin 存储层（可选）

**优点**：
- 完全去中心化
- 无服务器成本
- 数据主权

### Phase 4: P2P 网络（远期）

**目标**：纯 P2P 通信，无固定 Relay

```
┌─────────┐      WebRTC      ┌─────────┐
│  Alice  │ ◄──────────────► │   Bob   │
│ (节点A) │   信令: Nostr    │ (节点B) │
└────┬────┘                  └────┬────┘
     │                            │
     └──────┐              ┌──────┘
            │              │
            ▼              ▼
      ┌─────────────────────────┐
      │     DHT 网络发现        │
      │  (类似 BitTorrent)      │
      └─────────────────────────┘
```

**关键技术**：
- libp2p 网络栈
- WebRTC 数据传输
- CRDT 冲突解决（离线消息合并）

---

## 附录

### Relay 测试列表

| Relay | 地区 | 状态 | 备注 |
|-------|------|------|------|
| wss://relay.nostr.cn | 中国 | ✅ | 国内直连 |
| wss://relay.nostr.hk | 香港 | ✅ | 国内直连 |
| wss://nos.lol | 日本 | ✅ | 亚洲节点 |
| wss://relay.damus.io | 美国 | ❌ | 需代理 |
| ws://localhost:7777 | 本地 | ✅ | Docker |

### 常用命令

```bash
# 测试 relay 连接
./bin/agent-speaker relay info wss://relay.nostr.cn

# 列出所有 relay 事件
./bin/agent-speaker relay query ws://localhost:7777 --limit 10

# 本地 relay 管理
./scripts/setup-local-relay.sh start
./scripts/setup-local-relay.sh stop
./scripts/setup-local-relay.sh status
```

### 网络故障排查

```bash
# 1. 测试连通性
curl -v https://relay.nostr.cn

# 2. 测试 WebSocket
wscat -c wss://relay.nostr.cn

# 3. 使用代理
export https_proxy=http://127.0.0.1:7890
./bin/agent-speaker relay info wss://relay.damus.io
```

---

*文档版本: v0.1*  
*最后更新: 2026-04-08*
