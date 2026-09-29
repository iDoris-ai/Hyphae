# E-M1-A：Hyphae CLI 验收记录

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

## 接线边界与下一步

1. Agent24 按 [协作 PR #41](https://github.com/iDoris-ai/Hyphae/pull/41) 接 CLI，再完成身份/联系人/relay 管理及基础消息 UI；回填实际 PR、固定提交和验收结果。
2. daemon 目前输出运行日志，UI 通过 history JSON 读取持久化消息。进程存在、WebSocket 探测成功、relay 接受分别表达，不能代替对端送达或执行确认。
3. 补收受 relay 保留策略、分页上限和查询预算约束；普通 EOSE 不证明服务端完整历史。落盘后崩溃可能错过通知/自动回复，不等于任务执行恢复。
4. 旧 kind 30078 和正文保持兼容；未承诺支持所有公共 relay。邀请访问控制、多 relay 转发和高层 behavior 尚未实现。`profile publish` 的加密身份非交互注册仍待 T08。
5. T01 高层协议冻结、T07～T17、基础 UI、模型/模块/语音和四仓联调仍未完成。Agent24 旧入站执行路径的分流与持久化去重缺口已写入协作文档，不能以本仓消息去重替代执行验收。
6. #44 上游更新工作流已测试并合入默认分支，但 Actions 创建 PR 权限设置尚未完成；自动创建更新 PR 的线上闭环尚未验收。
