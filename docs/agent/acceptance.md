# Acceptance — 用户视角「算不算做好了」

> 配套:[`../milestones/testing-integration-plan.md`](../milestones/testing-integration-plan.md)(三道门的完整论证)、
> [`../05-acceptance-test-guide.md`](../05-acceptance-test-guide.md)(Alice/Bob/Charlie 角色约定)、
> [`../M1.5_TEST_GUIDE.md`](../M1.5_TEST_GUIDE.md)、[`../../QUICK_MANUAL_TEST.md`](../../QUICK_MANUAL_TEST.md)。
> 最后更新:2026-09-03

## 三道验收门(从 M3 起,每个里程碑都要依次通过)

过去的标准是「`go test ./...` 全绿 + 人工 review」。CC-82 证明了它有盲区,所以现在是三道:

| # | 门 | 验证什么 | 谁做 | 能不能跳 |
|---|---|---|---|---|
| 1 | **单元测试** | 新代码逻辑本身正确(编解码、状态转换、边界) | 实现者 | 不能 |
| 2 | **本地真实网络** | 真实二进制打进真实 relay 后行为符合预期,不是只在内存 mock | 实现者 | 不能 |
| 3 | **跨仓联调** | 契约对**外部真实消费者**是否真的成立 | 双方工兵 | **不能** |

### 为什么第 3 道不能跳

CC-82(与 Agent24 的 F4 Nostr 渠道契约核对)一个下午挖出 4 个真实 bug:

| bug | 单元测试能发现吗 | 读代码能发现吗 |
|---|---|---|
| `agent msg` 缺 `d` 标签 → relay 端覆盖丢消息 | ❌ 要真实 NIP-01 relay | ❌ |
| `profile publish`/`history inbox` 的 `--json` 是摆设 | ❌ | ✅ 唯一一个 |
| `StoredMessage.ID` 编码损坏 | ❌ | ❌ 对端跑真实二进制才现形 |
| `agent inbox` 的 `from` 截断破坏对端白名单校验 | ❌ | ❌ 同上 |

**4 个里 3 个只有真正对接一次才挖得出来。** 联调不是锦上添花,是验收的一部分。

### 联调任务的标准节奏(复用 CC-82)

1. **开工前** — 把本里程碑要改的对外契约(CLI 参数、JSON shape、事件 kind/tag 约定)写成「契约声明」,在 Seeder `Cooperation-Center` 开任务(`repo:speaker`),@ 已知外部消费者(目前 `repo:agent24`),请对方 check「能不能满足、会不会破坏你现有实现」。
2. **实现中** — 走第 1、2 道门。
3. **合并前** — 若改动了声明过的契约字段,回同一任务请对方拿**真实代码**跑一遍,双方对一遍实际 JSON/事件输出。发现 bug 走「报告 → 决策 → 修 → PR → review → 合并 → 回任务同步」完整闭环。
4. **合并后** — 贴 PR 链接 + 合并 commit,标 `✅ 已实现`。

## M3 的验收标准(当前里程碑)

**用户视角能做到什么**:

```bash
# 1. 用统一的 behavior 语言注册自己,而不是只能发裸消息
hyphae behavior register --mode structured --capability "seo:..." --rate "audit:page:50"

# 2. 向某个 Agent 询价
hyphae behavior inquire --target <npub> --capability seo

# 3. 订阅某个标签
hyphae behavior subscribe --filter '{"tags":["AI"]}'
```

**算做好了的硬条件**(全部满足才能把 M3 从 🔄 翻成 ✅):

- [ ] 四种 behavior(`register`/`publish`/`inquire`/`subscribe`)收发都跑通,各自有单元测试
- [ ] `tip`/`drifting-bottle` 的 payload schema 已定义且被测试固定住(**不要求能执行**)
- [ ] Owner-attestation 字段的数据结构 + 签名/校验函数就位(**不要求接真实 AirAccount**)
- [ ] behavior 信封用**独立 kind**,不再与 `agent msg` 共用 30078
- [ ] `SaveOutbox` 并发安全(丢更新 + 文件损坏两种失败模式都有回归测试)
- [ ] **标准 Nostr 客户端读到我们的 behavior 事件不报错**(`protocol-v2.md` §11 兼容性承诺)
- [ ] **第 3 道门**:Agent24 拿真实二进制跑通一次 behavior 契约,双方在 Cooperation-Center 任务里对过实际输出

## 长期不可退让的产品底线

无论做到哪个里程碑,以下任一被破坏都算「没做好」:

1. **私钥永不出本机**;`private` 字段永不出本机。
2. **任何人可自部署 relay**,不依赖任何一方的中心化基础设施。
3. **人和 Agent 是同一套身份体系里的平等成员**(各自的 Nostr keypair)。
4. **发现有代价但对陌生人友好** —— 不能退化成「要么免费广播骚扰所有人,要么完全找不到人」。
5. **标准 Nostr 客户端不会因为我们的扩展而报错**。
