# Hyphae × Agent24 发布、安装与更新路径

状态：部署设计决定（2026-10-10）  
范围：Hyphae 客户端/daemon 如何独立发布，以及如何作为 Agent24 的网络 sidecar 交付。本文不讨论远程执行协议。

## 1. 决定

Hyphae 采用**进程外网络 sidecar**形态：

- Hyphae 负责身份、发现、加密通信、历史、outbox 和 relay 协议。
- Agent24 负责 UI/CLI 入口、凭据保管、进程监管和版本兼容。
- 默认由 Agent24 安装包携带 `hyphae` 二进制；高级用户也可独立安装并使用。
- “内置”指二进制随包交付、由 `agent24d` 拉起，不是把 Go 代码链接进 Rust 进程。
- Agent24 普通用户默认不需要安装 `hyphae-relay`；relay 是独立的服务器端部署物或按需可选组件（详见第 10 节）。

默认部署布局：

```text
Agent24 发布包
├── agent24
├── agent24d
└── hyphae                   # 客户端 + daemon sidecar

~/.agent24/comm/
├── bin/hyphae-<sha16>       # 校验后安装的不可变版本副本
└── hyphae-home/.hyphae/     # 身份、relay 配置、历史、outbox
```

程序与数据分离；升级二进制不得覆盖用户身份和消息数据。

## 2. 当前已经存在的基础

Agent24 当前已有：

- `rust/crates/agent24-comm/` 通信模块；
- 编译进 Agent24 的 `hyphae.lock.json`；
- `VerifiedBinary` SHA-256 校验和版本化副本安装；
- Hyphae daemon 启停、重启和状态监管；
- Agent24 专用 HOME：`<state_dir>/comm/hyphae-home/`；
- 密码经系统凭据库保管，只通过 stdin 交给 Hyphae；
- 二进制解析顺序：
  1. `A24_HYPHAE_BIN`；
  2. `A24_SPEAKER_BIN`（旧名兼容）；
  3. `agent24d` 同目录下的 `hyphae`。

Agent24 当前的生产 lock 固定 Hyphae source SHA、Go 版本、构建配方和各平台二进制 hash。候选二进制 hash 不匹配时，通信模块必须进入 `binary_rejected`，不得执行。

当前缺口：

- Agent24 正式发布包尚未携带 `hyphae`；对应 Agent24 `docs/Deployment/TASKS.md` 的 DEP-C9。
- Hyphae 虽已有 GitHub Release 制品，但仓库尚缺少正式的 tag-triggered release workflow。当前构建器只产 macOS arm64、Linux amd64，另两平台尚未交付；当前 tar.gz 还把客户端、relay、LICENSE、NOTICE 打在同一包中。
- Hyphae 现有 `install.sh` 下载后不校验 `SHA256SUMS`，不能作为最终安全安装器；仅校验 Release 同包提供的 checksum 也只能证明包内一致性，不能证明发布者身份。
- `VerifiedBinary` 当前用 `O_EXCL` 直接写最终文件，而不是临时文件 + fsync + rename；中断可能留下之后会被 hash 校验拒绝的半成品。原子安装仍是目标态。
- 当前没有 Agent24 自动下载 Hyphae、启动后健康回滚或通信 UI 的完整实现；不能把这些设想写成已交付能力。

## 3. Hyphae 独立发布

对 Hyphae 打语义版本 tag，例如 `v0.27.0`。正式 CI 必须在原生或明确受支持的 runner 上生成：

```text
hyphae-darwin-arm64
hyphae-darwin-x64
hyphae-linux-amd64
hyphae-linux-arm64
hyphae-relay-<platform>       # 服务器端，单独分发
SHA256SUMS
artifact-manifest.json
hyphae.lock.json
```

这是目标产物布局。当前 `scripts/build_release_artifacts.py` 只生成 macOS arm64 和 Linux amd64，`hyphae.lock.json` 的 `darwin-x64`、`linux-arm64` 仍为空；当前 tar.gz 同时包含 `hyphae` 和 `hyphae-relay`。补齐四平台、客户端/relay 分包都是待实现的发布工作。

发布门：

