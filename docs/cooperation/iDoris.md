# iDoris × Hyphae

状态：Hyphae 侧协作提案，待 iDoris 确认。审阅基线：独立工作树 `iDoris-hyphae-review@074d35f`；原仓库未提交修改保留。

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
