# Research — 立项依据与差异化

> **权威调研文档是 [`../buzz-comparison-analysis.md`](../buzz-comparison-analysis.md)**(与 Block 的 Buzz 全面对比)
> 和 [`../agent-protocols-summary.md`](../agent-protocols-summary.md)(MCP / A2A / ACP 对比)。
> 本文件只提炼**已经拍板的结论**,让 `run` 循环不用重新论证一遍。
> 最后更新:2026-09-03

## 我们是什么

**Hyphae 不是人对人的加密聊天工具,是去中心化的 Agent 协作网络基础设施。** 三条核心主张:

1. 任何人可自部署 relay,无需依赖任何一方的中心化基础设施
2. 人和 Agent 是同一套身份体系里的平等成员,Agent 可被授权代表 owner 做有限的事
3. 发现和协作有代价,但代价对陌生人友好(花名册 + 漂流瓶低成本触达 + 按用付费)

命名呼应 Mycelium Protocol:hyphae(菌丝)是构成 mycelium 网络的单根丝。

## 与相邻协议的关系

| | MCP | A2A | ACP | **Hyphae** |
|---|---|---|---|---|
| 发布方 | Anthropic | Google | IBM | iDoris-ai |
| 协议层 | Agent-Tool | Agent-Agent | Agent-Agent | Agent-Agent |
| 通信 | stdio/HTTP | HTTP | HTTP | **Nostr Relay** |
| 中心化 | — | 需要可达的 HTTP 端点 | 同左 | **无中心,relay 可自部署** |

差异化在**传输层的去中心化**:A2A/ACP 假设 Agent 有稳定可达的 HTTP 端点(实际上要么上云要么打洞),我们走 Nostr relay,Agent 只需要一个 keypair。

## 与 Buzz(block/buzz)的边界 —— 已拍板,不重新讨论

Buzz 是 Block 出品的**中心化托管团队协作平台**(Postgres+Redis+S3+多租户,27 crate 微服务)。我们是**无中心自部署 Agent 对等网络**(fork khatru + SQLite)。**架构方向不变**,只借它已验证可行的具体机制。

**借**(已编入各里程碑):

| 借什么 | 落在哪 |
|---|---|
| 成员角色显式建模(至少 Human/Agent-Bot) | M2 ✅ 已做 |
| Owner-attestation 授权字段(NIP-OA 思路,身份换成 AirAccount) | M3 · F3.4 |
| 邻居发现/隧道传输的**思路**(选型换成 Go 生态 libp2p/gossipsub) | M4 |
| workflow schema 极简设计(4 触发器 + 有限 action + 单遍模板解析) | M5 |
| approval 挂起-恢复**必须能持久化**(Buzz 自己的 `WF-08` 是现成反面案例) | M5 · F5.4 |
| Agent 活动用「动词-宾语-结果」三元组渲染,不甩原始 JSON | M6 · F6.3 |
| 资金安全用 property-based test(`gopter`),**不上** TLA+/Tamarin | M4 · F4.4 |
| CLI `--json` in/out、`audit_log` 哈希链 | M2 ✅ 已做 |

**明确不借**(理由见 `buzz-comparison-analysis.md` §7):Postgres+Redis+S3+多租户后端、27-crate 微服务拆分、TLA+/Tamarin 全套形式化验证、完整 Git 托管后端、Flutter 移动客户端、**「新功能=新 kind+新 NIP 草案」的协议膨胀模式**。

最后一条尤其重要:它正是我们 M3 要做 behavior 统一信封的原因 —— 反向选择。

## 技术选型(已定)

| 需求 | 选型 | 理由 |
|---|---|---|
| Relay 软件 | fork [`fiatjaf/khatru`](https://github.com/fiatjaf/khatru) | Go(同语言)、官方维护、中间件机制清晰、NIP 实现完整 |
| 加密 | NIP-44 | Nostr 标准 |
| 压缩 | zstd | 已在 `pkg/compress` |
| 本地存储 | SQLite(WAL) | 已取代早期的 bbolt 设想 |
| 支付 | **AAstar Point(ERC-20)+ SuperPaymaster gasless** | Lightning 是比特币 L2、与本生态无关联;AAstar 已有 ERC-4337 抽象账户 |
| 身份/账户 | AAstar AirAccount | 同上 |
| 漂流瓶向量 | 倾向轻量本地模型(sentence-transformers ONNX) | ⚠️ **D2 未最终拍板**,M4 开工前必须定 |

## License 与生态边界

Apache 2.0。属 Mycelium Protocol 生态,受 MushroomDAO 商标约束(**分叉须更名**,见 `TRADEMARK.md`)。PGL 数字公共物品公约的接入(`pgl.yml`、链上分账、AgentStore 上架的「妈妈测试」)**尚未开始**,不在 M3-M7 范围内。

## 长期跟踪的未立项方向

**Buxin / 不信** —— 基于本仓库 Nostr 栈的自建家庭 IM + 音视频通话。消息层可直接复用(NIP-44 + 群聊 + relay 自部署),净新增是 WebRTC 信令(可复用 behavior 信封做 offer/answer/ICE)、NAT 穿透(倾向 WireGuard/Tailscale 私网 overlay 而非公网暴露端口)、移动端(当前完全没有,是最大缺口)。**不进 M3-M7**,未来可能独立开仓库把 hyphae 当 SDK。