1. source SHA、Go toolchain、构建配方全部固定；
2. 每个平台记录 SHA-256；
3. 默认测试、race、integration、真实 relay smoke 全部通过；
4. Release 下载后的制品再次校验并执行 smoke；
5. 安装器用独立固定的预期 hash，或验证带明确信任根的发布签名/证明，再原子替换文件；Release 同包的 `SHA256SUMS` 只作传输完整性检查，不能独立充当来源认证；
6. Release Notes 声明最低系统版本、数据迁移和兼容性变化。

Hyphae Release 是独立用户可消费的正式产品，也是 Agent24 升级兼容锁的候选输入；发布 Hyphae 不等于 Agent24 已接受该版本。

## 4. 三种安装方式

### 4.1 Agent24 默认安装（推荐）

Agent24 的 CLI 包和桌面安装包携带精确锁定的 `hyphae`：

```text
agent24-<version>-<os>-<arch>.tar.gz
└── agent24 + agent24d + hyphae
```

目标流程是首次启动通信模块时，`agent24d`：

1. 找到同目录 `hyphae`；
2. 按内置 lock 校验 SHA-256；
3. 经临时文件 + fsync + **不覆盖已有目标**的原子提交，安装到 `~/.agent24/comm/bin/hyphae-<sha16>`，权限设为仅当前用户可执行；目标已存在时必须重新计算 hash：一致才复用，不一致则拒绝，不能用 rename 覆盖可疑文件；
4. 以 `~/.agent24/comm/hyphae-home/` 为专用 HOME；
5. 启动并监管 `hyphae daemon --notify=false --auto-reply=false ...`。

当前实现已经完成查找、hash 校验、版本化副本和 daemon 监管，但第 3 步仍直接写最终文件，需要补成原子安装。最终用户不需要接触 Hyphae 安装、PATH 或 launchd/systemd。

### 4.2 Hyphae 独立安装

独立聊天/TUI 用户从 Hyphae Release 安装到 `/usr/local/bin/hyphae` 或用户目录，并使用 `~/.hyphae/`：

```bash
hyphae identity create --nickname alice --default --password-prompt
hyphae relay set --relay wss://example
hyphae contact add --nickname bob --npub npub1...

# 终端 1（或注册为后台服务）
hyphae daemon --identity alice

# 终端 2
hyphae tui chat --with bob
```

`--password-prompt` 是安全基线；省略它会创建未加密 keystore。聊天前必须先添加联系人；daemon 是长驻前台进程，应在另一终端或后台服务中运行。`tui` 本身不是聊天入口，必须使用 `tui chat --with <contact>`。

最终安装脚本必须支持固定版本，验证独立固定的预期 hash 或带明确信任根的发布签名，并同时校验下载完整性；只核对 Release 同包的 checksum 不足以认证来源。`latest` 只用于交互式便利，不用于 Agent24 生产接线。

### 4.3 Agent24 使用外部 Hyphae

高级用户可显式设置绝对路径：

```bash
# 仅在 agent24d 尚未运行时，这个环境变量才会传入新进程
A24_HYPHAE_BIN=/usr/local/bin/hyphae agent24 daemon start
```

如果 `agent24d` 已在运行，必须先停止并用新环境重启；服务模式还必须通过受支持的服务配置更新其环境快照，单纯在当前 shell `export` 不会改变既有 daemon。Agent24 仍按自身 lock 校验 hash。外部 Hyphae 若已升级到 Agent24 尚未接受的版本，必须拒绝，而不是跳过校验。

未来可以增加“启用通信时按需下载”，但只能下载 Agent24 lock 指向的精确资产并校验 hash/签名，不得跟随 Hyphae `latest`。默认交付仍以随 Agent24 打包为先；按需下载不是 DEP-C9 的前置条件。

## 5. Agent24 如何接受一个新 Hyphae 版本

Hyphae 发布后，在 Agent24 单独提交兼容升级 PR：

1. 更新 `rust/crates/agent24-comm/hyphae.lock.json` 的 source SHA、工具链、配方和平台 hash；
2. CI 从固定 Hyphae SHA 重建并逐字节核对 lock；
3. 执行 Agent24 × Hyphae 联调：身份、联系人、relay、加密发送、history、outbox、离线补收和 daemon 重启；
4. 验证普通通信保持 zero-run，不启动 Agent24 任务；
5. 在干净机器验证安装包内置二进制；
6. 通过后才进入下一版 Agent24 Release。

因此：

