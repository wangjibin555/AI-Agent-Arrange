📋 configs 目录 - 配置文件说明

configs/                                                                                                                                                                                                                                                              
├── config.yaml      # 主配置文件（系统级）                                                                                                                                                                                                                           
├── agents.yaml      # Agent 定义（业务级）
└── tools.yaml       # 工具定义（功能级）

  ---
1️⃣config.yaml - 主配置文件（基础设施）

作用：配置系统运行所需的基础组件

⏺ Read 3 files (ctrl+o to expand)

⏺ 完美！现在让我详细解释这三个配置文件的作用和它们之间的关系：
                                
---                                                                                                                                                                                                                                                                   
📊 三个配置文件的层次结构

┌─────────────────────────────────────┐                                                                                                                                                                                                                               
│     config.yaml (基础设施层)          │  ← 系统运行依赖
│  - 数据库连接                          │
│  - 消息队列配置                        │
│  - 大模型 API                         │
│  - 监控日志                           │
└─────────────────────────────────────┘
↓ 提供能力
┌─────────────────────────────────────┐
│     agents.yaml (业务逻辑层)          │  ← 定义"谁"能做事
│  - 定义有哪些 Agent                   │
│  - Agent 的能力标签                   │
│  - 使用哪个大模型                      │
└─────────────────────────────────────┘
↓ 调用工具
┌─────────────────────────────────────┐
│     tools.yaml (工具函数层)           │  ← 定义"怎么"做事
│  - HTTP 请求                          │
│  - 数据库操作                         │
│  - 文件读写                           │
└─────────────────────────────────────┘

  ---
1️⃣config.yaml - 系统配置（基础设施）

核心作用：告诉系统"基础服务在哪里"

配置内容：

┌───────────────┬─────────────────┬──────────────────────────┐
│    配置项     │      作用       │           示例           │
├───────────────┼─────────────────┼──────────────────────────┤
│ server        │ HTTP 服务器配置 │ 监听端口 8080            │
├───────────────┼─────────────────┼──────────────────────────┤
│ redis         │ 短期记忆存储    │ Agent 当前状态、任务进度 │
├───────────────┼─────────────────┼──────────────────────────┤
│ mysql         │ 长期数据存储    │ 任务历史、Agent 执行日志 │
├───────────────┼─────────────────┼──────────────────────────┤
│ rabbitmq      │ 消息队列        │ Agent 之间异步通信       │
├───────────────┼─────────────────┼──────────────────────────┤
│ milvus        │ 向量数据库      │ 语义搜索、记忆检索       │
├───────────────┼─────────────────┼──────────────────────────┤
│ llm.providers │ 大模型配置      │ OpenAI、DeepSeek、Claude、通义千问 │
└───────────────┴─────────────────┴──────────────────────────┘

使用场景：
// 应用启动时读取
config := LoadConfig("configs/config.yaml")
redis.Connect(config.Redis.Host, config.Redis.Port)
mysql.Connect(config.MySQL.Host, config.MySQL.Port)

  ---
2️⃣agents.yaml - Agent 定义（业务层）

核心作用：定义"系统里有哪些 Agent，它们能做什么"

两种类型的 Agent：

Type 1: builtin（内置 Agent）

- name: "calculator"           # Agent 名称
  type: "builtin"              # 类型：代码实现的
  description: "计算器"         # 功能描述
  capabilities:                # 能力标签（用于匹配任务）
    - "math"
    - "arithmetic"
      enabled: true                # 是否启用

代码实现示例：
// internal/agent/builtin/calculator.go
type CalculatorAgent struct{}

func (a *CalculatorAgent) Execute(input *TaskInput) (*TaskOutput, error) {
// 直接用代码计算
result := eval(input.Parameters["expression"])
return &TaskOutput{Result: result}, nil
}

  ---
Type 2: llm（AI Agent）

- name: "data_analyst"          # Agent 名称
  type: "llm"                   # 类型：调用大模型
  llm_provider: "deepseek"      # 使用 DeepSeek / OpenAI（从 config.yaml 读配置）
  capabilities:
    - "text-analysis"
    - "summarization"
    - "translation"
    - "complex-reasoning"

运行机制：
// 当任务分配给 data_analyst 时
agent := registry.Get("data_analyst")
// 内部会调用对应的大模型 API
response := llm.Generate(
prompt="分析这个数据: " + input.Data
)

  ---
3️⃣tools.yaml - 工具定义（功能层）

核心作用：定义"Agent 可以调用的工具函数"

工具就是"可调用的函数"

- name: "http_request"                # 工具名称
  description: "发送 HTTP 请求"       # 工具描述
  parameters:                          # 参数定义（JSON Schema）
    - name: "url"
      type: "string"
      required: true
    - name: "method"
      enum: ["GET", "POST", "PUT", "DELETE"]

使用场景：
// Agent 执行任务时可以调用工具
tool := toolRegistry.Get("http_request")
result := tool.Execute(map[string]interface{}{
"url": "https://api.example.com/data",
"method": "GET",
})

  ---
🔄 三者协同工作示例

场景：用户请求"分析网站数据并生成报告"

1. 用户发起任务
   ↓
2. 编排引擎查看 agents.yaml
   → 找到 "data_analyst" (LLM Agent)
   ↓
3. data_analyst 从 config.yaml 获取 DeepSeek / OpenAI API 配置
   → 调用对应模型分析数据
   ↓
4. 分析过程中需要获取网站数据
   → 查看 tools.yaml，找到 "http_request" 工具
   → 调用 http_request 工具抓取数据
   ↓
5. 分析完成后，调用 "report_generator" Agent
   → 使用对应 LLM API（从 config.yaml 读取）
   → 生成报告文档
   ↓
6. 结果保存到 MySQL (config.yaml 配置)

  ---
💡 配置文件的修改场景

修改 config.yaml

- ✅ 换数据库地址
- ✅ 更换大模型 API Key
- ✅ 调整系统性能参数

修改 agents.yaml

- ✅ 添加新的 Agent（如"翻译 Agent"）
- ✅ 禁用某个 Agent（enabled: false）
- ✅ 修改 Agent 能力标签

修改 tools.yaml

- ✅ 添加新工具（如"发送邮件"）
- ✅ 修改工具参数
- ✅ 定义更复杂的参数校验

  ---
🎯 总结

┌─────────────┬────────────────┬───────────────┐
│    文件     │     关注点     │   谁来修改    │
├─────────────┼────────────────┼───────────────┤
│ config.yaml │ 基础设施在哪里 │ 运维人员      │
├─────────────┼────────────────┼───────────────┤
│ agents.yaml │ 有哪些智能体   │ 产品/算法人员 │
├─────────────┼────────────────┼───────────────┤
│ tools.yaml  │ 有哪些工具函数 │ 开发人员      │
└─────────────┴────────────────┴───────────────┘

这种分层设计的好处：
- ✅ 职责分离：不同角色修改不同文件
- ✅ 灵活配置：不改代码就能增加 Agent
- ✅ 易于扩展：新增功能只需修改 YAML

你现在理解这三个配置文件的作用了吗？想深入了解哪部分的实现？
