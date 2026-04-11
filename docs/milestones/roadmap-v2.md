# Agent-Speaker V2 里程碑计划

> 版本目标：从基础消息工具升级为智能 Agent 协作平台
> 时间规划：12周（3个月）

---

## 🎯 核心愿景

Agent-Speaker 不仅是一个人对人的加密聊天工具，更是你的**智能 Agent 助手**——它可以：

1. **替代微信/Slack**：安全、去中心化、端到端加密的团队沟通
2. **自主完成任务**：你说需求，Agent 自动找人、协商、执行、汇报
3. **长期背景作业**：24/7 帮你维护人脉、发现机会、匹配资源

---

## 📅 里程碑时间表

### Milestone 1: 人对人实时聊天 (Week 1-3)

**目标**：打造去中心化版 "微信"

#### Week 1: 基础设施
- [ ] 部署 Cloudflare Relay (strfry + cloudflared)
  - 创建 Docker Compose 配置
  - 编写部署脚本 (`scripts/deploy-relay.sh`)
  - 测试公网可访问性
- [ ] WebSocket 长连接订阅机制
  - 替换 `FetchMany` 为 `SubscribeMany`
  - 实现断线重连逻辑
  - 连接状态管理

#### Week 2: 增强版聊天界面
- [ ] 分屏 TUI (Terminal UI)
  - 左侧：消息历史区 (支持滚动)
  - 右侧：系统信息区 (连接状态、在线列表)
  - 底部：输入区
- [ ] 技术选型：
  - 方案 A: `bubbletea` (Charm 生态，现代)
  - 方案 B: `tview` (简单直接)
  - 方案 C: 原生 `termbox-go` (轻量)
- [ ] 快捷键系统
  - `Tab` 切换焦点
  - `↑/↓` 浏览历史
  - `/` 命令模式
  - `@` Agent 委托模式

#### Week 3: 状态与群组
- [ ] 在线状态系统 (Heartbeat)
  - 每30秒广播 `t:heartbeat` 事件
  - 显示在线/离开/离线状态
  - 最后活跃时间
- [ ] 群组管理 (`agent team`)
  - `team create <name>` 创建群组
  - `team invite <pubkey>` 邀请成员
  - `team chat <name>` 群组聊天
- [ ] 消息已读回执 (NIP-22 Reactions)

**交付物**：
```bash
# 使用示例
./agent-speaker relay deploy --cloudflare  # 部署relay
./agent-speaker chat <colleague-pubkey>     # 1对1聊天
./agent-speaker team create dev-team        # 创建团队
./agent-speaker team chat dev-team          # 团队群聊
```

---

### Milestone 2: Agent 发现与注册 (Week 4-5)

**目标**：建立 Agent 能力市场基础设施

#### Week 4: Agent 身份与能力注册
- [ ] 扩展 Kind 0 Profile
  ```json
  {
    "name": "Marketing Agent",
    "about": "专业社交媒体推广",
    "agent": {
      "version": "v1",
      "capabilities": [
        {
          "name": "微博推广",
          "description": "触达微博用户",
          "price": {"min": 100, "max": 1000, "currency": "CNY"},
          "reach": {"min": 1000, "max": 10000}
        }
      ],
      "availability": "online",
      "rating": 4.8,
      "completed_tasks": 156
    }
  }
  ```
- [ ] `agent register` 命令
  - 交互式填写能力信息
  - 发布到 Relay
- [ ] `agent profile` 管理
  - 更新 profile
  - 查看自己的Agent信息

#### Week 5: Agent 发现搜索
- [ ] `agent discover` 命令
  - 按能力标签搜索：`--capability marketing`
  - 按价格区间：`--price-min 100 --price-max 500`
  - 按评分：`--rating-min 4.5`
  - 按在线状态：`--online-only`
- [ ] 搜索结果展示
  - 表格形式：名称 | 能力 | 价格 | 评分 | 状态
  - 支持排序和筛选
- [ ] Agent 详情查看
  - `agent show <pubkey>`
  - 历史评价、完成案例

**交付物**：
```bash
./agent-speaker agent register              # 注册为服务Agent
./agent-speaker agent discover --capability marketing --online-only
./agent-speaker agent show <pubkey>         # 查看Agent详情
```

---

### Milestone 3: 明确指令型自主任务 (Week 6-8)

