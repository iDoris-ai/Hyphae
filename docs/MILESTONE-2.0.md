# Agent-Speaker Milestone 2.0 Roadmap

> **定位声明**: agent-speaker 不是独立的任务市场，而是 MyTask 生态的 **底层通信与发现层 (Communication & Discovery Layer)**。它负责链下人与人的连接、意图解析、能力匹配和协商通信，最终通过 MyTask SDK/Contracts 完成链上契约与结算。

**更新日期**: 2026-04-13  
**当前基线**: v0.24.0 (Group Chat + SQLite + TUI + Refactor)  
**关联系统**: MyTask (TaskEscrowV2 / JuryContract / MySBT / x402 / agent-mock)

---

## 1. 已完成里程碑 (Phase 1: 基础设施)

| 版本 | 里程碑 | 核心功能 | 测试覆盖 | 状态 |
|------|--------|----------|----------|------|
| **v0.22.0** | Go项目重构 | 标准布局 (cmd/, internal/, pkg/) | ✅ 100% | 已完成 |
| **v0.22.1** | SQLite存储 | 消息持久化、搜索、迁移 | 11U + 6E2E | 已完成 |
| **v0.23.0** | TUI界面 | Bubble Tea聊天界面 | 8U + 3E2E | 已完成 |
| **v0.24.0** | 群聊功能 | 多人组、成员管理 | 14U + 14E2E | 已完成 |

> **累计**: 43+ Unit Tests, 27+ E2E Tests, 标准 Go 项目布局, SQLite 后端, TUI 交互, 群组通信

---

## 2. 与 MyTask 的生态关系分析

### 2.1 MyTask 负责什么

MyTask 是一个 **链上任务市场协议**，包含以下核心组件：

| 层级 | 组件 | 职责 |
|------|------|------|
| **合约层** | TaskEscrowV2 | 任务生命周期托管与资金分配 |
| **合约层** | JuryContract | ERC-8004 验证 + 陪审团投票 |
| **合约层** | MySBT | agentId → owner 的链上身份映射 |
| **合约层** | MyShopItems/RewardAction | 链上激励发放（Items + Actions） |
| **链下服务** | agent-mock | 事件监听、编排自动化、x402 代理 |
| **链下服务** | indexer.js | 链上事件索引与 Dashboard |
| **支付协议** | x402 | HTTP-native gasless 支付 |

### 2.2 MyTask 的 Agent 模型

MyTask 中的 Agent 是 **链外自动化服务**（agent-mock），职责是：
- 监听链上事件（TaskCreated, EvidenceSubmitted 等）
- 调用 x402-proxy 获取资源/支付
- 提交 gasless UserOperation 完成链上状态推进
- 执行 jury validation 的自动编排

**但它缺少什么？**
- ❌ 没有 **去中心化的 Agent 发现机制**
- ❌ 没有 **P2P 协商通信层**
- ❌ 没有 **用户意图的本地 LLM 解析**
- ❌ 没有 **人与 Agent / Agent 与 Agent 的实时对话能力**

### 2.3 agent-speaker 的新定位