> Hyphae Release 是候选发布；Agent24 lock 升级是兼容性晋升。

不得在 Agent24 启动时自动选择“最新 Hyphae”。

## 6. 目标更新与回滚机制

以下是必须交付的目标流程。当前已有版本化二进制和 daemon 监管，但**尚无**“新版本健康检查失败后自动恢复旧二进制”的完整实现。

### 6.1 Agent24 集成模式

普通用户只更新 Agent24。新版 Agent24 携带新的 lock 与 Hyphae 二进制：

1. 停止旧 Hyphae daemon；
2. 校验并安装新的 hash 命名副本；
3. 数据目录保持不变；
4. 用新二进制启动并做健康检查；
5. 启动失败则停止新进程并报告，不循环破坏数据。

旧二进制可以暂留用于诊断和回滚，稳定后再按版本保留策略清理。涉及数据库 schema 迁移时，Release 必须明确旧版本是否可读；不可逆迁移前必须备份，不能只靠切换旧二进制声称可回滚。

当前 embedded lock 意味着 Hyphae 升级随 Agent24 发布。若未来要在不升级 Agent24 的情况下更新 sidecar，需要另行设计受签名的兼容 manifest、回滚和 staged rollout；当前不实现。

### 6.2 Hyphae 独立模式

独立安装器的更新顺序：下载固定版本 → 校验 → 停 daemon → 原子替换 → 启动 → 健康检查。失败时恢复旧二进制；保留 `~/.hyphae/`。

若这个独立二进制同时供 Agent24 使用，用户不能超前升级到 lock 不接受的版本。

## 7. 用户使用流程

### Agent24 用户（目标产品流程）

1. 安装 Agent24，不另装 Hyphae。
2. 首次进入“通信”，创建身份或显式导入现有 `~/.hyphae`。
3. Agent24 把口令放入系统凭据库。
4. 用户配置 relay，并显式完成第一次 daemon 启动；第一次启动成功后才持久化 autostart，后续由 Agent24 自动启动。
5. 用户通过 Agent24 UI 或 `agent24 comm ...` 收发消息。
6. 普通消息只进入通信层，不触发远程执行或 Agent24 run。

当前 `agent24 comm` 后端和 daemon autostart 机制已经存在，但通信 UI 仍在 T21/T22/COMM-6/COMM-7 交付范围内；配置 relay 本身不会自动完成第一次启动。

### 独立 Hyphae 用户

安装 Hyphae 后直接使用其 CLI/TUI/daemon；可选择注册系统后台服务。独立数据默认在 `~/.hyphae/`，不会自动与 Agent24 专用 HOME 混用。导入必须由用户明确确认并复制，不能让两个 daemon 同时写同一目录。

### Relay 运营者

单独安装 `hyphae-relay` 或未来的容器镜像。普通客户端和 Agent24 安装包不携带 relay 服务端。

## 8. 当前二进制体积基线

在 Hyphae `be6d0a709a71cc06418cd715e76f2eb03c8d60c8`（后续 `a0fc80a` 仅合并文档，生产代码相同）使用当前锁定配方实测：

```text
GOTOOLCHAIN=go1.26.4 CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> \
  go build -trimpath -buildvcs=false -ldflags='-buildid=' \
  -o hyphae ./cmd/hyphae
```

| 平台 | 原始二进制 | gzip -9 |
|---|---:|---:|
| macOS arm64 | 24,330,834 bytes（23.20 MiB） | 13,069,991 bytes（12.46 MiB） |
| Linux x64 | 25,330,591 bytes（24.16 MiB） | 13,536,315 bytes（12.91 MiB） |

当前正式配方没有使用 `-s -w`。仅作体积对照，macOS arm64 加 `-s -w` 后为 16,885,250 bytes（16.10 MiB），gzip 后 6,845,055 bytes（6.53 MiB）。是否切换必须单独验证崩溃诊断、符号信息、可复现 hash 和跨仓 lock；不能直接改变现有生产配方。

结论：Agent24 默认内置 Hyphae 大约增加 **13 MB 下载体积、24 MB 解压体积**（单一平台，只含客户端；不含 relay）。

## 9. 落地顺序

