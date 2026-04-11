# ACP (Agent Communication Protocol) 调研报告

## 1. 概述

**ACP (Agent Communication Protocol)** 最初由 **IBM Research** 开发，用于支持其开源 **BeeAI** 平台。2025年3月发布后，IBM 将 BeeAI 项目（包括 ACP 规范）捐赠给 **Linux Foundation**。

> **重要更新**: 从 **2025年9月** 开始，ACP 团队与 Google A2A 团队合并，共同开发统一的 Agent 通信标准。ACP 的设计思想已融入 A2A 协议。

| 属性 | 详情 |
|------|------|
| **发布方** | IBM Research |
| **发布时间** | 2025年3月 |
| **治理机构** | Linux Foundation |
| **当前状态** | 已合并入 A2A (2025年9月) |
| **协议层级** | Agent ↔ Agent (本地优先) |
| **核心用途** | 轻量级、RESTful 的 Agent 间通信 |

### 核心设计理念
- **本地优先 (Local-first)** - 优化边缘计算和低延迟场景
- **极简 REST** - 无需复杂 RPC，标准 HTTP 即可
- **无 SDK 依赖** - 用 cURL 就能交互
- **离线发现** - 支持 scale-to-zero 环境

---

## 2. 核心架构

```
┌─────────────────────────────────────────────────────────────────┐
│                     ACP Architecture                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐  │
│   │                    ACP Client                            │  │
│   │                   (HTTP Client)                          │  │
│   └────────────────────────┬────────────────────────────────┘  │
│                            │                                    │
│                            │ HTTP/REST                         │
│                            │                                    │
│   ┌────────────────────────▼────────────────────────────────┐  │
│   │                    ACP Server                            │  │
│   │                                                          │  │
│   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │  │
│   │  │   Run Agent  │  │  Agent State │  │   Manifest   │  │  │
│   │  │   Endpoint   │  │  Management  │  │   Endpoint   │  │  │
│   │  │  POST /run   │  │              │  │  GET /       │  │  │
│   │  └──────────────┘  └──────────────┘  └──────────────┘  │  │
│   │                                                          │  │
│   └──────────────────────────────────────────────────────────┘  │
│                                                                  │
│   ┌─────────────────────────────────────────────────────────┐  │
│   │                 Agent Manifest                          │  │
│   │         (Embedded in container/image)                   │  │
│   │                                                          │  │
│   │   • Agent metadata      • Capabilities                  │  │
│   │   • Input/output schemas • Dependencies                 │  │
│   └─────────────────────────────────────────────────────────┘  │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 通信协议栈
| 层级 | 技术 |
|------|------|
| 应用层 | RESTful HTTP |
| 消息格式 | JSON / Multipart |
| 内容类型 | MIME Types |
| 流式传输 | SSE (Server-Sent Events) |
| 可选传输 | WebSocket |

---

## 3. 核心概念

### 3.1 Agent Manifest (代理清单)
ACP 的核心创新是**离线发现**机制，Agent 元数据嵌入在分发包中：

```json
{
  "agent": {
    "name": "document-analyzer",
    "version": "1.0.0",
    "description": "Analyze documents and extract key information",
    "author": "IBM Research",
    "license": "MIT"
  },
  "endpoints": {
    "run": "/run",
    "health": "/health",
    "manifest": "/"
  },
  "input": {
    "type": "object",
    "properties": {
      "document": {
        "type": "string",
        "format": "uri",
        "description": "Document URL or base64 content"
      },
      "analysis_type": {
        "type": "string",
        "enum": ["summary", "entities", "sentiment"]
      }
    }
  },
  "output": {
    "type": "object",
    "properties": {
      "result": {"type": "string"},
      "confidence": {"type": "number"}
    }
  },
  "capabilities": {
    "streaming": true,
    "async": true,
    "multimodal": ["text", "image", "pdf"]
  }
}
```

### 3.2 标准端点
| 端点 | 方法 | 用途 |
|------|------|------|
| `/` | GET | 获取 Agent Manifest |
| `/run` | POST | 执行 Agent 任务 |
| `/health` | GET | 健康检查 |
| `/stream` | GET (SSE) | 流式结果 |

### 3.3 任务执行
```bash
# 同步调用
curl -X POST http://localhost:8333/run \
  -H "Content-Type: application/json" \
  -d '{
    "input": {
      "document": "https://example.com/doc.pdf",
      "analysis_type": "summary"
    }
  }'

# 响应
{
  "output": {
    "result": "This document discusses...",
    "confidence": 0.95
  },
  "metadata": {
    "execution_time": 2.3,
    "tokens_used": 1500
  }
}
```

### 3.4 流式响应
```bash
# 流式调用
curl -N http://localhost:8333/stream \
  -H "Content-Type: application/json" \
  -d '{"input": {"query": "Generate a long report"}}'

# SSE 响应流
data: {"type": "progress", "progress": 0.1, "message": "Starting..."}

data: {"type": "progress", "progress": 0.5, "message": "Processing..."}

