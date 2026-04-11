# MCP (Model Context Protocol) 调研报告

## 1. 概述

**MCP (Model Context Protocol)** 是由 **Anthropic** 于 **2024年11月** 开源发布的协议，旨在为 AI 应用与外部系统之间的连接提供标准化接口。

### 核心定位
> "MCP 就像 AI 应用的 USB-C 端口" —— 为 AI 应用提供标准化的外部系统连接方式。

| 属性 | 详情 |
|------|------|
| **发布方** | Anthropic |
| **发布时间** | 2024年11月 |
| **治理机构** | Linux Foundation Agentic AI Foundation (2025年12月) |
| **协议层级** | Agent ↔ Tool/Data (垂直集成) |
| **核心用途** | 标准化 AI 应用与外部数据源、工具的连接 |

---

## 2. 核心架构

```
┌─────────────────────────────────────────────────────────────┐
│                      MCP Architecture                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│   ┌──────────────┐     ┌──────────────┐     ┌───────────┐  │
│   │   MCP Host   │────▶│  MCP Client  │◄───▶│ MCP Server│  │
│   │  (Claude,    │     │              │     │ (Tools,   │  │
│   │   ChatGPT,   │     │  JSON-RPC    │     │  APIs,    │  │
│   │   VS Code)   │     │  over stdio  │     │  DBs)     │  │
│   └──────────────┘     └──────────────┘     └───────────┘  │
│                              │                              │
│                              ▼                              │
│                    ┌──────────────────┐                     │
│                    │   Data Sources   │                     │
│                    │ • Files          │                     │
│                    │ • Databases      │                     │
│                    │ • APIs           │                     │
│                    │ • Workflows      │                     │
│                    └──────────────────┘                     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### 通信协议
- **传输层**: stdio (标准输入输出) 或 HTTP/SSE
- **消息格式**: JSON-RPC 2.0
- **连接模式**: 客户端-服务器架构

---

## 3. 核心能力

### 3.1 工具调用 (Tools)
```json
{
  "name": "search_database",
  "description": "Search customer records",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query": {"type": "string"},
      "limit": {"type": "number"}
    }
  }
}
```

### 3.2 资源访问 (Resources)
- 文件系统访问
- 数据库查询
- API 数据拉取
- 实时数据流

### 3.3 提示词模板 (Prompts)
- 预定义提示词模板
- 可复用的对话模式
- 结构化输入/输出

---

## 4. 代码框架与实现

### 4.1 官方 SDK

| 语言 | 包名 | 安装命令 |
|------|------|---------|
| Python | mcp | `pip install mcp` |
| TypeScript | @modelcontextprotocol/sdk | `npm install @modelcontextprotocol/sdk` |
| Java | mcp-java-sdk | Maven/Gradle |
| Go | mcp-go | `go get github.com/metoro-io/mcp-golang` |

### 4.2 简单服务器示例 (Python)
```python
from mcp.server import Server
from mcp.types import TextContent

app = Server("example-server")

@app.tool()
async def calculate_sum(a: int, b: int) -> TextContent:
    """Calculate the sum of two numbers."""
    result = a + b
    return TextContent(type="text", text=str(result))

@app.resource("data://users")
async def get_users() -> TextContent:
    """Get user data from database."""
    users = await db.query("SELECT * FROM users")
    return TextContent(type="text", text=json.dumps(users))
```

### 4.3 客户端示例 (TypeScript)
```typescript
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";

const client = new Client(
  { name: "example-client", version: "1.0.0" },
  { capabilities: { prompts: {}, resources: {}, tools: {} } }
);

const transport = new StdioClientTransport({
  command: "python",
  args: ["server.py"]
});

await client.connect(transport);

// List available tools
const tools = await client.listTools();

// Call a tool
const result = await client.callTool({
  name: "calculate_sum",
  arguments: { a: 1, b: 2 }
});
```

---

## 5. 生态与采用情况

### 5.1 关键数据 (截至2025年12月)
- **月度 SDK 下载量**: 9700万+
- **生产环境 MCP 服务器**: 10,000+
- **GitHub Stars**: 7.8k+
- **支持厂商**: Google, OpenAI, Microsoft, Amazon, Anthropic

### 5.2 应用场景
1. **企业聊天机器人** - 连接多个企业数据库
2. **代码助手** - 访问文件系统、API 文档
3. **数据分析** - 直接查询数据库生成报告
4. **自动化工作流** - 触发外部系统操作

---

## 6. 协议规范要点

### 6.1 生命周期
```
Initialize ──▶ Operation ──▶ Terminate
     │              │
     ▼              ▼
Capability    Tool/Resource
Negotiation      Access
```

### 6.2 消息类型
| 消息类型 | 用途 |
|---------|------|
| `initialize` | 能力协商与协议版本确认 |
| `tools/list` | 获取可用工具列表 |
| `tools/call` | 调用特定工具 |
| `resources/list` | 获取可用资源 |
| `resources/read` | 读取资源内容 |
| `prompts/list` | 获取提示词模板 |
| `prompts/get` | 获取具体提示词 |

### 6.3 安全模型
- **Capability-based**: 基于能力声明的访问控制
- **进程隔离**: 服务器运行在独立进程中
- **stdio 传输**: 默认使用标准输入输出，避免网络暴露

---

## 7. 优势与局限

### 优势
| 优势 | 说明 |
|------|------|
| 标准化 | 统一接口访问任意数据源 |
| 安全 | 进程隔离 + 能力声明 |
| 生态丰富 | 大量现成的服务器实现 |
| 易于集成 | 简单的 JSON-RPC 协议 |

### 局限
| 局限 | 说明 |
|------|------|
| 仅限本地 | 主要用于本地进程间通信 |
| 无 Agent 间通信 | 不解决多 Agent 协作问题 |
| 同步为主 | 对长时异步任务支持有限 |

---

## 8. 相关资源

### 官方文档
- 主页: https://modelcontextprotocol.io
- 规范: https://github.com/modelcontextprotocol/specification
- 示例: https://github.com/modelcontextprotocol/python-sdk

### 社区生态
- MCP 服务器仓库: https://github.com/modelcontextprotocol/servers
- 社区服务器列表: https://github.com/punkpeye/awesome-mcp-servers

---

## 9. 对 Agent-Speaker 的启示

### 借鉴点
1. **标准化接口设计**: MCP 的工具/资源/提示词三层抽象清晰
2. **能力协商机制**: 初始化时的能力声明值得参考
3. **安全模型**: 进程隔离 + 能力声明的访问控制

### 差异点
| 维度 | MCP | Agent-Speaker |
|------|-----|---------------|
| 通信范围 | 本地进程 | 网络/去中心化 |
| 协议层 | Agent-Tool | Agent-Agent |
| 传输方式 | stdio/HTTP | Nostr Relay |
| 身份模型 | 无 | Nostr 公私钥 |

### 可兼容性
- MCP 服务器可以作为 Agent-Speaker 的"工具层"
- 考虑实现 MCP 客户端能力，允许 Agent-Speaker 调用 MCP 服务器
