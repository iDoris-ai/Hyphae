# Hyphae
- Making agent discover, communicate and cooperate in high efficiency with a compress, encrypted and decentralized protocol.
- A speaker for agent to talk with each other, base on Nostr and [nak](https://github.com/fiatjaf/nak) repo, extend more features for agent.
- It is a cli tool build with Golang.

## 从 agent-speaker 升级

本项目原名 `agent-speaker`，改名为 `hyphae`（呼应 Mycelium Protocol 的网络命名）。**新版二进制会自动识别并升级旧的加密 keystore**（首次用正确密码解锁时，静默把校验 token 换成新版本，无需手动操作、私钥不受影响）。但下面几件事仓库改不到，需要手动处理：

1. **本地数据目录**：旧版把身份和消息存在 `~/.agent-speaker/`，新版读写 `~/.hyphae/`，不会自动搬迁。手动执行一次：
   ```bash
   mv ~/.agent-speaker ~/.hyphae
   ```
   如果 `~/.hyphae` 已经存在（比如已经跑过一次新版本创建了空目录），**不要**直接覆盖——请先确认两边内容，手动合并需要的身份/消息。
2. **macOS 后台服务（如果配置过 launchd 自启动）**：先卸载旧的 `com.agent-speaker.daemon`，再装新的 `com.hyphae.daemon`，详见 [`docs/README_DAEMON.md`](docs/README_DAEMON.md#配置自动启动macos)——旧 plist 的 `KeepAlive=true` 会在旧二进制找不到身份时无限重启循环。
3. **旧二进制**：`install.sh` 不会自动删除 `/usr/local/bin/agent-speaker`，装完新版本后自行 `rm` 掉，避免和新版本混用（尤其是两边同时跑 daemon 会对同一身份产生重复的自动回复）。
4. **脚本/自动化**：`AGENT_SPEAKER_OUTPUT=json` 环境变量作为兼容别名继续有效，但新脚本建议改用 `HYPHAE_OUTPUT=json`。

## 项目结构

```
hyphae/
├── 🌟 我们的代码
│   ├── cmd/hyphae/    # 程序入口
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
│   └── bin/hyphae     # 编译输出 (gitignore)
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
./bin/hyphae --help

# 创建身份
./bin/hyphae identity create --nickname alice --default

# 发送消息
./bin/hyphae agent msg --from alice --to bob --content "Hello" --relay wss://relay.aastar.io
```

## 核心功能

### 1. 点对点消息 (Agent Messaging)

```bash
# 发送加密消息
./bin/hyphae agent msg --from alice --to bob --content "Secret message" --encrypt=true

# 查看收件箱
./bin/hyphae history inbox

# 查看与某人的对话
./bin/hyphae history conversation --with bob
```

### 2. 群聊 (Group Chat)

```bash
# 创建群组（默认包含创建者）
./bin/hyphae group create --name "Dev Team" --members bob,jack

# 列出群组
./bin/hyphae group list

# 添加成员
./bin/hyphae group add-member --name "Dev Team" --user charlie

# 离开群组
./bin/hyphae group leave --name "Dev Team"
```

> **注意**：当前群聊 TUI 尚未完全实现。群聊消息需通过 `agent msg` 分别发送给各成员，relay 会广播给所有订阅者。

### 3. Agent 资料 (Agent Profile) — v0.25.0+

```bash
# 发布资料到 relay
./bin/hyphae profile publish --as alice \
  --name "Alice the SEO Expert" \
  --description "I help websites rank better" \
  --capability "seo:Search engine optimization" \
  --rate "audit:page:50" \
  --availability available

# 从 relay 发现他人资料
./bin/hyphae profile discover --npub <npub> --relay wss://relay.aastar.io

# 搜索本地缓存的资料
./bin/hyphae profile search --query "seo"
```

### 4. 后台守护进程 & 自动回复 (Daemon & Auto-reply)

```bash
# 启动后台守护进程（重试 outbox、监听新消息）
./bin/hyphae daemon --identity bob

# 启动自动回复模式
./bin/hyphae daemon --identity bob --auto-reply --notify=false
```

开启 `--auto-reply` 后，daemon 会在收到新消息时自动回复发送者：

```
[auto-reply] bob received your message: <original>
```

自动回复消息带有 `[auto-reply]` 前缀，不会被再次自动回复，避免循环。

#### 多人自动回复测试示例

```bash
# 终端 1：启动 bob 的自动回复 daemon
./bin/hyphae daemon --identity bob --auto-reply --notify=false

# 终端 2：启动 jack 的自动回复 daemon
./bin/hyphae daemon --identity jack --auto-reply --notify=false

# 终端 3（你扮演 alice）：创建群聊并发送消息
./bin/hyphae group create --name "Test Group" --members bob,jack
./bin/hyphae agent msg --from alice --to bob --content "Hey team!"
./bin/hyphae agent msg --from alice --to jack --content "Hey team!"

# 然后查看 alice 的收件箱
./bin/hyphae history inbox
# 你应该能看到 bob 和 jack 的自动回复
```

## 构建流程

```bash
./build.sh           # go build ./cmd/hyphae → bin/hyphae
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
- [`docs/M1.5_TEST_GUIDE.md`](docs/M1.5_TEST_GUIDE.md) — M1.5 新功能（花名册 register/discover、成员角色、审计日志、outbox 诊断、relay 自检）3 个核心场景手动测试指南
- [`QUICK_MANUAL_TEST.md`](QUICK_MANUAL_TEST.md) — 双人/三人协作手动测试指南（含 Profile/TUI）
- [`docs/05-acceptance-test-guide.md`](docs/05-acceptance-test-guide.md) — Alice/Bob/Charlie 验收测试约定
- [`docs/archive/`](docs/archive/) — 已被取代的历史文档（strfry 部署方案、旧重构记录等），仅供追溯，不要按其操作

## License

This project is licensed under the [Apache License, Version 2.0](LICENSE).  
Copyright 2024-present MushroomDAO Contributors.  
See [NOTICE](./NOTICE) · [TRADEMARK.md](./TRADEMARK.md) · [LICENSE-zh.md](./LICENSE-zh.md) · [TRADEMARK-zh.md](./TRADEMARK-zh.md) for details.
