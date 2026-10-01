# E-M1：Agent24 CLI 联调缺口

2026-10-01。Hyphae 基线 `a4aa606eb81d5c040d94c51cdf94553e646d8674`；反馈来自 [Agent24 #601](https://github.com/iDoris-ai/Agent24/pull/601#issuecomment-5923235533)，接口设计见已合并的 [COMM-0 #612](https://github.com/iDoris-ai/Agent24/pull/612)。下列改动各自独立提交，不切换高层协议，不解除 T01-E。

## 已确认的 CLI 行为

- 未配置 relay 时回退到 `wss://relay.aastar.io`。托管调用应先要求显式配置，再向 daemon 传入解析后的 relay。
- 全部 relay 不可达时，可靠入队成功仍可返回 `ok:true`、退出码 0；`published_to` 是 relay 接受证据。history 有记录不能证明 relay 或对端收到消息。
- mixed ACK 不必然返回退出码 4；历史或队列记账失败可以返回错误信封及部分 `data`。不能把所有网络原因或部分失败统一映射到同一退出码。
- keystore 整库加密；追加身份也需要口令。托管 daemon 显式使用 `--notify=false --auto-reply=false`；口令只经 `--password-stdin`。
- 普通消息已使用 kind 30078。基础通信的 zero-run 来自不接入执行入口；kind 过滤不能证明这一点。
- 固定 main 的非法公钥 `contact add` 路径返回 `other_error`；发送路径已按 `user_error` 处理。G6 已本地提交输入分类修复；新源码/hash 锁采用时，Agent24 需更新 #614 中旧二进制的退出码断言。
- `identity change-password` 的 JSON 模式拒绝交互提示，不能用作只读口令检查。

## 独立实现任务

| 任务 | 输入与输出 | 验收重点 | 状态 |
|---|---|---|---|
| G7：会话历史 JSON | `history conversation --with … [--as …]`；复用 JSON 开关和 `StoredMessage[]`，空结果为 `[]` | 三种 JSON 开关、默认/指定身份隔离、limit、完整正文与方向；错误不混入成功输出 | 本地 `2a8de300`，专项通过，待发布/完整 CI |
| G8：keystore 并发写 | 稳定 `keystore.json.lock`；锁内重新读取、验证、修改、原子保存 | 跨进程创建不丢私钥；密码轮换、legacy token 升级、联系人/default 写入都不覆盖新状态 | 本地 `087832c5`，专项/race 通过，待发布/完整 CI |
| G9：文件正文 | `agent msg --content-file PATH`；与 `--content` 恰选一个，stdin 留给口令 | 正文不在 argv；字节保持，空文件/超限/非法 UTF-8/非普通文件拒绝；验证失败不写历史或队列 | 本地 `3472f935`，专项/跨平台编译通过，待发布/完整 CI |
| daemon 互斥 | 同一 HOME 单实例；不同 HOME 可并行 | 稳定锁 inode；正常退出及 kill 后可重新启动；冲突先于口令读取返回 5；I/O 失败不伪装成冲突 | 本地 `7ea7ad8f` 专项/race 通过，特殊路径测试已通过，最终 `6765d1d` |
| G1：只读口令检查 | `identity check-password --password-stdin`；成功仅输出 valid/encrypted | 不创建 HOME，不迁移 legacy token，不改 keystore、history、outbox 或审计；错误不回显秘密 | 本地 `7fa3e7bc`，最终 identity/真实 CLI/race 通过，待发布/完整 CI |
| G6：联系人输入分类 | 保留有效 npub/hex；非法公钥在 keystore 读取前返回 `user_error`/1 | 输入不回显，拒绝不写盘；真实存储错误不伪装成输入错误 | 本地 `574f049b`，identity/真实 CLI/race 通过，待发布/完整 CI |
| G7b：存储信息 JSON | `storage info`；存在/不存在数据库均输出单一机器信封 | 未建库不创建 HOME/目录/DB；真实计数、稳定 table 顺序、读取失败不先输出成功 | 本地 `7dadc7a9`，storage/真实 CLI/race 通过，待发布/完整 CI |
| 补收状态 | `daemon --json` 的带版本 JSON-lines 完整快照，区分进程、relay 与扫描状态 | 失败/取消/不完整不显示完成；每轮完成不等于永远同步；不记录正文和秘密 | S1 `82de8549` / S2a `6f7daa14` 已本地提交及专项/race通过；S2b `7b29bf6f` 已本地提交（410 生产行），最终专项/race及根代理独立race通过；消费端、真实 relay 与 CI 待验收 |

G7 的 JSON 数组沿用查询返回的最新在前顺序；人类模式维持原有最早在前展示。`--as` 未提供时使用默认身份。历史没有对端回执字段，不构造该状态。

G8 不仅锁住 `rename` 或 `SaveKeyStore`。业务操作须在锁内重读并重新验证唯一性、加密状态和密码。保留直接 `SaveKeyStore` 的调用时，使用加载版本与磁盘版本比较；陈旧快照返回既有 `write_conflict`（退出码 5），不合并旧密钥。legacy token 升级只修改仍匹配的最新验证字段；轮换后不能用旧密码写回。锁文件保持 0600，目录/keystore 保持 0700/0600。

Agent24 的进程内写入监管仍负责自身调用与导入；daemon 解锁 legacy 库时也可能升级验证 token 并写 keystore，因此进程内监管不能替代 Hyphae 的跨进程锁。外部调用方不可持有 Hyphae 的文件锁后再启动 Hyphae 子进程，否则会互相等待。升级时先停止旧版写入进程，旧版不认识新锁。导入暂停生产写入并保留 `outbox.json`。

G1 使用既有 4096 字节原始 stdin 上限，只移除一组末尾 LF/CRLF，保留空格。该身份命令与 Agent24 COMM-0 所提 `keystore verify` 名称不同，接线时采用上述实际命令；新 hash 锁采用前不得调用。校验成功只证明读取到的 verifier 匹配，不保证稍后密码轮换后仍可解锁，不检查每个身份私钥。测试验证 SHA-256、mtime、mode 不变；初稿的换行预期与 stdin pipe 超时已修正，原始失败日志保留。

G7b 的现有库读取使用 SQLite read-only URI，统计完成后才输出 JSON；不自动迁移旧 schema。活跃 WAL 的共享内存 sidecar 可能由 SQLite 管理，不能把 mode=ro 当全文件系统不变证明。

G9 新增文件输入上限为 1 MiB；读取时限制实际字节数，拒绝目录、FIFO、设备和 `-`。文件正文不去除空格或末尾换行。现有 `--content` 保持兼容；stdin 不混入正文。

## 联调与交付门槛

1. 每项固定基线，Luna 在独立 worktree 实现和测试；根代理评审实际差异。各生产 PR 通常 300～500 行以内，测试和纯文档除外。
2. Agent24 COMM-1a [#614](https://github.com/iDoris-ai/Agent24/pull/614) 已在 `77655f48444e45e3ee0ec37e160bccccfbe8b8b2` 合并，双平台 Rust CI 通过。此前环境扫描误把集成测试的 `HYPHAE_TEST_BIN` 当生产配置，最终版已排除 `tests/` 并保留生产变量正对照。该最终 head 的 CI 仍未配置真实 Hyphae 构建和测试二进制，真实测试缺环境变量时直接返回。后续需独立交付不能跳过的真实制品/hash 验证和联合测试，不能把假子进程测试或跳过项当接线验收。
3. 可复现构建配方须固定源码、Go 版本、平台及完整参数。Agent24 的 Go 1.26.4 配方与旧 Go 1.27.1 临时二进制是不同制品，不能混用 hash。Hyphae 尚未复现该配方；新改动发布后需要重新锁定双方版本和 hash。
4. CLI 出口仍需 Agent24 实际入口、真实 relay 双向加密通信、离线重试与重启，以及 runs/模型/模块计数为零。UI 则需 CLI/UI 共用服务和真实状态测试。
5. T01-E、授权执行和四仓验收继续以 [任务台账](ecosystem-tasks.md) 的完整出口判断，任何本仓测试都不替代它们。
