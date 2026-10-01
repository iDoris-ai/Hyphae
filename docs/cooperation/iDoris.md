# iDoris × Hyphae

状态：Hyphae 侧协作提案，待 iDoris 确认。2026-10-01 只读核对 iDoris main `ffed37a107a2e152963caea845450c9515d12044` 与 Agent24 main `f5a76c015a7026c64fc872f47c6c160485cfed37`；没有改动两仓用户工作区、运行服务或调用 provider。iDoris 固定提交比旧基线 `074d35f89c9281a742872a1baf54013ee9d42b56` 多 273 commits。当前事实以本段两个固定 SHA 为准。

## 边界与当前实现

Hyphae 不调用模型来完成基础消息收发；iDoris 不接管 Nostr relay、身份密钥和投递重试。模型协作发生在 Agent24 Rust `agent24-models` 与 iDoris Rust router 之间。Agent24 当前尚未接入 iDoris：固定源码的 provider registry 默认只建 oMLX 与 Ollama，没有 iDoris provider 或 `X-iDoris-*` header 实现。

iDoris 当前正式入口是 `crates/idoris-router/src/bin/idoris.rs`，默认绑定 `127.0.0.1:8740`，路由包括 `/health`、`/v1/models`、`/v1/chat/completions`。生产 `AppState::default()` 未装载虽已实现的 SQLite `BudgetLedger`；没有认证、capabilities、usage/audit HTTP API。付费 dispatch 在账本不可用时 fail closed。非免费 resident `http_service` 启动受拒绝。router 可解析隐私/tenant等 profile headers，并在响应生成 Record-Id、选择后返回 Served-Locality；buffered proxy 只向上游传 JSON body，不传身份/控制 headers。

固定源码： [iDoris router entry](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/bin/idoris.rs#L77-L124)、[routes and state defaults](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/lib.rs#L138-L221)、[profile parsing](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/profile.rs#L72-L143)、[loopback registration checks](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-policy/src/registry.rs#L96-L190)。组件声明 `locality: loopback` 时会静态校验 endpoint scheme/host；这不证明 DNS 解析、redirect 或最终连接目标受限。

## 协作顺序

1. **iDoris 服务端先交付**：正式 binary 接入持久 ledger；绑定经认证的身份与 tenant scope；明确币种/最小单位、单请求与租户上限、`all`/`paid_only` gate、未知价格策略、并发预留和重启恢复；定义 provider usage 缺失/无效时的估算或未知策略，并让 reserve/settle/usage 查询来自一致账本；提供并测试 capabilities 与受保护的 usage 查询合同。当前 reservation completion 占位固定为 1024 tokens，settle actual 由本地文本 token estimator 得出，不是 provider 报告的真实 usage；没有可信 usage 时只能明确估算或未知，不能填 actual/0 成本：[budget estimate](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/budget.rs#L17-L87)、[dispatch settle](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/dispatch.rs#L380-L399)、[SQLite ledger](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-tenancy/src/budget/ledger.rs#L218-L245)。
2. **再由 Agent24 实现专用 adapter**：将获准的 privacy/intent/complexity/capability/tenant/request id 映射到双方确认的 header，保留取消；读取并验证 `X-iDoris-Served-Locality`、Record-Id 与 usage 来源。当前 OpenAI-compatible adapter 只发送标准 chat body 和自己的可选 Authorization，未消费 iDoris 响应 headers：[Agent24 provider](https://github.com/iDoris-ai/Agent24/blob/f5a76c015a7026c64fc872f47c6c160485cfed37/rust/crates/agent24-models/src/lib.rs#L185-L223)、[request/response path](https://github.com/iDoris-ai/Agent24/blob/f5a76c015a7026c64fc872f47c6c160485cfed37/rust/crates/agent24-models/src/lib.rs#L569-L690)。默认注册是 OMLX+Ollama：[registry](https://github.com/iDoris-ai/Agent24/blob/f5a76c015a7026c64fc872f47c6c160485cfed37/rust/crates/agent24-models/src/lib.rs#L694-L729)。
3. **endpoint 形态先用单入口**：由 iDoris 内部 privacy-aware router 选择 provider。`idoris-local`/`idoris-any` 是旧设计提案，不是当前已实现/确认的两个 iDoris endpoint；如仍需要双逻辑 provider，需先由双方确认它们的 capabilities、落点及失败语义。

## 取消、重试、缓存与 egress

Local handler 当前传入新建 cancellation token，没有将客户端断开接入；proxy streaming response 在断开时可通过 drop 取消上游，但 streaming 不重试/缓存。Buffered proxy 对 5xx/transport 最多重试两次，成功响应可按 tenant/endpoint/provider/request-id 做 60 秒进程内缓存，不是持久执行幂等账本；router 的默认 reqwest client 允许进一步核查 redirect、环境代理及实际连接目标边界。[handler](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/lib.rs#L498-L529)、[proxy retry/cache](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/proxy.rs#L208-L329)、[stream path](https://github.com/iDoris-ai/iDoris/blob/ffed37a107a2e152963caea845450c9515d12044/crates/idoris-router/src/proxy.rs#L339-L385)。

后续合同测试使用本地 mock 与临时 SQLite：local_only 不得外发；客户端断开可取消仍运行的 provider；只有能确认未执行/未产生费用时才释放 reservation，若费用可能发生或结果不明则保留未结算状态并进入对账，不能假定取消即零费用；成功只 reserve/settle 一次；provider usage 与账本、响应 cost 一致；缺失 usage 不标 actual；retry/cache 不重复收费且按 tenant 隔离；Authorization/控制 headers 不泄漏；redirect/代理配置不能绕开 egress 策略。Agent24 adapter 再测请求 header、取消、非成功映射和响应 locality/Record-Id 消费。此次只是源码审查，未运行这些测试或真实服务。

建议的本地命令（待对应实现添加测试后运行，不是本轮结果）：

```sh
cd /path/to/iDoris && cargo test -p idoris-router
cd /path/to/Agent24/rust && cargo test -p agent24-models
```

服务验收还需另提供固定两仓 SHA、隔离 tenant/SQLite/mock upstream 的启动配置、实际 HTTP 命令及 record id/账本断言；`cargo test` 不能替代真实入口检查。

## 历史：2026-09-30 的 TypeScript 快照

以下仅记录旧 `074d35f` TS 源码，不代表当前 main。该快照中 TypeScript router 提供 health/models/capabilities/chat，但 usage/budget HTTP route 未接；tenant budget 是内存 helper，proxy 未校验/落账 provider usage。旧判断“没有持久 SQLite ledger”只适用于该 TS 快照；iDoris 当前 Rust main 已有 SQLite ledger 模块，但 production binary 尚未接入。旧详细证据见 [旧提交接口表](https://github.com/iDoris-ai/iDoris/tree/074d35f89c9281a742872a1baf54013ee9d42b56/packages/router/src)。

T01-E 与 T07 门槛不变；本轮不冻结 wire、角色目录或双 endpoint，也不把源码审查记作编译、服务或跨仓验收。基础 CLI/UI 通信不依赖模型接线完成。
