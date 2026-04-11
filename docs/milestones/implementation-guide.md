# V2 实现指南

> 详细的开发任务分解和技术方案

---

## 1. WebSocket 实时订阅机制

### 1.1 当前问题

当前代码使用 `FetchMany` 是**单次查询**：
```go
// agent.go:267 - 10秒超时后结束
events := sys.Pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{})
for ie := range events {
    // 处理事件
}
// 连接关闭
```

### 1.2 目标方案

使用 `SubscribeMany` 保持**长连接**：
```go
// 新的 subscribe.go
package main

import (
    "context"
    "fmt"
    "time"
    
    "fiatjaf.com/nostr"
)

// SubscriptionManager 管理多个 relay 的订阅
type SubscriptionManager struct {
    sys       *nostr.System
    relays    []string
    filters   []nostr.Filter
    handler   func(nostr.Event)
    reconnect bool
}

func NewSubscriptionManager(sys *nostr.System) *SubscriptionManager {
    return &SubscriptionManager{
        sys:       sys,
        reconnect: true,
    }
}

// Subscribe 开始订阅，保持长连接
func (sm *SubscriptionManager) Subscribe(ctx context.Context, relays []string, filter nostr.Filter) error {
    sm.relays = relays
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }
        
        // 使用 SubscribeMany 建立长连接
        sub := sm.sys.Pool.SubscribeMany(ctx, relays, filter, nostr.SubscriptionOptions{})
        
        // 处理事件流
        err := sm.handleSubscription(ctx, sub)
        
        if !sm.reconnect {
            return err
        }
        
        // 断线重连
        fmt.Println("Connection lost, reconnecting in 5s...")
        time.Sleep(5 * time.Second)
    }
}

func (sm *SubscriptionManager) handleSubscription(ctx context.Context, sub nostr.Subscription) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case evt, ok := <-sub.Events:
            if !ok {
                return fmt.Errorf("subscription closed")
            }
            if sm.handler != nil {
                sm.handler(evt)
            }
        case err := <-sub.Errors:
            return err
        }
    }
}
```

### 1.3 断线重连策略

```go
type ReconnectPolicy struct {
    MaxRetries    int           // 最大重试次数 (0=无限)
    InitialDelay  time.Duration // 初始延迟 (1s)
    MaxDelay      time.Duration // 最大延迟 (60s)
    BackoffFactor float64       // 退避因子 (2.0)
}

func (rp *ReconnectPolicy) NextDelay(attempt int) time.Duration {
    delay := rp.InitialDelay * time.Duration(math.Pow(rp.BackoffFactor, float64(attempt)))
    if delay > rp.MaxDelay {
        delay = rp.MaxDelay
    }
    return delay
}
```

---

## 2. 分屏 TUI 界面

### 2.1 技术选型: Bubbletea

