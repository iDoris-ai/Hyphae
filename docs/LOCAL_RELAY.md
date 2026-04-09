# 本地 Relay 部署（无 Docker）

本文档介绍如何在本地运行 strfry relay，**无需 Docker**。

---

## 方案对比

| 方式 | 复杂度 | 速度 | 适用场景 |
|------|--------|------|----------|
| 一键脚本 | 低 | 5-10分钟编译 | 推荐 |
| 手动编译 | 中 | 5-10分钟编译 | 开发者 |
| Docker | 低 | 2分钟下载 | 有 Docker 环境 |
| Homebrew | 最低 | 1分钟 | 等待官方收录 |

---

## 方案 1: 一键安装脚本（推荐）

```bash
# 自动安装所有依赖并编译
./scripts/install-strfry.sh

# 安装完成后启动 relay
~/.local/bin/strfry relay
```

**后台运行:**
```bash
# 使用 screen/tmux
screen -S relay -d -m ~/.local/bin/strfry relay

# 或 nohup
nohup ~/.local/bin/strfry relay > relay.log 2>&1 &
```

---

## 方案 2: 手动编译

### macOS

```bash
# 1. 安装依赖
brew install cmake libtool automake autoconf

# 2. 克隆代码
git clone https://github.com/hoytech/strfry.git
cd strfry
git submodule update --init

# 3. 编译
make setup-golpe
make -j$(sysctl -n hw.ncpu)

# 4. 启动
./strfry relay
```

### Ubuntu/Debian

```bash
# 1. 安装依赖
sudo apt install -y git g++ make cmake \
    libssl-dev zlib1g-dev liblmdb-dev \
    libflatbuffers-dev libsecp256k1-dev libzstd-dev

# 2. 克隆代码
git clone https://github.com/hoytech/strfry.git
cd strfry
git submodule update --init

# 3. 编译
make setup-golpe
make -j$(nproc)

# 4. 启动
./strfry relay
```

### CentOS/RHEL

```bash
# 1. 安装依赖
sudo yum install -y git gcc-c++ make cmake \
    openssl-devel zlib-devel lmdb-devel \
    flatbuffers-devel secp256k1-devel libzstd-devel

# 2. 克隆并编译（同上）
```

---

## 方案 3: 预编译二进制

从 [GitHub Releases](https://github.com/hoytech/strfry/releases) 下载：

```bash
# macOS
wget https://github.com/hoytech/strfry/releases/latest/download/strfry-macos
chmod +x strfry-macos
./strfry-macos relay

# Linux
wget https://github.com/hoytech/strfry/releases/latest/download/strfry-linux
chmod +x strfry-linux
./strfry-linux relay
```

> ⚠️ 预编译版本可能不是最新，建议从源码编译

---

## 配置说明

### 默认配置

strfry 默认使用 `./strfry.conf` 配置文件：

```hcl
# 数据目录
db = "./strfry-db"

# 仅本地访问（安全）
relay.bind = "127.0.0.1"
relay.port = 7777
```

### 自定义配置

创建 `~/.strfry.conf`：

```hcl
db = "/var/lib/strfry/data"

relay {
    bind = "0.0.0.0"      # 允许外部访问（需防火墙配置）
    port = 7777
    
    info {
        name = "My Local Relay"
        description = "Personal development relay"
        pubkey = "your_npub_here"
    }
}
```

启动时指定配置：
```bash
strfry --config ~/.strfry.conf relay
```

---

## 与 agent-speaker 集成

### 1. 启动本地 Relay

```bash
# 方式 1: 直接启动
strfry relay

# 方式 2: 指定配置
strfry --config ./docker/relay/strfry.conf relay

# 方式 3: 后台运行
nohup strfry relay > relay.log 2>&1 &
echo $! > relay.pid  # 保存 PID
```

### 2. 配置环境变量

```bash
# .env
RELAY_PUBLIC=ws://localhost:7777
RELAY_LOCAL=ws://localhost:7777
```

### 3. 测试连接

```bash
# 获取 relay 信息
./bin/agent-speaker relay info ws://localhost:7777

# 发送测试消息
./bin/agent-speaker agent msg \
    --relay ws://localhost:7777 \
    --to "$BOB_PUB" \
    "Hello from local relay"

# 查询消息
./bin/agent-speaker agent query \
    --relay ws://localhost:7777 \
    --author "$ALICE_PUB" \
    --limit 10
```

---

## 管理脚本

### 创建启动脚本 `~/bin/start-relay.sh`

```bash
#!/bin/bash
STRFRY_BIN="${HOME}/.local/bin/strfry"
CONFIG="${HOME}/.strfry.conf"
LOG="${HOME}/.local/var/log/strfry.log"
PIDFILE="${HOME}/.local/var/run/strfry.pid"

mkdir -p "$(dirname "$LOG")" "$(dirname "$PIDFILE")"

if [ -f "$PIDFILE" ] && kill -0 $(cat "$PIDFILE") 2>/dev/null; then
    echo "Relay already running (PID: $(cat "$PIDFILE"))"
    exit 0
fi

nohup "$STRFRY_BIN" --config "$CONFIG" relay >> "$LOG" 2>&1 &
echo $! > "$PIDFILE"
echo "Relay started (PID: $!)"
```

### 创建停止脚本 `~/bin/stop-relay.sh`

```bash
#!/bin/bash
PIDFILE="${HOME}/.local/var/run/strfry.pid"

if [ -f "$PIDFILE" ]; then
    kill $(cat "$PIDFILE") 2>/dev/null && echo "Relay stopped"
    rm -f "$PIDFILE"
else
    echo "Relay not running"
fi
```

---

## 故障排查

### 端口被占用

```bash
# 检查端口
lsof -i :7777

# 更换端口
strfry relay --config <(echo 'relay.port = 7778')
```

### 权限不足

```bash
# 数据目录权限
chmod 755 ./strfry-db

# 或使用其他目录
mkdir -p ~/.local/share/strfry
# 修改配置: db = "/Users/you/.local/share/strfry"
```

### 编译错误

```bash
# 清理重新编译
make clean
make setup-golpe
make -j4
```

---

## 升级 strfry

```bash
cd /path/to/strfry
git pull
make update-submodules
make -j$(sysctl -n hw.ncpu)
```

---

*文档版本: v0.1*