1. Hyphae 增加正式 tag Release workflow；安装器加入来源认证（可信签名或独立固定 hash）及下载完整性校验，不能只信任 Release 同包 checksum。
2. 发布一个经过验收的 Hyphae 版本。
3. Agent24 更新 `hyphae.lock.json` 并跑跨仓兼容门。
4. 完成 Agent24 DEP-C9：CLI 和桌面发布包携带 `hyphae`。
5. 在干净 macOS/Linux 机器完成首次安装、通信、升级和回滚验收。
6. 默认随包交付 client 后，按第 10 节实现经用户同意的可选本机 relay、离线包和服务器发行；按需组件不改变标准包不含 relay 的边界。

## 10. Relay 产品化交付、网络入网与双仓规范契约

为了避免 Relay 部署路径仅面向专家或沦为运维专有操作，本节作为规范性定义，明确 Relay 架构定位、Agent24 普通用户入网引导模型、跨仓交付契约及实施依赖。

### 10.1 权限与攻击面隔离：为何默认不自启 Relay

标准 Agent24 发布包内置 Hyphae 客户端，但**绝不在每台用户机器上静默启动 Relay 监听服务**。这并非仅仅为了节省安装包与运行体积，核心原因在于**权威边界（Authority）与攻击暴露面（Attack Surface）的本质差异**：

1. **客户端行为边界**：Hyphae 客户端仅向外部或指定私有节点发起出站长连接（outbound-only），不开放任何入站网络监听端口，不会在宿主机上暴露服务攻击面；
2. **Relay 服务端职责**：Relay 作为服务端必须监听网络入站连接（inbound listener），并最终承担连接生命周期、数据持久化、访问控制、TLS 终止或受信任反向代理、限流及网络暴露面防护；这些是公开/LAN 部署的**目标责任**，不是当前实现声明；
3. **架构隔离结论**：将客户端与服务端拆分，是确保普通终端桌面安全的第一道防线。不能将服务端的暴露风险与管理负担隐式强加给所有终端用户。

当前 `cmd/hyphae-relay` 直接提供 `ws://` 服务，尚不能据此宣称已交付 TLS、访问控制或生产级限流。跨机器 LAN/公网使用必须先经过单独评审的 TLS、身份/访问控制和暴露方案；不满足时只允许 loopback。

### 10.2 Agent24 入网引导三选一（去专家化术语）

终端用户**不需要知晓“Relay”等协议术语，也不得被要求手动前往 GitHub 寻找或下载任何制品**。Agent24 的通信入网向导必须以通俗易懂的自然语言向用户呈现三项清晰选择：

1. **加入现有网络**（默认 / 推荐）：
   - 适用场景：用户已收到团队、朋友或社区的邀请码或节点地址；
   - 行为：仅使用 Agent24 默认内置的 Hyphae 客户端建立出站连接；
   - 约束：**本机不安装、不下发、也不启动任何 Relay 组件**。
2. **在本机创建私有网络**（按需可选）：
   - 默认场景：个人单机测试或本机私有节点；首次启用仅允许 loopback，界面明确说明“目前只有这台电脑能连接”；
   - 行为：由 Agent24 提供显式的组件下载提示，在**获得用户明确授权同意（Explicit Consent）**后按需下载受控 Relay 组件；
   - 约束：受严格本地沙箱与监管约束（见 10.3 节）。若要扩展到局域网协作，必须先具备单独评审的 TLS/访问控制方案，再二次确认网卡、地址、端口和可访问人群；不能把 loopback 冒充团队网络。
3. **在服务器部署团队/公开节点**（运维与团队节点）：
   - 适用场景：需要长驻、多团队共享或公网访问的独立基础设施；
   - 行为：向用户展示面向 Linux/容器（Docker）的服务器端部署指南与配置模板；
   - 约束：走独立的服务端运维路径，不混入桌面客户端生命周期。

### 10.3 本机私有网络 Relay 的受限安装与安全基线（目标态）

当用户明确授权并在本机创建私有网络时，Agent24 必须遵循以下目标安全基线（当前尚无此能力，严禁将设想表述为既有功能）：

1. **精确制品与来源鉴权**：
   - Agent24 仅下载与内置 lock 清单哈希严格匹配的特定版本 Relay 制品，绝不拉取 `latest`；
   - 必选路径是由已验证的 Agent24 发行内置、经过 lock-promotion 评审的独立固定 SHA-256；若声明使用发布签名/provenance，还必须按预先固化的信任根验证，不能随资产接受新根；
   - 未建立签名根时只能声明“独立 pin 已验证”，不得宣称“签名已验证”；Release 同包附带的 `SHA256SUMS` 仅用于传输完整性核对，不可作为来源可信凭据。
