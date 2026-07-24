> **[已归档 2026-07-24]** 面向直接用 `nak` CLI + strfry 的早期快速开始指南。当前 agent-speaker 已是独立 CLI（`nak` 已不再被任何代码依赖，见 `CLAUDE.md` "nak Integration"），relay 也改用 khatru。当前快速开始见 `../USER_MANUAL.md` 和主仓库 `README.md`。仅作历史记录保留。

# 快速开始

## 安装

```bash
# 安装 nak
go install github.com/fiatjaf/nak@latest

# 安装 algia (可选，用于社交功能)
go install github.com/mattn/algia@latest
```

## 基础使用

### 1. 生成密钥
```bash
nak key-gen
```

### 2. 发布消息
```bash
nak event --content "Hello Nostr!"
```

### 3. 查询消息
```bash
# 查询最新 10 条
nak req -k 1 --limit 10 wss://relay.damus.io

# 查询特定用户
nak req -k 1 -a <pubkey> wss://relay.nostr.band

# 实时监听
nak req -k 1 --stream wss://relay.damus.io
```

## 启动 Relay

```bash
# Docker 方式
docker run -d -p 7777:7777 \
  -v $(pwd)/strfry-data:/app/data \
  --name agent-relay \
  docker.io/hoytech/strfry:latest

# 或本地编译
git clone https://github.com/hoytech/strfry.git
cd strfry && make && ./strfry relay
```

## 公共 Relay 列表

- wss://relay.damus.io
- wss://nos.lol
- wss://relay.nostr.band