选择 [bubbletea](https://github.com/charmbracelet/bubbletea) (Charm 生态)：
- ✅ 现代化的 TUI 框架
- ✅ 支持复杂布局
- ✅ 响应式更新
- ✅ 与 Lipgloss 样式库配合

### 2.2 界面结构

```go
// chat/tui.go
package chat

import (
    "github.com/charmbracelet/bubbles/textinput"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

type Model struct {
    // 组件
    viewport    viewport.Model    // 左侧：消息历史
    textInput   textinput.Model   // 底部：输入框
    sidebar     SidebarModel      // 右侧：系统信息
    
    // 状态
    messages    []Message
    peer        PeerInfo
    connection  ConnectionState
    focus       FocusArea
    
    // 业务逻辑
    subManager  *SubscriptionManager
    relayURLs   []string
}

type Message struct {
    ID        string
    From      string
    Content   string
    Timestamp time.Time
    IsMe      bool
    Status    MessageStatus // sent, delivered, read
}

type FocusArea int
const (
    FocusInput FocusArea = iota
    FocusHistory
    FocusSidebar
)
```

### 2.3 布局渲染

```go
func (m Model) View() string {
    // 左侧消息区 (70%宽度)
    historyView := m.renderHistory()
    
    // 右侧信息区 (30%宽度)
    sidebarView := m.renderSidebar()
    
    // 底部输入区
    inputView := m.renderInput()
    
    // 合并布局
    mainArea := lipgloss.JoinHorizontal(
        lipgloss.Top,
        historyView,
        sidebarView,
    )
    
    return lipgloss.JoinVertical(
        lipgloss.Left,
        m.renderHeader(),
        mainArea,
        inputView,
    )
}

func (m Model) renderHistory() string {
    var content strings.Builder
    
    for _, msg := range m.messages {
        style := m.styles.received
        prefix := "👤"
        if msg.IsMe {
            style = m.styles.sent
            prefix = "→"
        }
        
        timeStr := msg.Timestamp.Format("15:04:05")
        line := fmt.Sprintf("[%s] %s %s\n", timeStr, prefix, msg.Content)
        content.WriteString(style.Render(line))
    }
    
    m.viewport.SetContent(content.String())
    return m.styles.history.Render(m.viewport.View())
}

func (m Model) renderSidebar() string {
    sections := []string{
        m.renderConnectionStatus(),
        m.renderPeerInfo(),
        m.renderActiveTasks(),
        m.renderShortcuts(),
    }
    return m.styles.sidebar.Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
}
```

### 2.4 消息处理循环

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "tab":
            m.focus = (m.focus + 1) % 3
            return m, nil
        case "/":
            m.textInput.SetValue("/")
            m.focus = FocusInput
            return m, nil
        case "@":
            m.textInput.SetValue("@")
            m.focus = FocusInput
            return m, m.startAgentMode()
        case "enter":
            if m.focus == FocusInput {
                return m, m.sendMessage()
            }
        case "q", "ctrl+c":
            if m.focus != FocusInput {
                return m, tea.Quit
            }
        }
        
    case NostrEventMsg:
        // 收到新消息
        m.messages = append(m.messages, convertEvent(msg.Event))
        m.viewport.GotoBottom()
        return m, nil
        
    case ConnectionStatusMsg:
        m.connection = msg.Status
        return m, nil
        
    case TaskUpdateMsg:
        m.activeTasks = updateTasks(m.activeTasks, msg.Task)
        return m, nil
    }
    
    // 更新子组件
    var cmd tea.Cmd
    m.textInput, cmd = m.textInput.Update(msg)
    m.viewport, _ = m.viewport.Update(msg)
    
    return m, cmd
}
```

### 2.5 快捷键设计

| 按键 | 功能 |
|------|------|
| `Tab` | 切换焦点 (输入框/历史区/侧边栏) |
| `↑/↓` | 在历史区滚动消息 |
| `Enter` | 发送消息 |
| `/` | 进入命令模式 |
| `@` | 进入 Agent 委托模式 |
| `Esc` | 取消当前操作 |
| `Ctrl+C` | 退出程序 |
| `Ctrl+L` | 清屏 |
| `Ctrl+R` | 手动重连 |

---

## 3. Agent 自主任务系统

### 3.1 任务状态机

```go
// task/statemachine.go
type TaskState int

const (
    TaskCreated TaskState = iota
    TaskDiscovering      // 发现候选Agent
    TaskNegotiating      // 协商中
    TaskContracted       // 已签约
    TaskExecuting        // 执行中
    TaskMonitoring       // 监控中
    TaskCompleted        // 完成
    TaskFailed           // 失败
    TaskTimeout          // 超时
)

type Task struct {
    ID          string
    Type        TaskType
    State       TaskState
    Description string
    Requirements TaskRequirements
    
    // 执行信息
    Candidates  []AgentInfo      // 候选Agent
    Selected    *AgentInfo       // 选中的Agent
    Negotiations []Negotiation   // 协商记录
    Contract    *Contract        // 合约
    
    // 监控信息
    StartTime   time.Time
    Deadline    time.Time
    Progress    float64
    Logs        []TaskLog
    Result      *TaskResult
    
    // 控制
    cancelFunc  context.CancelFunc
}
```

### 3.2 任务执行引擎

```go
// task/engine.go
type TaskEngine struct {
    tasks      map[string]*Task
    relayPool  *nostr.System
    discovery  *AgentDiscovery
    negotiator *Negotiator
    monitor    *TaskMonitor
}