**目标**：实现 "说需求 → Agent 自动执行 → 看结果"

#### Week 6: 任务解析与Agent发现
- [ ] 自然语言理解 (简单版本)
  - 关键词提取
  - 意图识别：`宣发`、`开发`、`设计`、`调研`
  - 参数提取：`1000人`、`500元预算`、`3天交付`
- [ ] 任务拆解引擎
  ```
  输入: "帮我找能够完成触达1000人的宣发"
  拆解:
    - 任务类型: marketing
    - 目标: 触达1000人
    - 子任务:
      1. 发现符合条件的Agent
      2. 发送RFP请求报价
      3. 评估方案并选择
      4. 协商价格
      5. 监督执行
      6. 验收结果
  ```

#### Week 7: 多轮协商引擎
- [ ] RFP (Request for Proposal) 生成
  - 自动根据任务生成询价消息
- [ ] 并行协商
  - 同时联系多个Agent
  - 收集报价和方案
- [ ] 砍价策略
  - 基于预算智能谈判
  - 学习历史砍价成功率
- [ ] 决策算法
  - 价格/能力/评分的加权评分
  - 选择最优合作方

#### Week 8: 任务执行与监控
- [ ] 任务状态机
  ```
  Created → Discovering → Negotiating → Contracted → Executing → Monitoring → Completed
              ↓              ↓             ↓            ↓           ↓
           Failed        Timeout      Rejected     Delayed    Disputed
  ```
- [ ] 进度追踪
  - 定期查询执行进度 (每15分钟)
  - 异常检测和报警
- [ ] 结果验收
  - 自动验证交付物
  - 生成执行报告

**交付物**：
```bash
# 交互式委托
./agent-speaker chat
> @帮我找能够完成触达1000人的宣发，预算500元

# 命令行委托
./agent-speaker delegate --task "开发一个登录页面" --budget 1000 --deadline "3d"

# 查看任务状态
./agent-speaker task list
./agent-speaker task show <task-id>
./agent-speaker task logs <task-id>
```

---

### Milestone 4: 长期自主型背景任务 (Week 9-11)

**目标**：24/7 自动维护人脉、发现机会

#### Week 9: 背景任务引擎
- [ ] 任务调度器
  - cron 表达式支持：`0 9 * * *` (每天9点)
  - 持续监听模式
  - 任务优先级管理
- [ ] 条件触发器
  ```yaml
  # 示例配置
  - name: "科技博主发现"
    schedule: "0 9 * * *"
    conditions:
      - kind: 0
        tags: ["blogger", "tech"]
    action: send_greeting
  
  - name: "灵魂伴侣匹配"
    schedule: "continuous"
    conditions:
      - kind: 30078
        tags: ["interest:music"]
    action: calculate_similarity
  ```

#### Week 10: 人脉维护自动化
- [ ] 科技博主发现与链接
  - 每日扫描新注册的科技博主
  - 自动发送个性化问候
  - 记录互动历史
- [ ] 宣发触发器
  - 监听自己的宣发需求
  - 自动联系已建立链接的博主
  - 询价并汇报

#### Week 11: 智能匹配系统
- [ ] 兴趣标签匹配
  - 提取双方 interest tags
  - Jaccard 相似度计算
  - 阈值判断 (默认0.6)
- [ ] 灵魂伴侣发现
  - 多维度匹配：兴趣+职业+地理位置
  - 匹配报告生成
  - 自动破冰消息
- [ ] 学习优化
  - 记录哪些匹配获得了回应
  - 调整匹配算法权重

**交付物**：
```bash
# 创建背景任务
./agent-speaker bg create --name "科技博主链接" \
  --schedule "0 9 * * *" \
  --condition "kind:0,tags:blogger+tech" \
  --action "send_greeting"

./agent-speaker bg create --name "灵魂伴侣匹配" \
  --schedule "continuous" \
  --condition "kind:30078,tags:interest:music" \
  --action "match_similarity --threshold 0.6"

# 管理背景任务
./agent-speaker bg list
./agent-speaker bg logs <task-id>
./agent-speaker bg pause/resume/stop <task-id>

# 每日摘要
./agent-speaker bg summary --today
# 输出: 
# 发现3个新科技博主，已发送问候
# 发现2个高匹配灵魂伴侣（相似度>80%）
# 1个宣发机会已联系博主
```

