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
    ├── LICENSE               # Apache 2.0 许可证
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

```bash
./build.sh           # go build ./cmd/agent-speaker → bin/agent-speaker
./build.sh install   # 同时 install 到 $GOPATH/bin
```

> ⚠️ `Makefile` 里的 `build`/`dev-build`/`test-all` 等目标是重构前（`internal/`+`pkg/` 布局之前）的遗留脚本，依赖的根目录文件（`agent.go` 等）和 `test/` 目录早已不存在，会直接报错。不要用它们，用上面 `./build.sh` 即可。详见 `CLAUDE.md` 的 Build & Development Commands 一节。

`third_party/nak` 是 [fiatjaf/nak](https://github.com/fiatjaf/nak) 的 git submodule，是本项目最早的基座，现在**已不再被任何代码 import**（`internal/nostr/` 是独立实现）。克隆本仓库不需要初始化这个 submodule；只有想参考/更新 vendored 副本时才需要 `git submodule update --init` + `make update-nak`。

## 测试

```bash
go test ./...       # 所有单元测试（每个包自带 *_test.go）
./test.sh            # 构建 + 跑单元测试 + CLI 冒烟检查

# E2E（需要真实 relay + Alice/Bob/Charlie 身份，见 docs/05-acceptance-test-guide.md）
./test_e2e.sh
./test_group_e2e.sh
./test_storage_e2e.sh
./test_profile_e2e.sh
./test_tui_e2e.sh
```

## 添加新功能

1. 在对应的 `internal/<pkg>/commands.go` 里添加新命令（或新建 `internal/<pkg>/`，参照现有包结构）
2. 如需公共库，放入 `pkg/`
3. 添加对应的测试到同目录 `*_test.go`
4. 运行 `go test ./...` 验证
5. 提交代码

## 文档

- [`CLAUDE.md`](CLAUDE.md) — 面向 Claude Code 的仓库导航（构建/测试命令、架构总览）
- [`docs/protocol-v2.md`](docs/protocol-v2.md) — V2 架构决策（L1/L2/L3 协议栈、relay 选型、里程碑总表）
- [`docs/milestones/roadmap-v2.md`](docs/milestones/roadmap-v2.md) — V2 详细任务清单（按里程碑拆分的子任务）
- [`docs/TODO.md`](docs/TODO.md) — 不依赖 V2 重构、可独立推进的短期任务
- [`docs/buzz-comparison-analysis.md`](docs/buzz-comparison-analysis.md) — 与 Buzz（block/buzz）的架构对比与借鉴分析
- [`docs/USER_MANUAL.md`](docs/USER_MANUAL.md) — 用户手册
- [`QUICK_MANUAL_TEST.md`](QUICK_MANUAL_TEST.md) — 双人/三人协作手动测试指南（含 Profile/TUI）
- [`docs/05-acceptance-test-guide.md`](docs/05-acceptance-test-guide.md) — Alice/Bob/Charlie 验收测试约定
- [`docs/archive/`](docs/archive/) — 已被取代的历史文档（strfry 部署方案、旧重构记录等），仅供追溯，不要按其操作

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).  
Copyright 2024-present MushroomDAO Contributors.  
See [NOTICE](./NOTICE) · [TRADEMARK.md](./TRADEMARK.md) · [LICENSE-zh.md](./LICENSE-zh.md) · [TRADEMARK-zh.md](./TRADEMARK-zh.md) for details.