func (te *TaskEngine) ExecuteTask(ctx context.Context, task *Task) error {
    // 1. 解析需求
    requirements := te.parseRequirements(task.Description)
    
    // 2. 发现候选Agent
    candidates, err := te.discovery.FindAgents(ctx, requirements)
    if err != nil {
        return te.transition(task, TaskFailed, err)
    }
    task.Candidates = candidates
    te.transition(task, TaskDiscovering, nil)
    
    // 3. 并行协商
    if len(candidates) == 0 {
        return te.transition(task, TaskFailed, fmt.Errorf("no agents found"))
    }
    
    te.transition(task, TaskNegotiating, nil)
    negotiations := te.negotiator.NegotiateParallel(ctx, task, candidates)
    task.Negotiations = negotiations
    
    // 4. 选择最优Agent
    selected := te.selectBestAgent(negotiations)
    if selected == nil {
        return te.transition(task, TaskFailed, fmt.Errorf("no suitable agent"))
    }
    task.Selected = selected.Agent
    te.transition(task, TaskContracted, nil)
    
    // 5. 执行与监控
    return te.executeAndMonitor(ctx, task)
}

func (te *TaskEngine) executeAndMonitor(ctx context.Context, task *Task) error {
    te.transition(task, TaskExecuting, nil)
    
    // 发送执行任务
    err := te.sendTaskToAgent(ctx, task)
    if err != nil {
        return te.transition(task, TaskFailed, err)
    }
    
    // 监控进度
    te.transition(task, TaskMonitoring, nil)
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return te.transition(task, TaskTimeout, ctx.Err())
            
        case <-ticker.C:
            progress, err := te.checkProgress(ctx, task)
            if err != nil {
                task.Logs = append(task.Logs, TaskLog{
                    Time:    time.Now(),
                    Level:   "error",
                    Message: err.Error(),
                })
                continue
            }
            
            task.Progress = progress
            if progress >= 100 {
                result, err := te.collectResult(ctx, task)
                if err != nil {
                    return te.transition(task, TaskFailed, err)
                }
                task.Result = result
                return te.transition(task, TaskCompleted, nil)
            }
        }
    }
}
```

### 3.3 协商策略

```go
// task/negotiator.go
type Negotiator struct {
    maxConcurrent int           // 最大并发协商数
    timeout       time.Duration // 协商超时
}

type Negotiation struct {
    Agent      AgentInfo
    Proposal   Proposal    // Agent的报价
    Counter    *Proposal   // 我们的还价
    Rounds     int         // 协商轮数
    Accepted   bool        // 是否接受
    Reason     string      // 拒绝原因
}

type Proposal struct {
    Price       float64
    Timeline    time.Duration
    Deliverables []string
    Conditions  map[string]string
}

func (n *Negotiator) NegotiateParallel(ctx context.Context, task *Task, candidates []AgentInfo) []Negotiation {
    var wg sync.WaitGroup
    results := make(chan Negotiation, len(candidates))
    semaphore := make(chan struct{}, n.maxConcurrent)
    
    for _, agent := range candidates {
        wg.Add(1)
        go func(a AgentInfo) {
            defer wg.Done()
            
            semaphore <- struct{}{}
            defer func() { <-semaphore }()
            
            neg := n.negotiate(ctx, task, a)
            results <- neg
        }(agent)
    }
    
    go func() {
        wg.Wait()
        close(results)
    }()
    
    var negotiations []Negotiation
    for neg := range results {
        negotiations = append(negotiations, neg)
    }
    
    return negotiations
}

