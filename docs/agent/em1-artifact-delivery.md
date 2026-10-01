# E-M1 固定制品交付与独立联调

2026-10-01。状态：R1 实现中；R2/R3 待前置验收。关联 [FU-1](followups.md)、[T20](ecosystem-tasks.md) 与 [Agent24 协作](../cooperation/Agent24.md)。这项交付补齐可下载的 CLI/relay，不修改 E-M1 的执行协议或四仓验收出口。

## 固定版本

第一轮独立复测必须使用 Agent24 的生产 lock：源码 `a4aa606eb81d5c040d94c51cdf94553e646d8674`、Go `go1.26.4`、`CGO_ENABLED=0`、`-trimpath -buildvcs=false -ldflags=-buildid=`；分别构建 `./cmd/hyphae` 和 `./cmd/hyphae-relay`。不注入版本 ldflags，不用新 lock 替代旧轮次。

| 平台 | Hyphae SHA-256 | relay SHA-256 |
|---|---|---|
| darwin-arm64 | `f53c29b31d8ca5eb0124ced246bcff6610f048f18bc8dcc2de27f685dad8b221` | `a012d86e549cbeb564d5a5932c54f9b3511c2434203846537096420c89f36aef` |
| linux-amd64（lock 键 linux-x64） | `042f6200f43c095cfcec16b31a136467a39b08c810819ab3c875df5cfe0164f6` | 尚无对仓固定值，真实构建后记录 |

生产 lock 来源：[Agent24 固定文件](https://github.com/iDoris-ai/Agent24/blob/65a5c5115482522539496bc5c5cd8cdbec9a2f6e/rust/crates/agent24-comm/hyphae.lock.json)。保存的原始文件摘要为 `fbb96d21b72597029826d321d65a7d8a2428a3d9f509d079213bb7808667578a`。生成的候选 lock 与生产 lock 分开保存；新功能合入后另立联调版本。

## 三个独立交付

### R1：构建与打包工具

Luna 在独立 worktree 实现标准库 Python 工具。输入固定源码目录、完整 commit、输出目录及拟发布 tag；先校验真实 HEAD 与干净输入，验证本地 Go 精确版本，禁止下载或切换工具链。构建隔离 HOME、Go 配置、缓存及外层 workspace；输出在源码目录外。构建前后都检查源码未变。

两个归档 `hyphae-darwin-arm64.tar.gz`、`hyphae-linux-amd64.tar.gz` 在根目录包含 hyphae、hyphae-relay、LICENSE、NOTICE；tar/gzip 的时间、权限和排序固定。另输出各平台原始二进制、SHA256SUMS、artifact-manifest.json 和兼容 Agent24 schema 1 的候选 lock。manifest 记录实际源码、Go、配方和二进制/归档摘要；拟定 URL 必须明确尚未发布。已有输出拒绝覆盖。

通过条件：假 Go 黑盒测试覆盖输入、版本、失败、环境、目录、确定性与摘要；真实固定 Go 构建另行记录。假 Go 测试不证明真实二进制匹配。

### R2：真实 CI 构建与制品验证

R1 验收后另提小 PR。CI 使用固定 Go 1.26.4；构建工具与待打包的固定源码分别 checkout，关闭凭据持久化、子模块及外层 workspace。脚本从工具 checkout 执行，source-dir 指向干净的固定源码 checkout，输出位于 runner 临时目录。配置用环境变量传递，不把用户输入拼进 shell。

首轮必须比对上表三个既有二进制摘要，不能只检验新 manifest 的自洽性。Linux 用真实客户端和 relay 跑独立联调；macOS arm64 用匹配的真实 runner/机器执行相同制品复测。跨平台编译成功不能算目标平台运行通过。打包编排测试接入 CI；实际构建缺工具链、缺制品或 hash 不匹配必须失败。

先保存成功运行的 workflow artifact 与 run ID，供复核。它是 CI 交付证据，不能冒充用户要求的永久 Release 下载 URL。GitHub workflow artifact 有访问与保留期条件，见 [官方说明](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/download-workflow-artifacts?tool=webui)。

### R3：Release 发布与下载验收

R2 通过后才发布固定 tag 的 Release。发布前核对 tag 指向完整源码 SHA、对应真实 CI、manifest、SHA256SUMS 及原生产 lock；拒绝同 tag 资产覆盖。首次联调 tag 采用明确标识源码与 Go 的命名，具体名称在实际发布前确定；不更新已有空 v0.26.0 来掩盖历史缺口。

交付必须给出真实 Release 页面及两个归档的直接下载 URL，另提供 manifest、SHA256SUMS、生产 lock 与构建配方。下载到新目录，先校验归档，再核对解包后的二进制 hash、文件类型与可执行权限；不得使用已存在的 PATH 程序代替下载的制品。

使用下载制品运行第一轮独立复测工具：显式传入生产 lock，验证双向加密收发、拉取前 history 为空、原 event_id 的离线重试、重启去重及错误路径；保留实际命令、退出码、双方 commit/hash 和原始日志。支持监听的实际环境运行才计入通过。

FU-1 的安装验收还需覆盖 install.sh 已识别的 darwin/linux × amd64/arm64 全部平台，提供缺失的归档，真实执行下载安装与 smoke，并检查 Release latest 的选择。当前第一轮两个平台只解除联调制品缺口，不能直接关闭 FU-1。若要改安装器的校验或 sudo 行为，另外提独立小 PR。

## 当前边界与后续

本会话尚未发布新分支、PR 或 Release；相关 GitHub 写操作被工具执行审批层拒绝。制品请求评论正文已保存，尚未送达。恢复可用的发布路径后按 R1→R2→R3 分别审查和交付，不创建汇总 PR。

第一轮复测通过后再推进 Agent24 COMM-3 与 COMM-4a 的第二轮托管、history 可见、故障恢复及 zero-run 验收。基础 UI、T01-E、模型/模块/语音和四仓闭环继续按原门槛验收。