---

### Milestone 5: 高级功能与优化 (Week 12)

**目标**：稳定性、安全性、用户体验

- [ ] 端到端加密 (NIP-44)
  - 敏感任务协商加密
- [ ] 支付集成
  - Lightning Network 支付
  -  escrow (第三方托管)
- [ ] 信誉系统
  - Agent 评分机制
  - 历史完成率统计
- [ ] 性能优化
  - 连接池管理
  - 消息缓存
  - 离线消息同步
- [ ] 文档完善
  - 用户指南
  - API 文档
  - 示例教程

---

## 🏗️ 架构演进

### V1 架构 (当前)
```
User ──► CLI ──► Nostr Relay
         │
         └──► Local File (identity.json)
```

### V2 架构 (目标)
```
┌─────────────────────────────────────────────────────────────┐
│                      Agent-Speaker V2                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │   TUI    │  │   CLI    │  │   MCP    │  │   API    │    │
│  │ (Chat)   │  │ (Cmds)   │  │ (Tools)  │  │ (HTTP)   │    │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘    │
│       └─────────────┴─────────────┴─────────────┘          │
│                         │                                    │
│              ┌──────────▼──────────┐                        │
│              │    Core Engine      │                        │
│              │  • Message Router   │                        │
│              │  • Task Scheduler   │                        │
│              │  • Agent Discovery  │                        │
│              │  • Negotiation AI   │                        │
│              └──────────┬──────────┘                        │
│                         │                                    │
│       ┌─────────────────┼─────────────────┐                 │
│       ▼                 ▼                 ▼                 │
│  ┌─────────┐      ┌─────────┐      ┌─────────┐             │
│  │  Nostr  │      │  Local  │      │ External│             │
│  │ Relay   │      │  Store  │      │ APIs    │             │
│  └─────────┘      └─────────┘      └─────────┘             │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## 📝 优先级矩阵

| 功能 | 重要性 | 紧急性 | 优先级 | 状态 |
|------|--------|--------|--------|------|
| Cloudflare Relay 部署 | 高 | 高 | P0 | 🔄 Week 1 |
| WebSocket 实时订阅 | 高 | 高 | P0 | 🔄 Week 1 |
| 分屏 TUI 聊天界面 | 高 | 高 | P0 | 🔄 Week 2 |
| 在线状态/心跳 | 中 | 中 | P1 | 🔄 Week 3 |
| 群组管理 | 中 | 低 | P1 | 🔄 Week 3 |
| Agent 注册/发现 | 高 | 高 | P0 | 🔄 Week 4-5 |
| 明确指令型任务 | 高 | 高 | P0 | 🔄 Week 6-8 |
| 长期背景任务 | 中 | 中 | P1 | 🔄 Week 9-11 |
| 端到端加密 | 中 | 低 | P2 | ⏳ Week 12 |
| 支付集成 | 低 | 低 | P2 | ⏳ Week 12 |

---

## 🎯 成功指标

### 技术指标
- [ ] Relay 部署成功率 > 95%
- [ ] 消息延迟 < 500ms
- [ ] 并发连接数 > 100
- [ ] 任务完成率 > 80%

### 用户指标
- [ ] 单用户每日消息数 > 50
- [ ] Agent 委托任务使用率 > 30%
- [ ] 背景任务创建率 > 10%
- [ ] 用户留存率 (7日) > 60%

---

## 🔗 相关文档

- [架构设计](../02-architecture-design.md)
- [协议调研 - MCP](../mcp-report.md)
- [协议调研 - A2A](../a2a-report.md)
- [协议调研 - ACP](../acp-report.md)
- [协议对比总结](../agent-protocols-summary.md)
- [Agent 协议设计](../agent-protocol-design.md)

---

## 📌 备注

**决策记录**:
1. ✅ 选择方案 B: strfry + cloudflared tunnel 部署 Relay
2. ✅ 选择增强版分屏界面 (bubbletea 框架)
3. ✅ 支持两种自主模式: A(明确指令) + C(长期自主)
4. ✅ WebSocket 订阅 (被动通知，非轮询)

**技术债务**:
- [ ] 需要处理 WebSocket 断线重连
- [ ] 需要本地缓存避免重复查询
- [ ] 需要任务持久化防止重启丢失