func (n *Negotiator) negotiate(ctx context.Context, task *Task, agent AgentInfo) Negotiation {
    negotiation := Negotiation{Agent: agent, Rounds: 0}
    
    // 第一轮：发送RFP
    rfp := n.buildRFP(task)
    proposal, err := n.sendRFP(ctx, agent, rfp)
    if err != nil {
        negotiation.Reason = err.Error()
        return negotiation
    }
    negotiation.Proposal = proposal
    negotiation.Rounds = 1
    
    // 第二轮：砍价（如果需要）
    if proposal.Price > task.Requirements.MaxBudget {
        counter := n.buildCounter(proposal, task.Requirements)
        response, err := n.sendCounter(ctx, agent, counter)
        if err != nil {
            negotiation.Reason = "counter rejected"
            return negotiation
        }
        negotiation.Counter = &counter
        negotiation.Rounds = 2
        
        if response.Accepted {
            negotiation.Accepted = true
        }
    } else {
        negotiation.Accepted = true
    }
    
    return negotiation
}

func (n *Negotiator) buildRFP(task *Task) RFP {
    return RFP{
        TaskID:      task.ID,
        Description: task.Description,
        Requirements: task.Requirements,
        BudgetRange: BudgetRange{
            Min: task.Requirements.MinBudget,
            Max: task.Requirements.MaxBudget,
        },
        Deadline: task.Deadline,
    }
}
```

---

## 4. 长期背景任务

### 4.1 任务调度器

```go
// background/scheduler.go
type Scheduler struct {
    tasks    map[string]*BackgroundTask
    cron     *cron.Cron
    executor *TaskExecutor
}

type BackgroundTask struct {
    ID       string
    Name     string
    Type     BackgroundType
    Schedule Schedule
    Conditions []Condition
    Actions    []Action
    Status     BackgroundStatus
    History    []ExecutionRecord
}

type Schedule struct {
    Type     string  // "cron", "interval", "continuous"
    CronExpr string  // for cron type
    Interval int     // for interval type (seconds)
}

type Condition struct {
    Kind     int
    Tags     []string
    Authors  []string
    ContentRegex string
    Metadata map[string]string
}

func (s *Scheduler) RegisterTask(task *BackgroundTask) error {
    switch task.Schedule.Type {
    case "cron":
        _, err := s.cron.AddFunc(task.Schedule.CronExpr, func() {
            s.execute(task)
        })
        return err
        
    case "interval":
        ticker := time.NewTicker(time.Duration(task.Schedule.Interval) * time.Second)
        go func() {
            for range ticker.C {
                s.execute(task)
            }
        }()
        
    case "continuous":
        // 持续监听，创建订阅
        go s.continuousListen(task)
    }
    
    s.tasks[task.ID] = task
    return nil
}

func (s *Scheduler) continuousListen(task *BackgroundTask) {
    filter := s.buildFilter(task.Conditions)
    
    sub := s.executor.sys.Pool.SubscribeMany(context.Background(), 
        s.executor.relays, filter, nostr.SubscriptionOptions{})
    
    for evt := range sub.Events {
        if s.matchesConditions(evt, task.Conditions) {
            s.executeAction(task, evt)
        }
    }
}

func (s *Scheduler) execute(task *BackgroundTask) {
    record := ExecutionRecord{
        Time:   time.Now(),
        Status: "running",
    }
    
    // 发现匹配的事件
    events := s.discover(task.Conditions)
    
    for _, evt := range events {
        s.executeAction(task, evt)
    }
    
    record.Status = "completed"
    record.EventsFound = len(events)
    task.History = append(task.History, record)
}
```

### 4.2 匹配算法

```go
// background/matcher.go

// Jaccard 相似度计算
func JaccardSimilarity(set1, set2 []string) float64 {
    set1Map := make(map[string]bool)
    for _, v := range set1 {
        set1Map[v] = true
    }
    
    intersection := 0
    for _, v := range set2 {
        if set1Map[v] {
            intersection++
        }
    }
    
    union := len(set1Map) + len(set2) - intersection
    if union == 0 {
        return 0
    }
    
    return float64(intersection) / float64(union)
}