2. **原子安全写入**：
   - 采用临时文件写入 + fsync + 冲突检测原子安装；
   - 若目标路径已存在同名文件但哈希不匹配，必须阻断并报错，**严禁静默覆盖已有文件**，防止损坏或覆盖篡改文件。
3. **状态与存储隔离**：
   - 使用与客户端完全隔离的专用数据目录（目标：`<state_dir>/comm/relay/<node-id>/`），避免两个进程冲突争用同一存储。
4. **生命周期监管**：
   - 由 `agent24d` 纳入子进程监管，提供心跳探测、健康检查、日志轮转与受控优雅停机。
5. **网络暴露负面清单（绝对禁止项）**：
   - **默认仅绑定回环地址**：监听接口必须默认为 `127.0.0.1`（启用 IPv6 时为 `::1`），此状态只代表本机可连接；
   - **严禁公网或 LAN 静默监听**：不得在没有单独评审的 TLS/访问控制方案及二次确认时扩大监听；不得直接监听 `0.0.0.0`；
   - **严禁 UPnP 打洞**：严禁自动向路由器申请 UPnP 端口转发；
   - **严禁修改系统防火墙**：严禁静默申请管理员权限篡改系统防火墙规则；
   - **严禁静默注册域名/DDNS**：不得自动绑定公共域名或公网解析；
   - **严禁绕过 TLS 约束**：私有本地连接使用标准无证书回环或受控凭据，公网连接必须完整验证 TLS，绝不自降安全级别；
   - **严禁隐式后台安装**：禁止在用户不知情或未显式授权的情况下后台下载并运行 Relay。

### 10.4 四种交付与打包变体

为覆盖不同场景与受众，Hyphae 与 Agent24 共同定义四种打包与分发形式：

| 变体形态 | 包含组件 | 目标受众与场景 | 交付机制 |
|---|---|---|---|
| **标准 Agent24 发布包**（Standard） | `agent24` + `agent24d` + Hyphae Client | 绝大多数普通用户与桌面端用户（默认推荐） | 开箱即用，随 Agent24 发布包直接分发，无 Relay 负担 |
| **按需可选 Relay 组件**（Optional Component） | `hyphae-relay` 单二进制（精确版本） | 需要在本机创建私有网络的用户 | 由 Agent24 UI/CLI 经用户授权后按需下载并校验安装 |
| **离线/自托管完整包**（Offline Bundle） | Agent24 全套 + 预锁定的 Client 与 Relay | 受限内网、强合规隔离、无外网连接环境 | 单一全量离线归档，预置独立 pin/lock 与适用的签名或 provenance 材料 |
| **服务器/容器运维制品**（Server Artifact） | `hyphae-relay` 二进制、OCI 容器镜像、systemd unit | 团队管理员、节点运营者、公网服务部署 | Hyphae Release 独立分发与容器镜像仓库推送 |

### 10.5 独立的锁清单与版本生命周期

1. **依赖清单解耦**：Hyphae 客户端与 Relay 服务端采用独立的锁定清单（如 `hyphae.lock.json` 与 `hyphae-relay.lock.json`），分别固定各自的源 SHA、Go 工具链、编译参数与各平台二进制 SHA-256；
2. **严禁跟随最新（Never follow `latest`）**：Agent24 无论分发客户端还是按需拉取 Relay，必须只消费其静态编译绑定的精确锁清单版本，绝不在运行时解析或跟随 Hyphae 的 `latest` 标签；
3. **独立构建、组合晋升**：客户端与服务端的补丁可用独立 PR 和 lock 构建，但 Agent24 只能运行经过组合兼容晋升的版本；不能因为单个组件已发布就绕过组合验证。

### 10.6 跨仓库规范契约与协作流程

Hyphae 与 Agent24 之间遵循严格的跨仓库契约约束：

