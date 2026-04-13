# Go 项目结构最佳实践

## 参考标准

- [golang-standards/project-layout](https://github.com/golang-standards/project-layout)
- [Go Modules 最佳实践](https://go.dev/doc/modules/managing-dependencies)
- [Go Plugin 系统](https://pkg.go.dev/plugin)

---

## 推荐的项目结构

```
agent-speaker/
├── api/                    # API 定义（Protobuf/OpenAPI）
│   └── v1/
│       └── types.proto
│
├── cmd/                    # 应用程序入口
│   └── agent-speaker/      # 主应用
│       └── main.go         # 唯一包含 main 函数的地方
│
├── internal/               # 私有代码（不允许外部导入）
│   ├── identity/           # 身份管理模块
│   │   ├── keystore.go
│   │   ├── store.go
│   │   └── keystore_test.go
│   │
│   ├── messaging/          # 消息模块
│   │   ├── encrypt.go
│   │   ├── outbox.go
│   │   └── message.go
│   │
│   ├── nostr/              # Nostr 协议封装
│   │   ├── client.go
│   │   └── types.go
│   │
│   └── daemon/             # 后台服务
│       ├── daemon.go
│       └── notifier.go
│
├── pkg/                    # 公共库（可被外部导入）
│   ├── crypto/             # 加密工具
│   │   ├── nip44.go
│   │   └── compress.go
│   │
│   └── types/              # 共享类型
│       └── types.go
│
├── plugins/                # 插件目录
│   ├── encryptor/          # 加密插件接口
│   │   └── interface.go
│   │
│   └── storage/            # 存储插件接口
│       └── interface.go
│
├── web/                    # Web UI（可选）
│   ├── static/
│   └── templates/
│
├── configs/                # 配置文件示例
│   └── config.yaml
│
├── scripts/                # 构建脚本
│   ├── build.sh
│   └── install.sh
│
├── docs/                   # 文档
│   └── ...
│
├── test/                   # 测试数据和工具
│   └── integration/
│
├── go.mod                  # 模块定义
├── go.sum                  # 依赖锁定
├── Makefile                # 构建脚本
├── README.md               # 项目说明
└── LICENSE                 # 许可证
```

---

## 关键设计原则

### 1. 代码组织

| 目录 | 用途 | 导入限制 |
|------|------|----------|
| `cmd/` | 应用程序入口 | 可以导入所有其他包 |
| `internal/` | 私有实现 | 不能被外部项目导入 |
| `pkg/` | 公共库 | 可以被外部项目导入 |
| `api/` | API 契约 | 前后端共享 |

### 2. 模块化设计

```go
// internal/identity/identity.go
package identity

type Manager struct {
    store *KeyStore
}

func NewManager(configDir string) (*Manager, error) {
    // ...
}

func (m *Manager) Create(nickname string) (*Identity, error) {
    // ...
}

// 只暴露必要的接口
```

### 3. 插件系统

```go
// plugins/encryptor/interface.go
package encryptor

type Plugin interface {
    Name() string
    Encrypt(plaintext string, key []byte) (string, error)
    Decrypt(ciphertext string, key []byte) (string, error)
}

// 注册机制
var registry = make(map[string]Plugin)

func Register(name string, p Plugin) {
    registry[name] = p
}

func Get(name string) (Plugin, bool) {
    p, ok := registry[name]
    return p, ok
}
```

### 4. 可嵌入性

```go
// pkg/client/client.go
package client

// Client 可以被其他项目嵌入使用
type Client struct {
    identity *identity.Manager
    messenger *messaging.Client
}

func New(opts ...Option) (*Client, error) {
    // ...
}

func (c *Client) SendMessage(to, content string) error {
    // ...
}

// 其他项目可以这样使用：
// import "github.com/AuraAIHQ/agent-speaker/pkg/client"
// cli, _ := client.New()
// cli.SendMessage("bob", "hello")
```

---

## 当前问题分析

### ❌ 当前结构

```
agent-mouth-cli/
├── agent.go          # 业务代码在根目录
├── daemon.go         # 分散的模块
├── encryption.go     # 没有模块划分
├── identity_cmd.go   # 命令和业务混在一起
├── keystore.go
├── message_store.go
├── outbox.go
├── ... (20+ 文件在根目录)
└── main.go           # 入口
```

### ⚠️ 问题

1. **命名空间混乱** - 所有包都在 main，没有清晰的模块边界
2. **无法被导入** - 其他项目无法使用我们的代码
3. **难以测试** - 没有清晰的接口，单元测试困难
4. **无法扩展** - 没有插件机制，新功能只能硬编码
5. **版本管理困难** - 所有代码耦合在一起

---

## 重构建议

### Phase 1: 基础结构迁移

```bash
# 1. 创建标准目录结构
mkdir -p cmd/agent-speaker
mkdir -p internal/{identity,messaging,nostr,daemon}
mkdir -p pkg/{crypto,types}
mkdir -p plugins/{encryptor,storage}

# 2. 移动 main.go
cp main.go cmd/agent-speaker/

# 3. 按功能分组移动文件
# identity_*.go -> internal/identity/
# encryption.go -> internal/crypto/ or pkg/crypto/
# daemon.go -> internal/daemon/
# messaging相关 -> internal/messaging/
```

### Phase 2: 提取公共接口

```go
// pkg/types/types.go
package types

type Identity struct {
    Nickname string
    Npub     string
    // 不包含私钥，安全
}

type Message struct {
    ID        string
    From      Identity
    To        Identity
    Content   string
    Encrypted bool
    Timestamp int64
}
```

### Phase 3: 插件化改造

```go
// 将加密算法抽象为插件
// plugins/encryptor/nip44/nip44.go
package nip44

import "github.com/AuraAIHQ/agent-speaker/plugins/encryptor"

type NIP44Plugin struct{}

func (p *NIP44Plugin) Name() string { return "nip44" }
func (p *NIP44Plugin) Encrypt(plaintext string, key []byte) (string, error) {
    // 实现
}

func init() {
    encryptor.Register("nip44", &NIP44Plugin{})
}
```

### Phase 4: 提供 SDK

```go
// pkg/client/client.go
// 让其他项目可以嵌入使用

package client

type Client struct {
    // ...
}

func (c *Client) SendDirectMessage(recipientNpub, content string) error
func (c *Client) ListenMessages(handler MessageHandler) error
func (c *Client) GetHistory(contactNpub string) ([]Message, error)
```

---

## 版本控制和发布

### Semantic Versioning

```
v0.1.0  # 初始版本
v0.2.0  # 新增加密功能
v0.3.0  # 新增 daemon
v1.0.0  # 稳定版本
```

### Go Modules

```go
// go.mod
module github.com/AuraAIHQ/agent-speaker

go 1.21

require (
    fiatjaf.com/nostr v0.0.0-...
    github.com/klauspost/compress v1.18.0
    // ...
)
```

### 发布流程

```bash
# 1. 打标签
git tag -a v0.3.0 -m "Release v0.3.0"
git push origin v0.3.0

# 2. Go proxy 会自动缓存
# 其他项目可以通过 go get 使用
go get github.com/AuraAIHQ/agent-speaker/pkg/client@v0.3.0
```

---

## 总结

| 方面 | 当前状态 | 目标状态 |
|------|----------|----------|
| 代码组织 | 根目录混乱 | 按功能分模块 (internal/pkg) |
| 可导入性 | ❌ 无法被导入 | ✅ 提供 pkg/client SDK |
| 插件系统 | ❌ 硬编码 | ✅ 注册机制 |
| 测试 | 困难 | 单元测试友好 |
| 版本管理 | 无 | SemVer + Go Modules |

**建议：先进行 Phase 1 的结构迁移，这是基础。**
