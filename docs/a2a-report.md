# A2A (Agent-to-Agent Protocol) 调研报告

## 1. 概述

**A2A (Agent-to-Agent Protocol)** 是由 **Google** 于 **2025年4月** 发布的开放协议，旨在解决不同框架、不同厂商构建的 AI Agent 之间的互操作性问题。

### 核心定位
> "让 AI Agent 能够像人一样相互协作，无论它们使用什么框架构建。"

| 属性 | 详情 |
|------|------|
| **发布方** | Google Cloud |
| **发布时间** | 2025年4月 |
| **治理机构** | Linux Foundation (2025年6月捐赠) |
| **协议层级** | Agent ↔ Agent (水平协作) |
| **核心用途** | 跨框架、跨厂商的 Agent 间通信与协作 |

### 核心设计原则
1. **拥抱 Agent 能力** - 支持 Agent 以自然、非结构化方式协作
2. **基于现有标准** - 使用 HTTP、SSE、JSON-RPC 等成熟技术
3. **默认安全** - 企业级认证授权，兼容 OpenAPI 安全方案
4. **支持长时任务** - 从即时响应到数天的复杂任务
5. **模态无关** - 支持文本、音频、视频等多种交互形式

---

## 2. 核心架构

```
┌─────────────────────────────────────────────────────────────────┐
│                     A2A Architecture                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│   ┌──────────────────┐         ┌──────────────────┐            │
│   │   Client Agent   │◄───────►│   Remote Agent   │            │
│   │                  │   A2A   │                  │            │
│   │  • Formulates    │ Protocol│  • Executes      │            │
│   │    tasks         │         │    tasks         │            │
│   │  • Communicates  │         │  • Returns       │            │
│   │    requests      │         │    artifacts     │            │
│   └──────────────────┘         └──────────────────┘            │
│           │                              │                      │
│           ▼                              ▼                      │
│   ┌──────────────────┐         ┌──────────────────┐            │
│   │    Agent Card    │         │    Agent Card    │            │
│   │  (Capability     │         │  (Capability     │            │
│   │   Discovery)     │         │   Advertisement) │            │
│   └──────────────────┘         └──────────────────┘            │
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐  │
│   │              Transport Layer                             │  │
│   │   HTTP/HTTPS + JSON-RPC 2.0 + SSE (Server-Sent Events)   │  │
│   └─────────────────────────────────────────────────────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 通信协议栈
| 层级 | 技术 |
|------|------|
| 应用层 | JSON-RPC 2.0 |
| 传输层 | HTTP/HTTPS |
| 流式通信 | Server-Sent Events (SSE) |
| 认证层 | OpenAPI 安全方案 |

---

## 3. 核心概念

### 3.1 Agent Card (能力卡片)
Agent Card 是 Agent 的能力描述文件，用于服务发现：

```json
{
  "name": "contract-review-agent",
  "description": "Review legal contracts for compliance",
  "version": "1.0.0",
  "url": "https://agents.example.com/contract-review",
  "capabilities": {
    "streaming": true,
    "pushNotifications": true,
    "stateTransitionHistory": true
  },
  "skills": [
    {
      "id": "contract-review",
      "name": "Contract Review",
      "description": "Analyze contracts for legal compliance",
      "inputModes": ["text", "file"],
      "outputModes": ["text", "structured"]
    }
  ],
  "authentication": {
    "type": "oauth2",
    "flows": ["client_credentials"]
  }
}
```

### 3.2 Task (任务)
Task 是 Agent 间协作的基本单元：

```json
{
  "id": "task-12345",
  "status": "in_progress",
  "createdAt": "2025-04-10T10:00:00Z",
  "updatedAt": "2025-04-10T10:05:00Z",
  "sessionId": "session-67890",
  "messages": [
    {
      "role": "user",
      "parts": [
        {
          "type": "text",
          "content": "Review this contract for compliance issues"
        },
        {
          "type": "file",
          "mimeType": "application/pdf",
          "uri": "https://storage.example.com/contract.pdf"
        }
      ]
    }
  ],
  "artifacts": []
}
```

### 3.3 Message (消息)
消息支持多种内容类型 (Parts)：

```json
{
  "role": "agent",
  "parts": [
    {
      "type": "text",
      "content": "I've found 3 compliance issues in the contract."
    },
    {
      "type": "structured",
      "mimeType": "application/json",
      "data": {
        "issues": [
          {"severity": "high", "clause": "Indemnification"},
          {"severity": "medium", "clause": "Termination"}
        ]
      }
    }
  ]
}
```

---

## 4. 代码框架与实现

### 4.1 官方 SDK

| 语言 | 包名 | 安装命令 |
|------|------|---------|
| Python | a2a-sdk | `pip install a2a-sdk` |
| Go | a2a-go | `go get github.com/a2aproject/a2a-go` |
| JavaScript | @a2a-js/sdk | `npm install @a2a-js/sdk` |
| Java | a2a-java | Maven |
| .NET | A2A | `dotnet add package A2A` |

### 4.2 Python SDK 示例

#### 创建 A2A Agent Server
```python
from a2a.server import A2AServer
from a2a.types import AgentCard, Skill, Message, TextPart

