# iDoris × Hyphae

状态：Hyphae 侧协作提案，待 iDoris 确认。2026-09-30 只读 GitHub API 固定 main `074d35f89c9281a742872a1baf54013ee9d42b56`；本轮未访问或修改原仓库用户暂存文件，未启服务。下述事实仅针对该提交。

## 边界

Hyphae 不调用模型来完成基础消息收发；iDoris 不接管 Nostr relay、身份密钥和投递重试。模型接线发生在 Agent24 的 Rust `agent24-models`，由 Agent24 向 iDoris 传递隐私、预算和能力约束。

## 协作事项

- 确认 `IDORIS_URL`、`idoris-local/idoris-any` 和角色目录对应的请求/错误格式。
- 贯穿 `X-iDoris-Privacy`、`X-iDoris-Served-Locality`、Record-Id 及缓存原始落点。本地回环地址本身不能证明推理发生在本地。
- 确认预算准入/核销归属与用量来源；未知或估算用量不得写成实际用量。
- 相关上游升级改变模型接口时，先提出兼容约定，由 Agent24 跑真实 provider 联调。此次 Nostr/khatru 升级不要求修改 iDoris。

## 验收与待确认

本地限定且本地不可用时，外部调用次数必须为零；预算拒绝不得启动模型调用；实际 provider/model 与审计落点一致。已有入口为 `pnpm --filter @idoris/router test`、`typecheck` 和 `pnpm smoke:agent24`；最后一项不替代 Agent24 Rust provider 的真实接线验收。

待确认：角色目录 Q-3、预算字段/错误码、缓存与重试的用量核销规则。基础 CLI/UI 通信不依赖这些事项完成。

## T01-D：已核对接口与真实缺口

固定来源均为 [iDoris@074d35f](https://github.com/iDoris-ai/iDoris/tree/074d35f89c9281a742872a1baf54013ee9d42b56)。

| 项目 | 固定版本事实 | 接线约束 |
|---|---|---|
| 请求隐私 | `packages/router/src/profile.ts` 支持 `X-iDoris-Privacy: local_only\|any`，缺省 local_only；tenant 模式需 Tenant header | 控制模型执行位置，不表达发起端向远端 Agent 披露数据的许可；两项授权分别检查 |
| 实际落点 | `dispatch.ts` 与 `locality.ts` 校验有效 loopback、privacy_class 和 allowed_egress；subscription/spawn_cli 为 remote | 不能凭 router 回环 URL 或客户端 provider tier 推断实际落点 |
| 记录与缓存 | `server.ts` 各响应生成独立 Record-Id；通过来源校验并进入 backend 路径后才设置 Served-Locality；缓存回放原始落点及 Origin-Record-Id | 早期错误可以没有 locality；不能补猜测值。缓存来源与本次记录分别保留 |
| HTTP 服务 | `server.ts` 提供 health/models/capabilities/chat；usage/budget 路由未接入 | tenancy helper 的响应 interface 不能当已可调用 API；未知路径当前为 404 |
| 预算 | `packages/tenancy/src/budget.ts` 只比较 TenantContext 的 spent_minor/limit_minor，区分 all/paid_only；charge 是内存计数器；store 是内存数组 | router 未接入预算 helper，也未消费 schema 的可选 quota.rpm/tpm；缺少单次费用上限、原子预留或实际核销，不能宣称预算受限执行已实现 |
| 用量 | `proxy.ts` 透传 provider JSON，不核验/落账 usage；`packages/adapters/subscription/relay.ts` 按文本长度估算 tokens | 缺少可靠来源时只能标未知或估算，不能统一标 actual |
| 重试 | 非流式代理可重试；请求 ID 可用于短期进程内缓存，但没有连接预算账本 | 缓存去重不是持久化执行幂等；一次逻辑请求的多个尝试须另行定义准入和核销 |

这些是代码核查证据，不是本轮真实 provider 联调结果。现有 helper 单元通过不代表 HTTP 准入、持久化账本或并发预算通过。

## 由对应仓库推进的最小任务

1. **iDoris：预算准入与持久化。** 固定请求上限/租户上限、单位与币种、预留键和未知价格处理；将准入与核销实际接入 chat 路径。并发请求不能共用同一份过期余额；重试、缓存命中、取消和崩溃不能重复核销或直接释放未确认费用。模型费用控制不等于 E-M5 支付实现。
2. **iDoris：用量来源与查询接口。** 区分 provider 返回、估算和未知；实际/估算 token、cost 和原始记录的语义分别固定。公开 usage/budget 路由前补真实 HTTP、权限、持久化与时区验收，不能仅暴露类型定义。
3. **Agent24：正式 provider 接线。** 复用已有 idoris-local/idoris-any 设计，转发严格许可并验证服务身份、响应落点及来源；缺落点时不猜测本地，预算能力不满足时不启动预算受限调用。网络取消不能降级成另一个 provider 继续运行。
4. **Hyphae：执行约束与回执字段。** 请求的隐私/预算/模块许可进入授权摘要；回执记录实际观察。字段与双方错误码确认后再补共享正反例，T01-E 之前不开启生产执行入口。

验收至少包含：预算零（区分 all/paid_only）/未知价格/超限、并发预留、重试与缓存核销、取消后未知费用、重启恢复；local_only 本地不可用时外部调用零；any 实际走本地时准确记录；缺 locality、缓存原始落点和估算 usage 不误标。固定双方 commit、真实服务命令与记录 ID，再回填 PR 链接。跨仓实现由用户推动，本文不表示对应仓库已确认或完成。
