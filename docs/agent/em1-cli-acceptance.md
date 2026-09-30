# E-M1-A：Hyphae CLI 验收记录

最新结果：2026-09-30 固定 main `1948aadc` 的 Hyphae 侧基础 CLI 已收口通过，见下方“固定 main 收口验收”；Agent24 CLI/UI 与完整 E-M1 仍未通过。以下保留各历史提交当时的证据，不用历史组合结果替代新版本验收。

2026-09-29，主代理独立验收。**Hyphae 侧基础 CLI 通信在下述固定组合提交上通过；Agent24 CLI 接线、基础 UI 和完整 E-M1 尚未通过。** 实现以独立小 PR 交付；最新合并状态见 [PR 依赖表](em1-pr-order.md)。后续依赖升级和修复需要另行验证，不能把这一结果当作当前 main 的完整功能验收。

## 固定版本

- 组合验收提交：[`f46744aa519937ed38832e75395591b7200d9520`](https://github.com/iDoris-ai/Hyphae/commit/f46744aa519937ed38832e75395591b7200d9520)，分支 `integration/em1-cli-acceptance`。
- 该分支仅供构建与联调，不创建汇总 PR。各项实现的前置与合并顺序见 [PR 依赖表](em1-pr-order.md)。
- 环境：macOS arm64、Go 1.25.0；临时 HOME、临时身份、回环地址及独立 relay 数据。未以用户生产身份或公共 relay 作为验收夹具。
- 单项修改另有相关包的 race 验证；本记录不表示全部新 PR 已在 Linux CI 上运行。#37 合并后，以 main 为基线的 PR 已运行 Linux/macOS CI；仍以旧 integration 分支为基线的 PR 需要在迁移后重新确认检查覆盖。

## 已验证行为

| 要求 | 实际证据 |
|---|---|
| 自部署 relay 与双向加密通信 | #42/#47：真实 Hyphae 与 khatru 二进制、Alice/Bob 独立 HOME，NIP-44 双向收发；relay 重启后仍可查询 |
| CLI 管理接口 | #45/#50/#53/#56：身份、联系人、relay 配置/探测、待发列表/清理/重试 JSON；只输出约定字段，错误和空数组可解析 |
| 发送前可靠入队与断线重试 | #54/#61：relay 停止时保存已签名事件，重启后以同一 ID/签名重试；接收方解密，双方历史正文保持 |
| 重启补收超过一页 | #60/#65/#66：真实 CLI 在收件 daemon 离线时发送 125 条；原始 relay 查询确认全部 ID 和签名，再启动 daemon，125 条正文、ID、加密标记均匹配 |
| 跨重启去重 | #51/#55/#66：同一 HOME 重启 daemon，等待首轮扫描结束，消息行保持 125 条，新消息效果为零；单元测试另验证重复通知/自动回复不再触发 |
| 边界时间戳与错误继续处理 | #60/#65：103 条同秒积压、分页重复、无进展时明确失败；坏消息不阻断有效消息，写盘失败后仍可恢复 |
| 加密身份非交互调用 | #63/#64：stdin 创建/追加身份、发送/收件解锁；错误密码不改原库，不回显密码；JSON 缺凭据不等待提示 |
| 退出与空历史 | #59/#61：停滞网络下 SIGTERM 退出；#67：新身份无消息及仅有其他身份消息时，四项统计均为零 |

#66 的旧版本对照确认 relay 在线且保存了全部 125 条，旧 daemon 只导入 10 条后停滞；测试能够检出原缺陷。修复后的同一测试通过。

## 最终检查

在上述固定提交、Go 1.25.0、隔离 HOME 下完成：

- `go test -tags integration ./... -count=1`：通过。
- `go vet ./...`、`./build.sh`：通过。
- `./test.sh`：通过；先在同一临时 HOME 创建默认测试身份，因为 `history stats` 需要当前身份。
- `identity create` 后立即运行 `history stats --json`：四项均为零。
- `go mod tidy` 后无剩余模块差异；#66 只把已有 websocket 测试依赖改为 direct，版本未变。

`test.sh` 的 E2E 段仅提示脚本入口，不能计作真实 E2E 通过；上述真实二进制证据来自 integration 标签的测试。

## 后续主线验证

2026-09-29，#70/#73/#72 合并后的 main `6bc16ee` 在独立 worktree、临时 HOME 下通过 `go test ./...`、`go vet ./...` 和 `./build.sh`。本次包含已进入主线的 CLI v3.13 兼容修复及真实二进制回归测试；后续功能链仍按独立 PR 合并。

2026-09-29，在 main `d44b3c8010a4c587e5909bc708e454c9ac3c756c`（已合并 #68/#69/#74/#77）上，主代理使用独立 worktree、临时 HOME、macOS arm64 和 Go 1.27.1 完成 `go test ./...`、`go vet ./...` 与 `./build.sh`，全部通过。本次验证不包含尚未合入 main 的 CLI 功能链，也不覆盖待修复的 #72。

同日完成新版依赖与功能链的组合验收。输入按顺序为 `f46744a`（原 CLI 验收）、`d44b3c8`（新主线依赖）、`69e4875`（#72 CLI 兼容实现）、`c4e54c9`（#59 interval 类型兼容）。临时本地组合提交为 `59803c0`，没有推送或创建汇总 PR。合并冲突仅处理模块依赖以及 relay 的 `StringArg.Max` 删除，保留 list/set/timeout 功能。

- Luna 使用 Go 1.27.1、临时 HOME、显式 GOPATH/GOCACHE，运行 `go test -tags integration ./... -count=1`、`go vet ./...`、`./build.sh`，全部通过。
- 主代理检查合并解法，并独立通过 `go test -tags integration ./tests -run TestOfflineCLIOutboxRetryAndDaemonSignal -count=1 -v`，验证真实断线重试和 daemon 退出。
- #72 自身 `72e2aaf` 另有真实二进制 JSON/帮助/位置参数回归测试、全量测试及相关包 race；主代理独立复验新命令测试。该测试增量没有混入上述固定组合。
- 上述结果证明新依赖与已验收 CLI 功能链兼容，仍不代表功能链全部合入 main 或跨仓 E-M1 完成。验证后清理临时组合 worktree 和本地分支，保留原 PR 分支。

## 2026-09-30 查询并发回归

#79 的实现提交为 `5f5e0856ac52ed6c55e64775f7c01369ebbd8f37`，基线包含 #47 的真实二进制夹具。上游 `fiatjaf.com/nostr` 的事件派发和订阅清理之间存在发送/关闭通道竞争；主代理在 Go 1.27.1、旧实现 `3fc91c9` 上运行断线/EOSE 后立即断连两个用例的 `-race -count=100`，确认复现。main CI 也记录过同类失败。

修复仅将 `relayquery.Fetch` 改为顺序读取专用 WebSocket，继续使用 SDK 事件解析、ID/签名校验与过滤；不再创建 SDK 异步订阅。完整 EOSE 之前的断线或取消返回错误，已完整收到 EOSE 后的 socket 关闭不抹去查询结果；EOSE 仍不证明完整历史。

- Luna：`go test ./...`、`go vet ./...`、查询包 `-race -count=3` 通过；上述两个竞态用例 `-race -count=100` 通过。
- 主代理：独立通过新增边界用例的 race 检查，覆盖错误订阅、无效签名/ID、过滤、可选 AUTH、畸形 EOSE 和过大帧；`go test -tags integration ./... -count=1` 通过，包含真实 CLI/relay 夹具。
- PR 双平台 CI：[36665545290](https://github.com/iDoris-ai/Hyphae/actions/runs/36665545290) 通过；合入 #48/#79 后的 main `e433a67`：[36667746886](https://github.com/iDoris-ai/Hyphae/actions/runs/36667746886) 通过。
- 本地检查均使用临时 HOME。该结果不包含 #49、#53～#67 的最终主线组合，也不代表 Agent24 或四仓闭环通过。

## 2026-09-30 查询修复与完整 CLI 链组合

将既有组合 `59803c0`、main `e433a6754687857c146e289842c8c5f86b4b235d` 和 #82 的 `6eb95e1c82e6f8fd57dcc4d44a39f3c1306e7426` 组合后，主线新增的真实 CLI 测试发现 #54 移除了 `--to` / `--content` 的必填标记：缺参数时仍退出失败，但丢失标准帮助及约定的缺参诊断。

#54 的 `3f33164facffbcaaaed3a6b0a5dc59a84ad3652f` 恢复这两个标记，保留 Action 对显式空值的校验，没有放宽测试。最终临时本地组合为 `eca615212bec5b5bbe516dfd16bd4b29e8877b6b`，不推送或合并汇总分支。

- 主代理审阅冲突处理和修复差异；组合的 `go.mod/go.sum` 与 main 完全一致，保留 #62 的失败连接清理。
- Luna 在 Go 1.27.1、临时 HOME 下通过 `go test ./cmd/hyphae -count=1`、`go test -tags integration ./... -count=1`、`go test -race ./internal/relayquery ./internal/daemon ./internal/messaging -count=1` 和 `go vet ./...`。
- 本次验证覆盖 #79 查询实现、#82 raw req/query 及完整待合并 CLI 链的兼容性；不包含后续 profile discover 修复，也不代表已交付主线或完成四仓验收。#54 新 head 仍须复审，旧批准不能替代本次变更的审阅。

## 2026-09-30 固定 main 收口验收

#93/#67/#66/#97 按最新有效批准和 head SHA 正常合并后，main 固定为 `1948aadc551e360176711f9c50172ed6edccd253`。Luna 在独立 detached worktree、Go 1.27.1 darwin/arm64、临时 HOME 下执行；根代理核对原始日志、源码断言、二进制摘要及干净工作树。GOPATH/GOCACHE 使用固定缓存路径，测试数据不进入用户 HOME。

- `go test ./... -count=1`、`go test -tags integration ./... -count=1 -v`、`go vet ./...`、`./build.sh` 和 `./test.sh` 均退出 0。
- 真实 `TestDaemonImportsAndDeduplicatesOfflineBacklog` 执行 6.42 秒并通过：relay 保存恰好 125 个唯一且签名有效的事件；daemon 导入 125 条正确正文/属性并产生 125 个新消息效果；同库重启行数及内容不变、新效果为零。
- 真实 `TestOfflineCLIOutboxRetryAndDaemonSignal`（1.73 秒）和 `TestEncryptedCLIRelayFlow`（1.69 秒）均通过。三个目标测试实际执行，未跳过；integration tests 包共 13.192 秒。
- 默认 suite 包含 stdin 和空统计回归。另在临时 HOME 创建合成默认身份后运行 `history stats --json`，单个成功信封中的四项统计均为零；身份创建输出丢弃，临时数据已清理。
- `test.sh` 只提示 E2E 脚本，没有执行它们；真实 relay 证据来自上述 integration。`go.mod`/`go.sum` 前后摘要不变，源码工作树干净。
- macOS arm64 构建产物 SHA-256：`a7bb4a83b5d6be0a939a4cd92a853a2672f97012c48d704a9a3a718b9e6d806b`。未安装生产二进制，其他平台产物需独立记录摘要。
- 该 main 的 [Linux/macOS CI 36729070274](https://github.com/iDoris-ai/Hyphae/actions/runs/36729070274) 通过，包含真实 integration 步骤。

此项收口 Hyphae 侧基础 CLI；Agent24 CLI/UI、模型、模块完整生命周期、语音和四仓执行闭环仍未通过。T20/T21/T22/T19 维持各自跨仓门槛。

## 接线边界与下一步

1. Agent24 按 [协作 PR #41](https://github.com/iDoris-ai/Hyphae/pull/41) 接 CLI，再完成身份/联系人/relay 管理及基础消息 UI；回填实际 PR、固定提交和验收结果。
2. daemon 目前输出运行日志，UI 通过 history JSON 读取持久化消息。进程存在、WebSocket 探测成功、relay 接受分别表达，不能代替对端送达或执行确认。
3. 补收受 relay 保留策略、分页上限和查询预算约束；普通 EOSE 不证明服务端完整历史。落盘后崩溃可能错过通知/自动回复，不等于任务执行恢复。
4. 旧 kind 30078 和正文保持兼容；未承诺支持所有公共 relay。邀请访问控制、多 relay 转发和高层 behavior 尚未实现。`profile publish` 的加密身份非交互注册仍待 T08。
5. T01 高层协议冻结、T07～T17、基础 UI、模型/模块/语音和四仓联调仍未完成。Agent24 旧入站执行路径的分流与持久化去重缺口已写入协作文档，不能以本仓消息去重替代执行验收。
6. #44 上游更新工作流已测试并合入默认分支，但 Actions 创建 PR 权限设置尚未完成；自动创建更新 PR 的线上闭环尚未验收。