# Define agent capabilities
agent_card = AgentCard(
    name="document-processor",
    description="Process and analyze documents",
    version="1.0.0",
    url="http://localhost:8000",
    capabilities={
        "streaming": True,
        "pushNotifications": False
    },
    skills=[
        Skill(
            id="summarize",
            name="Document Summarization",
            description="Summarize long documents",
            inputModes=["text", "file"],
            outputModes=["text"]
        )
    ]
)

# Create server
server = A2AServer(agent_card=agent_card)

@server.on_task
async def handle_task(task):
    """Handle incoming tasks."""
    # Process the task
    content = extract_content(task.messages)
    summary = await generate_summary(content)
    
    # Return result
    return Message(
        role="agent",
        parts=[TextPart(type="text", content=summary)]
    )

# Start server
if __name__ == "__main__":
    server.run(host="0.0.0.0", port=8000)
```

#### 创建 A2A Client
```python
from a2a.client import A2AClient

# Connect to remote agent
client = A2AClient(
    agent_card_url="https://agents.example.com/agent-card.json"
)

# Fetch agent capabilities
agent_info = await client.get_agent_card()

# Create and send task
task = await client.create_task(
    session_id="session-123",
    message={
        "role": "user",
        "parts": [
            {"type": "text", "content": "Summarize this document"},
            {"type": "file", "uri": "https://example.com/doc.pdf"}
        ]
    }
)

# Get task result (blocking)
result = await client.wait_for_completion(task.id)

# Or stream updates
async for update in client.stream_task(task.id):
    print(f"Status: {update.status}")
    if update.artifacts:
        print(f"Artifacts: {update.artifacts}")
```

### 4.3 Go SDK 示例
```go
package main

import (
    "context"
    "log"
    "github.com/a2aproject/a2a-go/pkg/server"
    "github.com/a2aproject/a2a-go/pkg/types"
)

func main() {
    // Create agent card
    card := &types.AgentCard{
        Name:        "data-analyzer",
        Description: "Analyze datasets and generate insights",
        Version:     "1.0.0",
        URL:         "http://localhost:8080",
        Capabilities: &types.AgentCapabilities{
            Streaming: true,
        },
    }

    // Create server
    s := server.New(card)

    // Register task handler
    s.HandleTask(func(ctx context.Context, task *types.Task) (*types.Task, error) {
        // Process task
        result := processData(task)
        
        return &types.Task{
            ID:     task.ID,
            Status: types.TaskStatusCompleted,
            Artifacts: []types.Artifact{
                {
                    Type: "structured",
                    Data: result,
                },
            },
        }, nil
    })

    // Start server
    log.Fatal(s.Start(":8080"))
}
```

---

## 5. 生态与采用情况

### 5.1 合作伙伴 (50+)

**技术合作伙伴**:
- Atlassian, Box, Cohere, Intuit, LangChain, MongoDB
- PayPal, Salesforce, SAP, ServiceNow, Workday

**服务合作伙伴**:
- Accenture, BCG, Capgemini, Cognizant, Deloitte
- HCLTech, Infosys, KPMG, McKinsey, PwC, TCS, Wipro

### 5.2 应用场景

#### 招聘流程自动化
```
Hiring Manager Agent
       │
       ▼
┌─────────────────┐
│ Candidate       │◄──────┐
│ Sourcing Agent  │       │
└─────────────────┘       │
       │                  │
       ▼                  │
┌─────────────────┐       │
│ Interview       │       │
│ Scheduler Agent │───────┤
└─────────────────┘       │
       │                  │
       ▼                  │
