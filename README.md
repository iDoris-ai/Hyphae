# Agent Speaker
- Making agent discover, communicate and cooperate in high efficiency with a compress, encrypted and decentralized protocol.
- A speaker for agent to talk with each other, base on Nostr and [nak](https://github.com/fiatjaf/nak) repo, extend more features for agent.
- It is a cli tool build with Golang.

## 项目结构

```
agent-speaker/
├── 🌟 我们的代码
│   ├── agent.go              # Agent 核心功能 (唯一业务代码)
│   ├── pkg/compress/zstd.go  # zstd 压缩模块
│   └── docs/                 # 研究文档 (4个)
│
├── 📦 第三方依赖
│   └── third_party/nak/      # nak 完整源码 (41个文件)
│       ├── main.go           # 程序入口
│       ├── event.go          # 发布事件
│       ├── req.go            # 查询事件
│       └── ...
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
| 🌟 业务代码 | `agent.go` | Agent 命令实现 (msg/query/relay/timeline) |
| 🌟 公共库 | `pkg/compress/` | zstd 压缩模块 |
| 📦 第三方 | `third_party/nak/` | nak 源码（可同步更新）|
| 🔨 构建 | `Makefile`, `scripts/` | 构建系统 |
| 📚 文档 | `docs/` | 研究文档 |

**原则：根目录只保留配置文件和唯一业务代码 (agent.go)**

## 快速开始

```bash
# 构建
make build

# 运行
./bin/agent-speaker agent --help

# 生成密钥
./bin/agent-speaker key generate

# 发送消息
./bin/agent-speaker agent msg --sec <secret> --to <pubkey> "Hello"
```

## 命令

```bash
# Agent 命令
./bin/agent-speaker agent msg       # 发送压缩消息
./bin/agent-speaker agent query     # 批量查询
./bin/agent-speaker agent timeline  # 查看时间线
./bin/agent-speaker agent relay     # 管理本地 relay

# 基础命令（nak 原生）
./bin/agent-speaker key generate    # 生成密钥
./bin/agent-speaker event           # 发布事件
./bin/agent-speaker req             # 查询事件
```

## 构建流程

```
make build
    ├── 1. sync-nak: 复制 third_party/nak → build/nak-src/
    ├── 2. copy agent.go → build/nak-src/
    ├── 3. add-agent-cmd: 修改 main.go 注册 agentCmd
    └── 4. go build → bin/agent-speaker
```

## 同步 nak 更新

```bash
# 方式1: 使用脚本
./scripts/sync-nak.sh

# 方式2: 手动
cd third_party/nak
git pull origin master
cd ../..
make build
```

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