agent-speaker 填补以上空白，成为 MyTask 的 **前置层 (Pre-Contract Layer)** 和 **协作通信层 (Collaboration Layer)**：

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         用户交互层 (Frontend / CLI / TUI)                │
│                              agent-speaker                               │
├─────────────────────────────────────────────────────────────────────────┤
│  意图解析      │  Agent发现      │   P2P协商通信    │   任务协调        │
│  (本地2B LLM)  │  (nostr relay)  │   (E2E加密消息)  │   (群组/TUI)      │
│              │                │                 │                  │
│  用户说:     │  搜索 relay     │  发送 RFP       │  创建协作群组      │
│  "帮我找个   │  上带标签的     │  接收报价       │  监控进度          │
│   会做SEO的  │  agent          │  谈判契约       │  聚合结果          │
│   agent"     │                │                 │                  │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         链上契约层 (MyTask Contracts)                    │
│  TaskEscrowV2  ←  JuryContract  ←  MySBT  ←  MyShop  ←  x402           │
│  (资金托管)      (验证仲裁)       (身份)    (激励)    (支付)            │
└─────────────────────────────────────────────────────────────────────────┘
```

**一句话定位**:
> agent-speaker 是 **MyTask 的嘴巴和耳朵** —— 负责说话、听话、找人和商量事；MyTask 是 **大脑和账本** —— 负责记账、裁决和发钱。

---

## 3. 完整流程模拟

### 场景: 用户 Alice 想找人帮忙做网站 SEO

#### Step 1: 用户登录与身份 (MySBT + speaker Identity)
- Alice 在前端页面登录，关联她的 MySBT AgentId
- 前端同时调用 agent-speaker 的 identity 系统，加载 Alice 的 nostr 密钥对
- Alice 的 speaker 身份和链上 AgentId 建立映射

#### Step 2: 自然语言输入 (speaker TUI / Frontend)
- Alice 在 speaker TUI 中说："帮我找一个擅长 SEO 优化的 agent，预算 500 CNY，一周内完成"

#### Step 3: AI 意图解析 (speaker 本地 2B LLM)
- speaker 调用本地 LLM（如 Ollama + qwen2.5-1.5b）
- 提取标签：`["seo", "website-optimization", "content-marketing"]`
- 提取任务类型：`marketing`
- 提取预算：`500 CNY`
- 提取时限：`7 days`
- 生成结构化的 `TaskRequest`

#### Step 4: Agent 发现 (speaker → nostr relay)
- speaker 向 `relay.aastar.io` 发送订阅请求：
  - `kind: 30078` (Agent Profile)
  - 标签过滤：`c: ["agent"]` + 能力标签匹配
- relay 返回注册 agent 的元数据
- speaker 匹配出 Bob (SEO 专家) 和 Jack (内容营销专家)

#### Step 5: 主动协商 (speaker P2P 消息)
- speaker 自动向 Bob 和 Jack 发送 RFP (Request for Proposal)：
  - 消息 kind: `30079`
  - 内容包含任务描述、预算、deadline
  - NIP-44 端到端加密
- Bob 的 agent 24/7 在线，收到后：
  - 本地 LLM 分析任务
  - 自动生成报价 (Quote, kind: `30080`)
  - 回复 speaker: "可以接，报价 400 CNY，5 天完成"
- Jack 回复: "报价 450 CNY，但我提供完整的内容策略"

#### Step 6: 用户决策 (speaker TUI)
- Alice 在 TUI 中看到两个报价
- 选择 Bob，确认合作
- speaker 生成 `Contract` (kind: `30081`)
- 双方通过 nostr 消息电子签名确认

#### Step 7: 链上契约 (speaker → MyTask Contracts)
- speaker 调用 MyTask SDK / agent-mock
- 在链上执行 `TaskEscrowV2.createTask(...)`
  - community: Alice
  - taskor: Bob
  - reward: 400 CNY (锁定在 escrow)
- 任务进入链上生命周期

#### Step 8: 执行期通信 (speaker Group Chat)
- speaker 自动创建 "Alice-Bob SEO 项目" 协作群组
- Alice 和 Bob 在群组中实时沟通
- Bob 提交阶段性成果 → 上传到 IPFS → 返回 URI
- speaker 将 evidence URI 同步到链上 `submitEvidence(taskId, evidenceUri)`

#### Step 9: 验证与结算 (MyTask Jury + Escrow)
- Bob 完成后，链上进入 challenge period
- 若无争议，agent-mock 自动调用 `completeTask(taskId)`
- TaskEscrowV2 自动分配:
  - 70% → Bob (taskor)
  - 20% → Supplier (若使用了第三方资源)
  - 10% → Jury
- MyShop 触发 RewardAction，发放额外激励

#### Step 10: 结果回传 (speaker)
- speaker 从链上 indexer 获取完成状态
- 在 TUI 中通知 Alice: "任务已完成，资金已释放"
- 更新 Alice 和 Bob 的本地消息历史

---

## 4. 职责边界划分表

| 能力域 | agent-speaker | MyTask (Contracts + agent-mock) | 说明 |
|--------|---------------|----------------------------------|------|
| **身份注册** | nostr 密钥对管理 (nsec/npub) | MySBT 链上身份注册 | speaker 保管 nostr 私钥，MyTask 管链上身份 |
| **标签/能力** | Agent Profile (kind 30078) 的发布与搜索 | Registry 角色配置 (JURY/TASKER等) | speaker 管"技能标签"，MyTask 管"角色权限" |
| **发现匹配** | ✅ 搜索 relay 上的 agent | ❌ 不做 | speaker 独占 |
| **意图解析** | ✅ 本地 LLM 解析自然语言 | ❌ 不做 | speaker 独占 |
| **P2P通信** | ✅ nostr E2E 加密消息 | ❌ 不做 | speaker 独占 |
| **协商报价** | ✅ RFP/Quote/Contract 消息流 | ❌ 不做 | speaker 独占 |
| **资金托管** | ❌ 不做 | ✅ TaskEscrowV2 | MyTask 独占 |
| **验证仲裁** | ❌ 不做 | ✅ JuryContract | MyTask 独占 |
| **链上结算** | ❌ 不做 | ✅ Escrow + MyShop | MyTask 独占 |
| **进度监控** | 读取 indexer 展示在 TUI | 链上事件索引 | 协作：speaker 读，MyTask 写 |
| **激励发放** | 触发 agent-mock 执行奖励 | MyShop RewardAction | speaker 发起调用，MyTask 执行 |

---

## 5. 调整后的里程碑规划 (Milestone 2.0)

### Phase 2: Agent 身份与发现层 (Pre-Contract)

#### v0.25.0 — Agent Profile & Tag System
**目标**: 让 agent 能在 nostr relay 上注册自己的能力标签，并让其他 agent/用户能搜索到

**功能**:
- [ ] 扩展 `pkg/types` 增加 `AgentProfile`, `Capability`, `Availability`
- [ ] `agent-speaker agent register` 命令 — 发布 Kind 30078 Agent Profile 到 relay
- [ ] `agent-speaker agent update` — 更新 profile
- [ ] `agent-speaker agent info <npub>` — 查询某 agent 的 profile
- [ ] Agent Profile 与 MyTask MySBT AgentId 的关联字段
- [ ] SQLite `agent_profiles` 表缓存本地发现的 agent

**测试**: 8 Unit + 5 E2E (真实 relay 发布/查询)

---

#### v0.26.0 — Agent Discovery Engine
**目标**: 实现基于标签和需求的智能搜索匹配

**功能**:
- [ ] `internal/discovery` 包 — DiscoveryEngine
- [ ] 订阅 relay 的 `kind: 30078` 并建立本地索引
- [ ] 标签匹配算法（精确匹配 + 模糊匹配）
- [ ] 评分排序（能力匹配度、在线状态、历史评分预留接口）
- [ ] `agent-speaker discover --tags "seo,content" --budget 500 --currency CNY`
- [ ] 发现结果展示（CLI table / TUI list）

**测试**: 10 Unit + 5 E2E

---

### Phase 3: AI 与自动响应层

#### v0.27.0 — 本地 LLM 意图解析
**目标**: 集成本地小模型，把自然语言转为结构化的任务请求

**功能**:
- [ ] `internal/ai` 包 — IntentEngine
- [ ] 支持 Ollama API (默认) / OpenAI API (可选)
- [ ] Prompt 工程：任务分解、标签提取、预算识别、复杂度评估
- [ ] `agent-speaker ai parse "帮我找会做SEO的人，预算500"` → 输出 JSON
- [ ] 本地 Prompt 模板可配置
- [ ] 意图解析置信度过滤（低于阈值时人工确认）

**测试**: 6 Unit + 4 E2E

---

#### v0.28.0 — 自动响应 Agent (24/7 Daemon)
**目标**: 让 agent-speaker 成为真正"在线"的自动代理

**功能**:
- [ ] 扩展 `internal/daemon` 增加 AutoResponder
- [ ] 监听 incoming `kind: 30079` (Task RFP)
- [ ] 本地 LLM 自动分析 RFP，判断是否能承接
- [ ] 自动生成并发送 `kind: 30080` (Quote)
- [ ] 可配置响应策略（自动/半自动/手动）
- [ ] 离线消息队列， speaker 启动后批量处理

**测试**: 8 Unit + 6 E2E (Alice 发 RFP → Bob 的 daemon 自动回复)

---

### Phase 4: 委托协商与契约生成

#### v0.29.0 — Task Delegation Protocol
**目标**: 实现完整的链前协商流程

**功能**:
- [ ] `internal/delegation` 包 — 继承并改造 nak-src/delegate.go
- [ ] 定义 Nostr Event Kinds:
  - `30079` TaskRFP
  - `30080` TaskQuote
  - `30081` TaskContract
  - `30082` TaskProgress
  - `30083` TaskDelivery
- [ ] `agent-speaker delegate create --desc "..." --tags "seo" --budget 500`
- [ ] 自动发现 → 发送 RFP → 接收报价 → 展示选项 → 用户确认
- [ ] 生成 `Contract` 消息，双方 nostr 签名确认
- [ ] SQLite `delegations` 表跟踪任务状态机

**测试**: 12 Unit + 8 E2E

---

#### v0.30.0 — MyTask 合约桥接 & 协作执行
**目标**: 把 speaker 的协商结果落地到 MyTask 链上，并在执行期持续协调

**功能**:
- [ ] `internal/mytask-bridge` 包 — 调用 MyTask SDK / viem
- [ ] 从 speaker Contract 生成 MyTask `createTask` 参数
- [ ] 调用 agent-mock / SDK 在链上创建任务
- [ ] 执行期：将 `submitEvidence`, `completeTask` 等操作与 speaker 消息流打通
- [ ] TUI 中显示链上任务状态（读取 indexer API）
- [ ] 协作群组自动关联链上 taskId
- [ ] 结果交付后自动归档消息历史

**测试**: 8 Unit + 6 E2E (端到端：speaker 协商 → 链上 task → 完成结算)

---

### Phase 5: 生态增强 (未来)

| 版本 | 里程碑 | 说明 |
|------|--------|------|
| v0.31.0 | 声誉与评分 | 基于 MyTask Jury 结果生成 agent 声誉分，speaker 侧展示 |
| v0.32.0 | 多 agent 协作编排 | 一个任务自动拆解为子任务，分配给多个 agent，speaker 协调 |
| v0.33.0 | 语音/多模态输入 | 语音命令、图片上传等 |

---

## 6. 架构设计图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           agent-speaker (v0.25 - v0.30)                      │
│                                                                              │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐ │
│  │   CLI/TUI    │   │   Local LLM  │   │   Discovery  │   │  Delegation  │ │
│  │              │   │  (Ollama)    │   │   Engine     │   │   Protocol   │ │
│  │  chat/group  │   │              │   │              │   │              │ │
│  │  discover    │   │  intent      │   │  relay       │   │  RFP/Quote   │ │
│  │  delegate    │   │  parse       │   │  search      │   │  Contract    │ │
│  └──────┬───────┘   └──────┬───────┘   └──────┬───────┘   └──────┬───────┘ │
│         │                   │                   │                   │        │
│  ┌──────┴───────────────────┴───────────────────┴───────────────────┴──────┐ │
│  │                          internal/messaging                               │ │
│  │                     SQLite + nostr E2E encryption                         │ │
│  └────────────────────────────────────┬─────────────────────────────────────┘ │
└───────────────────────────────────────┼───────────────────────────────────────┘
                                        │
                        ┌───────────────┴───────────────┐
                        │       nostr relay             │
                        │    (wss://relay.aastar.io)    │
                        └───────────────┬───────────────┘
                                        │
┌───────────────────────────────────────┼───────────────────────────────────────┐
│                               MyTask Ecosystem                                │
│                                                                              │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐  │
│  │  agent-mock  │   │  TaskEscrowV2│   │ JuryContract │   │   MySBT      │  │
│  │  orchestrator│   │  (资金托管)   │   │  (验证仲裁)   │   │  (链上身份)   │  │
│  │  indexer.js  │   │              │   │              │   │              │  │
│  └──────────────┘   └──────────────┘   └──────────────┘   └──────────────┘  │
│                                                                              │
│  ┌──────────────┐   ┌──────────────┐                                         │
│  │  MyShopItems │   │   x402 Proxy │                                         │
│  │  (激励发放)   │   │  (gasless)   │                                         │
│  └──────────────┘   └──────────────┘                                         │
└───────────────────────────────────────────────────────────────────────────────┘
```