1. **持久规范文档链接**：
   - Agent24 候选契约 PR：[`iDoris-ai/Agent24#881`](https://github.com/iDoris-ai/Agent24/pull/881)；
   - 合并后的稳定文档地址：[`docs/Deployment/HYPHAE-NETWORK-DELIVERY.md`](https://github.com/iDoris-ai/Agent24/blob/main/docs/Deployment/HYPHAE-NETWORK-DELIVERY.md)；
   - *状态声明：#881 与本 Hyphae 补充 PR 都合并只建立基线；双方还须用互链 follow-up PR 冻结健康阈值和 schema/回滚矩阵，之后 `DEP-HN0` 才关闭。在 #881 合并前 stable main 链接可能尚未出现。*
2. **候选与晋升模型（Candidate & Promotion）**：
   - **Hyphae 侧**：负责按固定配方构建、记录来源/provenance 并发布候选制品；若已建立签名信任根则同时签名，否则不得宣称已验签；
   - **Agent24 侧**：在 CI 跨仓门禁中对候选制品执行端到端契约校验，核准后通过 PR 更新 lock 文件，正式晋升（Promote）为受支持版本。
3. **不兼容阻断与证据要求**：
   - Agent24 发现不兼容时保留旧 lock，并在 Hyphae 提交关联 issue 或修复 PR，同时从 Agent24 lock-promotion PR 反链；涉及契约变更时两仓都必须提交互链 PR；
   - 缺陷与阻断报告必须包含精确 Git Commit SHA、构建制品哈希、预期/实际、可复现命令及脱敏失败证据；不得把私钥、口令、邀请凭据或用户正文放入日志。
4. **双向受控演进**：
   - 涉及网络协议、分发结构、CLI/RPC 接口或安全基线的任何变更，必须在 Hyphae 与 Agent24 提双向关联 PR 并同步审查；
   - **严禁任何一方单方面静默放宽行为约束、安全策略或扩大功能作用域（Neither side silently broadens behavior）**。
5. **Zero-Run 通信边界**：
   - 所有通信层消息收发严格保持 Zero-Run 语义，仅作为网络消息传输与存储，绝不赋予远程代码执行（Remote Execution）或未经确认的系统调用作用域。

### 10.7 双仓部署里程碑、任务映射与依赖 DAG

本里程碑只使用一套跨仓任务编号，以 Agent24 `docs/Deployment/TASKS.md` 的 `DEP-HN` 系列及既有 `DEP-C8/C9` 为执行台账；Hyphae 不再维护一套顺序不同的 D1–D8。双方 PR 合并后，映射如下：

| 共享任务 | 归属 | Hyphae 责任 / 可观察出口 |
|---|---|---|
| `DEP-HN0` | 双仓 | 两边互链契约 PR 合并先建立 SKU、独立 pin/签名根和责任基线；随后用互链 follow-up PR 冻结健康阈值与 schema/回滚矩阵，才将 HN0 标 DONE。当前仍是提案，不代表产品交付。 |
| `DEP-HN1` | Hyphae | M1 发布门关闭后，交付 tag Release、四平台 client/relay 分包、独立 manifest/lock、安全安装器、Linux 双架构容器和持久卷指引；真实下载及 relay smoke 通过。 |
| `DEP-C8` | Agent24 | 对 Hyphae 候选做 lock-promotion：固定双方 SHA、重建匹配下载资产、验证 client/relay 组合、daemon lock、通信与 zero-run；不兼容时反向提交带证据的 Hyphae issue/PR。 |
| `DEP-HN2` | Agent24 | 实现 client/relay 共用的安全安装事务：staging、fsync、不覆盖原子提交、已装副本重验；满盘、篡改、并发、崩溃和恶意归档都不执行半成品。 |
| `DEP-C9` | Agent24 | 标准 CLI/桌面四平台包携带精确 client，不携带 relay；干净环境不依赖源码、Go、PATH 中全局 Hyphae。 |
| `DEP-HN3` | Agent24 | 可选 relay 下载、离线导入、plan/apply API、持久事务和同意绑定；拒绝任意 URL、`latest`、无 token、无同意及坏 pin。 |
| `DEP-HN4` | Agent24 | relay 独立数据与 loopback 首启、生命周期监管、组合更新、健康检查、授权旧组合回滚和 schema/备份门。 |
| `DEP-HN5` | Agent24 | 把三个非术语入口及状态/确认接入 COMM-6/7 UI/CLI；不复制通信实现，不把 ACK/进程存活冒充送达/健康。 |
| `DEP-HN6` | 双仓 | 组装离线包；用真实 Release 完成标准/可选/离线及服务器路径、干净 macOS/Linux、负例、升级/回滚和最终归档。 |

统一依赖 DAG：

```text
DEP-HN0（现在只冻结双仓契约）
  → Hyphae M1 发布门
  → DEP-HN1
  → DEP-C8
  → DEP-HN2
  → { DEP-C9, DEP-HN3 → DEP-HN4 }
  → DEP-HN5
  → DEP-HN6
```

`DEP-HN6` 还直接依赖 `DEP-HN1`、`DEP-C9` 与 `DEP-HN4`。`COMM-6/7` 尚未提供的通信 UI 必须在 `DEP-HN5` 补齐或对接，不能因本文出现就记为已完成。

**启动门禁**：Hyphae M1 仍是当前唯一 P0。现在允许合并 `DEP-HN0` 文档与任务台账；`DEP-HN1` 及其全部实现下游必须等待 M1 最终发布验收关闭。本里程碑不是 Hyphae M2，也不包含远程执行。

### 10.8 统一交付完成定义（Definition of Done）

文档或规划 PR 合并只表示契约冻结，不能把部署里程碑标为完成。关闭 `DEP-HN` 必须同时满足：

1. Hyphae 与 Agent24 所需契约、实现和修复 PR 均合并；本里程碑每个 PR 的最终 head 都取得外部 PR-Daemon 精确 head APPROVE，required CI 和本地门禁通过；Release 由人发起审查并经 jason 验收，不因 PR 合并自动发布；
2. client/relay 四平台拆分资产发布到 Hyphae GitHub Release；服务器容器按 digest 发布到双方冻结的 OCI registry；Agent24 Release 发布标准包与离线包，所有资产记录来源、完整 hash 和兼容组合；
3. Agent24 标准内置 client、经同意的可选本机 relay、离线导入及服务器部署指引均实现；普通用户无需理解 relay 或手工搜索 GitHub；
4. 至少 macOS arm64 干净笔记本与 Linux x64 干净 VM 完成 CLI/桌面全流程，其余支持平台完成准确标注环境的安装/CLI smoke；普通通信的 run/model/module 计数保持 zero-run；
5. 负例证明首次打开、加入已有网络及拒绝下载时均无本机监听；本机节点默认只绑定 loopback；无授权不得扩大到 LAN/公网，不调用 UPnP、不修改防火墙、不绕过 TLS；
6. client/relay 独立更新、组合更新、损坏候选、启动超时、磁盘满和不可逆 schema 场景完成升级/回滚演练，身份、history、outbox 和 relay 数据无损；
7. 双仓 PR、合并 SHA、release/tag/资产 URL、完整摘要、平台、命令/退出码、CI run、脱敏监听/UI 证据和回滚记录归档；日志不得包含私钥、口令、邀请凭据或用户正文。

LAN/公网暴露、TLS 与访问控制方案在启用前还必须经过独立安全设计评审；若该评审未通过，对应模式保持禁用，不能用 loopback 验收冒充团队/公共节点完成。双方任何协议、manifest、包布局、信任根或责任边界变化仍需互链 PR，不能单方放宽。

### 10.9 条件性工期与目标窗口

统一估算采用 Agent24 执行台账的完整范围：约 **20–30 人日**，另预留外部评审与发布等待 **2–3 个工作日**。在 Hyphae、Agent24 各有一名主要实现者并能并行获得 UI/平台支持时，依赖链预计为 **15–20 个工作日**；这比只计算 Hyphae 打包工作的 8–12 日更完整，包含 Agent24 安装器、UI、生命周期、回滚和双仓干净机器验收。

- `DEP-HN0` 文档冻结：Agent24 #881 与本 Hyphae 补充 PR 合并只建立基线；健康阈值和 schema/回滚矩阵 follow-up 合并后才完成；
- 若 Hyphae M1 在 **2026-10-16** 前关闭，最早 **2026-10-19** 开始实现；
- 条件目标完成窗口：**2026-11-06 至 2026-11-13**；
- 这是估算而非承诺。M1、可信资产、COMM UI、签名条件、跨仓评审或真机资源每延后一天，目标窗口相应顺延；不得为日期删除安全或验收门。
