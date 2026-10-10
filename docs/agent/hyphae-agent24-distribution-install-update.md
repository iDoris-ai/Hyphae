# Hyphae × Agent24 发布、安装与更新路径

状态：部署设计决定（2026-10-10）  
范围：Hyphae 客户端/daemon 如何独立发布，以及如何作为 Agent24 的网络 sidecar 交付。本文不讨论远程执行协议。

## 1. 决定

Hyphae 采用**进程外网络 sidecar**形态：

- Hyphae 负责身份、发现、加密通信、历史、outbox 和 relay 协议。
- Agent24 负责 UI/CLI 入口、凭据保管、进程监管和版本兼容。
- 默认由 Agent24 安装包携带 `hyphae` 二进制；高级用户也可独立安装并使用。
- “内置”指二进制随包交付、由 `agent24d` 拉起，不是把 Go 代码链接进 Rust 进程。
- Agent24 普通用户不需要安装 `hyphae-relay`；relay 是独立的服务器端部署物。

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
6. 有明确体积或轻量安装需求时，再决定是否增加按需下载；不影响默认随包交付。