// 多维度匹配
type MatchResult struct {
    Target      string
    Score       float64
    Dimensions  map[string]float64
}

func (m *Matcher) CalculateMatch(myProfile, targetProfile Profile) MatchResult {
    dimensions := map[string]float64{
        "interests":   JaccardSimilarity(myProfile.Interests, targetProfile.Interests),
        "skills":      JaccardSimilarity(myProfile.Skills, targetProfile.Skills),
        "location":    locationScore(myProfile.Location, targetProfile.Location),
        "activity":    activityScore(targetProfile.LastActive),
    }
    
    // 加权总分
    weights := map[string]float64{
        "interests": 0.4,
        "skills":    0.3,
        "location":  0.2,
        "activity":  0.1,
    }
    
    var totalScore float64
    for dim, score := range dimensions {
        totalScore += score * weights[dim]
    }
    
    return MatchResult{
        Target:     targetProfile.Pubkey,
        Score:      totalScore,
        Dimensions: dimensions,
    }
}
```

### 4.3 典型背景任务配置

```yaml
# config/background-tasks.yaml
tasks:
  - id: "tech-blogger-discovery"
    name: "科技博主发现与链接"
    schedule:
      type: "cron"
      cron_expr: "0 9 * * *"  # 每天9点
    conditions:
      - kind: 0
        tags: ["blogger", "tech", "developer"]
    actions:
      - type: "send_greeting"
        template: |
          Hi {{.Name}}，
          我是 {{.MyName}}，关注科技内容创作。
          希望保持联系，有机会可以合作！
      - type: "add_to_contact"
        category: "tech-bloggers"
    
  - id: "soulmate-matching"
    name: "灵魂伴侣匹配"
    schedule:
      type: "continuous"  # 持续监听
    conditions:
      - kind: 30078
        tags: ["interest:music", "interest:art"]
    actions:
      - type: "calculate_similarity"
        threshold: 0.6
      - type: "send_match_request"
        template: |
          🎵 发现我们有很多共同兴趣！
          匹配度: {{.Score}}%
          共同兴趣: {{.CommonInterests}}
          可以认识一下吗？
    
  - id: "marketing-trigger"
    name: "宣发需求触发"
    schedule:
      type: "continuous"
    conditions:
      - kind: 30078
        content_regex: "(宣发|推广|营销|发布)"
        authors: ["myself"]  # 只监听自己的消息
    actions:
      - type: "query_contacts"
        category: "tech-bloggers"
      - type: "send_broadcast"
        template: |
          Hi，之前有联系过。
          现在有宣发需求：{{.TaskDescription}}
          有兴趣合作吗？报价多少？
```

---

## 5. 数据存储

### 5.1 本地存储结构

```
~/.agent-speaker/
├── identity.json          # 身份信息 (已存在)
├── config.yaml            # 配置文件
├── local-db/
│   ├── messages/          # 本地消息缓存
│   │   ├── 2024-01/
│   │   └── 2024-02/
│   ├── contacts/          # 联系人
│   │   └── tech-bloggers.json
│   └── tasks/             # 任务记录
│       ├── active/
│       └── completed/
└── logs/
    └── agent-speaker.log
```

### 5.2 消息缓存

```go
// storage/message_cache.go
type MessageCache struct {
    db *bbolt.DB
}

func (mc *MessageCache) Store(event nostr.Event) error {
    return mc.db.Update(func(tx *bbolt.Tx) error {
        bucket := tx.Bucket([]byte("messages"))
        key := []byte(event.ID)
        value, _ := json.Marshal(event)
        return bucket.Put(key, value)
    })
}