┌─────────────────┐       │
│ Background      │       │
│ Check Agent     │───────┘
└─────────────────┘
```

---

## 6. 协议规范要点

### 6.1 通信模式
| 模式 | 描述 | 适用场景 |
|------|------|---------|
| **同步** | 请求-响应，立即返回 | 简单查询、快速任务 |
| **流式** | SSE 实时推送 | 长任务进度更新 |
| **异步** | 任务创建 + 推送通知 | 耗时数小时的任务 |

### 6.2 Task 生命周期
```
submitted → working → input_required → working → completed
                │                           │
                ▼                           ▼
            canceled                    failed
```

### 6.3 消息类型
| 类型 | 用途 |
|------|------|
| `text` | 纯文本内容 |
| `file` | 文件引用 |
| `structured` | 结构化数据 (JSON) |
| `data` | 二进制数据 |

---

## 7. 与 MCP 的关系

```
┌─────────────────────────────────────────────────────────────┐
│                     Protocol Stack                           │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│   ┌─────────────────────────────────────────────────────┐  │
│   │                    A2A Layer                         │  │
│   │         (Agent-to-Agent Collaboration)               │  │
│   │                                                      │  │
│   │   ┌─────────┐      A2A Protocol     ┌─────────┐     │  │
│   │   │ Agent A │◄─────────────────────►│ Agent B │     │  │
│   │   │  ┌───┐  │                       │  ┌───┐  │     │  │
│   │   │  │MCP│  │                       │  │MCP│  │     │  │
│   │   │  └───┘  │                       │  └───┘  │     │  │
│   │   └────┬────┘                       └────┬────┘     │  │
│   │        │                                 │          │  │
│   └────────┼─────────────────────────────────┼──────────┘  │
│            ▼                                 ▼             │
│   ┌─────────────────────────────────────────────────────┐  │
│   │                  MCP Layer                           │  │
│   │     (Agent-to-Tool/Data Integration)                 │  │
│   │                                                      │  │
│   │   • Filesystem    • Databases    • APIs              │  │
│   │   • Workflows     • Search       • Calculators       │  │
│   └─────────────────────────────────────────────────────┘  │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**核心区别**:
- **MCP**: Agent ↔ Tool/Data (垂直集成)
- **A2A**: Agent ↔ Agent (水平协作)

---

## 8. 优势与局限

### 优势
| 优势 | 说明 |
|------|------|
| 开放标准 | Linux Foundation 治理，无厂商锁定 |
| 广泛支持 | 50+ 合作伙伴，多家咨询巨头支持 |
| 企业就绪 | 内置认证、授权、审计能力 |
| 灵活通信 | 同步、异步、流式全覆盖 |
| 模态丰富 | 文本、文件、结构化数据、音视频 |

### 局限
| 局限 | 说明 |
|------|------|
| 相对复杂 | JSON-RPC + 多种通信模式 |
| 需要基础设施 | 需要 HTTP 服务器、DNS 等 |
| 中心化倾向 | 依赖 Agent Card URL 发现 |

---

## 9. 相关资源

### 官方文档
- 主页: https://google.github.io/A2A
- 规范: https://github.com/google/A2A/tree/main/specification
- 教程: DeepLearning.AI A2A 课程

### 参考实现
- Python SDK: https://github.com/google/A2A/tree/main/python
- 示例代码: https://github.com/google/A2A/tree/main/samples

---

## 10. 对 Agent-Speaker 的启示

### 借鉴点
1. **Agent Card 机制** - 标准化的能力发现机制
2. **Task 生命周期** - 清晰的状态管理模型
3. **多模态消息** - 文本 + 文件 + 结构化数据
4. **通信模式** - 同步/异步/流式的灵活支持

### 差异点
| 维度 | A2A | Agent-Speaker |
|------|-----|---------------|
| 发现机制 | Agent Card URL | Nostr Relay 广播 |
| 通信方式 | HTTP 直连 | Relay 中继 |
| 身份模型 | OAuth2 等 | Nostr 公私钥 |
| 网络拓扑 | 点对点 | 去中心化广播 |
| 适用场景 | 企业内网 | 开放网络/跨组织 |

### 可兼容性
- 可以实现 A2A 协议适配层，让 Agent-Speaker 作为 A2A Agent 参与协作
- Agent Card 可以发布到 Nostr 上实现去中心化发现
- 结合两者优势：A2A 的任务模型 + Nostr 的去中心化通信