data: {"type": "chunk", "content": "First part of result..."}

data: {"type": "chunk", "content": "Second part..."}

data: {"type": "complete", "output": {"result": "Final result"}}
```

---

## 4. 代码框架与实现

### 4.1 官方 SDK

| 语言 | 包名 | 安装命令 |
|------|------|---------|
| Python | acp-python-sdk | `pip install acp-python-sdk` |
| TypeScript | acp-typescript-sdk | `npm install acp-typescript-sdk` |

### 4.2 Python SDK 示例

#### 创建 ACP Agent Server
```python
from acp.server import ACPServer
from acp.types import AgentManifest, InputSchema, OutputSchema

# Define agent manifest
manifest = AgentManifest(
    name="text-summarizer",
    version="1.0.0",
    description="Summarize long text documents",
    input=InputSchema(
        type="object",
        properties={
            "text": {"type": "string", "maxLength": 100000},
            "max_length": {"type": "integer", "default": 200}
        },
        required=["text"]
    ),
    output=OutputSchema(
        type="object",
        properties={
            "summary": {"type": "string"},
            "original_length": {"type": "integer"},
            "summary_length": {"type": "integer"}
        }
    )
)

# Create server
server = ACPServer(manifest=manifest)

@server.run_handler
async def handle_run(input_data: dict) -> dict:
    """Handle run requests."""
    text = input_data["text"]
    max_length = input_data.get("max_length", 200)
    
    # Process
    summary = await generate_summary(text, max_length)
    
    return {
        "summary": summary,
        "original_length": len(text),
        "summary_length": len(summary)
    }

@server.stream_handler
async def handle_stream(input_data: dict):
    """Handle streaming requests."""
    text = input_data["text"]
    
    # Stream progress
    yield {"type": "progress", "progress": 0.0}
    
    for i, chunk in enumerate(process_chunks(text)):
        progress = (i + 1) / total_chunks
        yield {"type": "progress", "progress": progress}
        yield {"type": "chunk", "content": chunk}
    
    yield {"type": "complete"}

# Start server
if __name__ == "__main__":
    server.run(host="0.0.0.0", port=8333)
```

#### ACP Client 示例
```python
from acp.client import ACPClient

# Connect to agent (using manifest URL or file)
client = ACPClient.from_manifest("https://example.com/agent/manifest.json")

# Or from local manifest
client = ACPClient.from_file("./manifest.json")

# Synchronous call
result = await client.run({
    "text": "Long text to summarize...",
    "max_length": 150
})

print(result["summary"])

# Streaming call
async for event in client.stream({
    "text": "Very long text..."
}):
    if event["type"] == "progress":
        print(f"Progress: {event['progress'] * 100}%")
    elif event["type"] == "chunk":
        print(f"Chunk: {event['content']}")
    elif event["type"] == "complete":
        print("Done!")
```

### 4.3 TypeScript SDK 示例
```typescript
import { ACPServer, AgentManifest } from 'acp-typescript-sdk';

const manifest: AgentManifest = {
  agent: {
    name: 'code-reviewer',
    version: '1.0.0',
    description: 'Review code for quality and bugs'
  },
  input: {
    type: 'object',
    properties: {
      code: { type: 'string' },
      language: { type: 'string' }
    },
    required: ['code']
  },
  output: {
    type: 'object',
    properties: {
      issues: {
        type: 'array',
        items: {
          type: 'object',
          properties: {
            line: { type: 'number' },
            severity: { type: 'string' },
            message: { type: 'string' }
          }
        }
      }
    }
  }
};

const server = new ACPServer({ manifest });

server.onRun(async (input) => {
  const { code, language } = input;
  const issues = await reviewCode(code, language);
  return { issues };
});

server.start(8333);
```

### 4.4 无 SDK 直接使用 (cURL)
```bash
# 获取 manifest
curl http://localhost:8333/

# 健康检查
curl http://localhost:8333/health

# 运行任务
curl -X POST http://localhost:8333/run \
  -H "Content-Type: application/json" \
  -d '{"input": {"text": "Hello world"}}'

# 流式输出
curl -N http://localhost:8333/stream \
  -H "Content-Type: application/json" \
  -d '{"input": {"query": "test"}}'
```

---

## 5. 生态与参考实现

### 5.1 BeeAI 平台
BeeAI 是 ACP 的官方参考实现，提供：
- **Agent 仓库** - 发现、分享 ACP Agent
- **运行环境** - 本地运行任意框架的 Agent
- **编排能力** - 组合多个 Agent 成工作流

### 5.2 安装使用
```bash
# macOS/Linux
brew install i-am-bee/beeai/beeai
brew services start beeai

# 配置 LLM 提供商
beeai env setup

# 启动 Web UI
beeai ui  # http://localhost:8333