---

## 7. 技术风险与待定项

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| **本地 LLM 性能不足** | 意图解析延迟高或质量差 | 默认用 1.5B-3B 模型，允许 fallback 到云端 API；关键决策增加人工确认 |
| **Relay 搜索延迟/不可靠** | Agent 发现失败 | 本地 SQLite 缓存 + 定期刷新；支持多 relay 订阅 |
| **MyTask 合约接口变更** | Bridge 层需要重写 | 与 MyTask 团队约定稳定 ABI；bridge 层用接口封装隔离 |
| **nostr 消息丢失** | RFP/Quote 未送达 |  outbox 重试机制（已具备）+ 消息 ACK 机制 |
| **隐私与数据泄露** | Agent profile 暴露敏感信息 | Profile 只公开能力标签和定价范围，不暴露具体案例和客户信息 |

---

## 8. 总结

**agent-speaker Milestone 2.0 的核心使命**:

> 成为 **MyTask 生态的通信与发现基础设施**，用 nostr P2P 网络连接人与 AI Agent，用本地 LLM 理解用户意图，用端到端加密保障协商隐私，最终把达成的协作共识安全地写入 MyTask 链上合约。

**开发优先级**:
1. **最高**: v0.25 Agent Profile + v0.26 Discovery（没有发现和身份，后续都无从谈起）
2. **高**: v0.27 本地 LLM（意图解析是自动化的核心）
3. **中**: v0.28 AutoResponder + v0.29 Delegation（实现完整的协商闭环）
4. **中**: v0.30 MyTask Bridge（与现有 MyTask 生态正式打通）

**预计总工期**: 12-14 周（3-3.5 个月）
