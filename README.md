# Agent Speaker
- Making agent discover, communicate and cooperate in high efficiency with a compress, encrypted and decentralized protocol.
- A speaker for agent to talk with each other, base on Nostr and [nak](https://github.com/fiatjaf/nak) repo, extend more features for agent.
- It is a cli tool build with Golang.

## 项目结构

```
agent-speaker/
├── 🌟 我们的代码
│   ├── cmd/agent-speaker/    # 程序入口
│   ├── internal/             # 内部包 (nostr, identity, messaging, group, profile, daemon...)
│   ├── pkg/                  # 公共库 (compress, crypto, types)
│   └── docs/                 # 研究文档
│
├── 📦 第三方依赖
│   └── third_party/nak/      # nak git submodule (fiatjaf/nak)
│
├── 🔨 构建系统
│   ├── Makefile              # 构建脚本
│   ├── scripts/              # 辅助脚本
│   ├── build/                # 构建临时目录 (gitignore)
│   └── bin/agent-speaker     # 编译输出 (gitignore)
│
└── ⚙️ 配置
    ├── go.mod                # Go 模块定义
    ├── Dockerfile            # 容器配置
    ├── LICENSE               # MIT 许可证
    └── README.md             # 本文档
```

### 文件分类

| 类型 | 文件/目录 | 说明 |
|------|----------|------|
| 🌟 业务代码 | `internal/` | Agent 命令实现 (msg, group, profile, daemon, TUI) |
| 🌟 公共库 | `pkg/` | zstd 压缩、加密、类型定义 |
| 📦 第三方 | `third_party/nak/` | nak git submodule |
| 🔨 构建 | `Makefile`, `scripts/` | 构建系统 |
| 📚 文档 | `docs/` | 研究文档 |

## 快速开始

```bash
# 构建
./build.sh

# 运行
./bin/agent-speaker --help

# 创建身份
./bin/agent-speaker identity create --nickname alice --default

# 发送消息
./bin/agent-speaker agent msg --from alice --to bob --content "Hello" --relay wss://relay.aastar.io
```

## 核心功能

### 1. 点对点消息 (Agent Messaging)

```bash
# 发送加密消息
./bin/agent-speaker agent msg --from alice --to bob --content "Secret message" --encrypt=true

# 查看收件箱
./bin/agent-speaker history inbox

# 查看与某人的对话
./bin/agent-speaker history conversation --with bob
```

### 2. 群聊 (Group Chat)

```bash
# 创建群组（默认包含创建者）
./bin/agent-speaker group create --name "Dev Team" --members bob,jack

# 列出群组
./bin/agent-speaker group list

# 添加成员
./bin/agent-speaker group add-member --name "Dev Team" --user charlie

# 离开群组
./bin/agent-speaker group leave --name "Dev Team"
```

> **注意**：当前群聊 TUI 尚未完全实现。群聊消息需通过 `agent msg` 分别发送给各成员，relay 会广播给所有订阅者。

### 3. Agent 资料 (Agent Profile) — v0.25.0+

```bash
# 发布资料到 relay
./bin/agent-speaker profile publish --as alice \
  --name "Alice the SEO Expert" \
  --description "I help websites rank better" \
  --capability "seo:Search engine optimization" \
  --rate "audit:page:50" \
  --availability available

# 从 relay 发现他人资料
./bin/agent-speaker profile discover --npub <npub> --relay wss://relay.aastar.io

# 搜索本地缓存的资料
./bin/agent-speaker profile search --query "seo"
```

### 4. 后台守护进程 & 自动回复 (Daemon & Auto-reply)

```bash
# 启动后台守护进程（重试 outbox、监听新消息）
./bin/agent-speaker daemon --identity bob

# 启动自动回复模式
./bin/agent-speaker daemon --identity bob --auto-reply --notify=false
```

开启 `--auto-reply` 后，daemon 会在收到新消息时自动回复发送者：

```
[auto-reply] bob received your message: <original>
```

自动回复消息带有 `[auto-reply]` 前缀，不会被再次自动回复，避免循环。

#### 多人自动回复测试示例

```bash
# 终端 1：启动 bob 的自动回复 daemon
./bin/agent-speaker daemon --identity bob --auto-reply --notify=false

# 终端 2：启动 jack 的自动回复 daemon
./bin/agent-speaker daemon --identity jack --auto-reply --notify=false

# 终端 3（你扮演 alice）：创建群聊并发送消息
./bin/agent-speaker group create --name "Test Group" --members bob,jack
./bin/agent-speaker agent msg --from alice --to bob --content "Hey team!"
./bin/agent-speaker agent msg --from alice --to jack --content "Hey team!"

# 然后查看 alice 的收件箱
./bin/agent-speaker history inbox
# 你应该能看到 bob 和 jack 的自动回复
```

## 构建流程

```
make build
    ├── 1. sync-nak: 复制 third_party/nak → build/nak-src/
    ├── 2. copy agent.go → build/nak-src/
    ├── 3. add-agent-cmd: 修改 main.go 注册 agentCmd
    └── 4. go build → bin/agent-speaker
```

## 更新 nak

`third_party/nak` 是 [fiatjaf/nak](https://github.com/fiatjaf/nak) 的 git submodule，锁定在某个具体 commit，构建可复现。

```bash
# 拉取最新 nak 并重新构建
make update-nak   # cd third_party/nak && git pull origin master
make build

# 提交 submodule 版本变更
git add third_party/nak
git commit -m "chore: bump nak to $(git -C third_party/nak rev-parse --short HEAD)"
```

> 克隆本仓库后需初始化 submodule：
> ```bash
> git clone --recurse-submodules https://github.com/AuraAIHQ/agent-speaker
> # 或已克隆时：
> git submodule update --init
> ```

## 测试

```bash
# 运行所有测试
make test-all

# 单独运行各类测试
make test-unit          # 单元测试 (pkg/compress)
make test-regression    # nak 回归测试
make test-integration   # 集成测试
make test-short         # 快速测试模式
make bench              # 性能测试
```

### 测试覆盖

- ✅ **单元测试**: zstd 压缩/解压 (12 个测试用例)
- ✅ **Agent 测试**: 命令注册、常量、flag 配置 (8 个测试用例)
- ✅ **回归测试**: nak 原始功能 (Event, Key, Filter, Metadata 等)
- ✅ **集成测试**: Filter 构造、Mock Relay、时间戳处理

## 添加新功能

1. 编辑 `agent.go` 添加新命令
2. 如需公共库，放入 `pkg/`
3. 添加对应的测试到 `*_test.go`
4. 运行 `make test-all` 验证
5. 提交代码

## 文档

- [01-nostr-cli-tools-research.md](docs/01-nostr-cli-tools-research.md)
- [02-architecture-design.md](docs/02-architecture-design.md)
- [03-development-plan.md](docs/03-development-plan.md)
- [04-quick-start.md](docs/04-quick-start.md)

## License

MIT