# CLI 操作
beeai list              # 列出可用 agents
beeai run chat          # 运行聊天 agent
beeai compose sequential # 顺序执行多个 agents
```

### 5.3 支持的 Agent 框架
- **BeeAI** - IBM 官方框架
- **LangGraph** - LangChain 的多 Agent 框架
- **CrewAI** - 角色扮演的多 Agent 系统
- **Smolagents** - Hugging Face 的轻量框架
- **自定义** - 任何符合 ACP 规范的实现

---

## 6. 协议规范要点

### 6.1 设计原则对比

| 特性 | ACP | A2A | MCP |
|------|-----|-----|-----|
| **协议风格** | REST | JSON-RPC | JSON-RPC |
| **传输层** | HTTP | HTTP | stdio/HTTP |
| **发现机制** | Manifest (离线) | Agent Card (在线) | 静态配置 |
| **SDK 依赖** | 可选 | 推荐 | 推荐 |
| **适用场景** | 边缘/本地 | 企业/云端 | 本地工具 |
| **学习曲线** | 低 | 中 | 低 |

### 6.2 多模态支持
ACP 使用标准 MIME 类型支持任意内容格式：

```json
{
  "input": {
    "document": {
      "mimeType": "application/pdf",
      "data": "base64encoded..."
    },
    "image": {
      "mimeType": "image/png",
      "uri": "https://example.com/image.png"
    }
  }
}
```

支持的 MIME 类型：
- `text/plain` - 纯文本
- `application/json` - 结构化数据
- `text/markdown` - Markdown
- `image/*` - 图像
- `audio/*` - 音频
- `video/*` - 视频
- `application/pdf` - PDF 文档

### 6.3 Agent 生命周期
```
INITIALIZING → ACTIVE → DEGRADED → RETIRING → RETIRED
     │             │          │          │         │
     ▼             ▼          ▼          ▼         ▼
  启动中        运行中      降级运行    退役中     已退役
```

生命周期事件通过 OpenTelemetry 追踪输出。

---

## 7. 与 A2A 的合并

### 7.1 合并背景
2025年9月，ACP 团队与 Google A2A 团队宣布合并，共同开发统一的 Agent 通信标准。

### 7.2 ACP 对 A2A 的贡献
1. **REST 简洁性** - A2A 考虑增加 REST 绑定
2. **离线发现** - Agent Manifest 概念融入 A2A
3. **边缘计算优化** - 资源受限场景的设计考量
4. **无 SDK 理念** - 降低协议采纳门槛

### 7.3 当前状态
- ACP 规范不再独立演进
- 已有 ACP Agent 可通过适配层与 A2A 互通
- BeeAI 平台继续作为多框架 Agent 的运行时

---

## 8. 优势与局限

### 优势
| 优势 | 说明 |
|------|------|
| 极致简洁 | 纯 REST，无需学习复杂 RPC |
| 无需 SDK | cURL 即可测试和集成 |
| 边缘友好 | 本地优先，低资源消耗 |
| 离线发现 | Manifest 嵌入，支持 scale-to-zero |
| 多模态原生 | MIME 类型驱动，任意格式 |

### 局限
| 局限 | 说明 |
|------|------|
| 已停止演进 | 合并入 A2A，不再独立发展 |
| 生态较小 | 相比 MCP/A2A，采用率较低 |
| 功能较简单 | 缺少复杂的任务编排能力 |

---

## 9. 相关资源

### 官方文档
- ACP 文档: https://agentcommunicationprotocol.dev
- BeeAI 文档: https://docs.beeai.dev
- IBM BeeAI: https://www.ibm.com/think/topics/beeai

### 课程
- DeepLearning.AI ACP 课程: https://learn.deeplearning.ai/courses/acp-agent-communication-protocol

### 代码仓库
- BeeAI: https://github.com/i-am-bee/beeai
- ACP SDK: https://github.com/i-am-bee/beeai/tree/main/packages/acp

---

## 10. 对 Agent-Speaker 的启示

### 借鉴点
1. **极简设计** - RESTful API 比 JSON-RPC 更易理解和实现
2. **离线发现** - Manifest 嵌入分发包的模式值得参考
3. **无 SDK 理念** - 协议应该简单到无需专用 SDK
4. **MIME 类型驱动** - 多模态内容的优雅处理方式

### 与 Nostr 的契合点
| ACP 特性 | Nostr 对应 |
|---------|-----------|
| Agent Manifest | Kind 0 (Metadata) |
| REST 端点 | Relay 查询接口 |
| 流式响应 | REQ 订阅机制 |
| 离线发现 | NIP-65 (Relay List) |

### 可兼容性
- ACP 的 Manifest 可以作为 Nostr Event 发布
- `/run` 端点对应 Nostr 的 Kind 30078 消息
- 流式响应对应 Nostr 的实时订阅推送
- ACP 的简洁设计哲学与 Nostr 的极简理念高度契合

### 独特优势
相比 ACP，Agent-Speaker 基于 Nostr 的优势：
1. **无需 HTTP 服务器** - Relay 中继降低部署门槛
2. **去中心化发现** - 不依赖中心化的 Manifest 注册表
3. **天然异步** - Nostr 的发布-订阅模型
4. **身份自管** - 公私钥体系，无需 OAuth