func (mc *MessageCache) Query(filter nostr.Filter) ([]nostr.Event, error) {
    var events []nostr.Event
    
    mc.db.View(func(tx *bbolt.Tx) error {
        bucket := tx.Bucket([]byte("messages"))
        
        return bucket.ForEach(func(k, v []byte) error {
            var event nostr.Event
            json.Unmarshal(v, &event)
            
            if matchesFilter(event, filter) {
                events = append(events, event)
            }
            return nil
        })
    })
    
    return events, nil
}
```

---

## 6. 测试策略

### 6.1 单元测试

```go
// task/task_test.go
func TestTaskStateMachine(t *testing.T) {
    task := &Task{
        ID:   "test-123",
        Type: TaskMarketing,
    }
    
    // 测试状态转换
    err := task.Transition(TaskDiscovering, nil)
    assert.NoError(t, err)
    assert.Equal(t, TaskDiscovering, task.State)
    
    err = task.Transition(TaskFailed, fmt.Errorf("no agents"))
    assert.NoError(t, err)
    assert.Equal(t, TaskFailed, task.State)
}

func TestNegotiation(t *testing.T) {
    negotiator := &Negotiator{
        maxConcurrent: 3,
        timeout:       30 * time.Second,
    }
    
    proposal := Proposal{Price: 1000}
    counter := negotiator.buildCounter(proposal, Requirements{MaxBudget: 800})
    
    assert.Less(t, counter.Price, proposal.Price)
}
```

### 6.2 集成测试

```go
// test/integration_chat_test.go
func TestChatSession(t *testing.T) {
    // 启动测试 relay
    relay := startTestRelay()
    defer relay.Stop()
    
    // 创建两个用户
    alice := createTestUser("alice")
    bob := createTestUser("bob")
    
    // Alice 发送消息
    msg := Message{
        To:      bob.Pubkey,
        Content: "Hello Bob!",
    }
    err := alice.Send(msg)
    assert.NoError(t, err)
    
    // Bob 接收消息
    received, err := bob.Receive(5 * time.Second)
    assert.NoError(t, err)
    assert.Equal(t, "Hello Bob!", received.Content)
}
```

---

## 7. 部署清单

### 7.1 开发环境

```bash
# 1. 安装依赖
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/lipgloss
go get github.com/robfig/cron/v3
go get go.etcd.io/bbolt

# 2. 构建
make build

# 3. 测试
make test-all
```

### 7.2 生产部署

```bash
# 1. 部署 Relay
docker run -d \
  --name agent-relay \
  -p 7777:7777 \
  -v /data/relay:/app/strfry-db \
  hoytech/strfry:latest

# 2. 配置 cloudflared
cloudflared tunnel create agent-relay
cloudflared tunnel route dns agent-relay relay.yourdomain.com
cloudflared tunnel run agent-relay

# 3. 配置域名
# relay.yourdomain.com → cloudflare tunnel → localhost:7777
```

---

## 📎 附录

### A. 命令速查表

```bash
# 人对人聊天
./agent-speaker chat <pubkey>              # 启动聊天界面
./agent-speaker msg --to <pubkey> "msg"    # 快速发送

# Agent 委托
./agent-speaker delegate --task "xxx" --budget 1000
./agent-speaker task list                  # 查看任务
./agent-speaker task logs <id>             # 查看日志

# 背景任务
./agent-speaker bg create --name "xxx" --schedule "0 9 * * *"
./agent-speaker bg list
./agent-speaker bg logs <id>

# Agent 发现
./agent-speaker agent register             # 注册自己
./agent-speaker agent discover --capability marketing
./agent-speaker agent show <pubkey>
```

### B. 配置文件示例

```yaml
# ~/.agent-speaker/config.yaml
identity:
  private_key_file: ~/.agent-speaker/identity.json

relays:
  primary: wss://relay.yourdomain.com
  fallback:
    - wss://relay.damus.io
    - wss://nos.lol

chat:
  history_limit: 1000
  auto_decompress: true
  notification: true

tasks:
  default_timeout: 24h
  max_concurrent: 5
  auto_retry: true

background:
  enabled: true
  daily_summary: "09:00"
  max_active_tasks: 10
```
