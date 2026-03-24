# AI-Agent-Arrange

一个基于 Go 的多 Agent 编排服务，当前已经提供两类执行入口：

- `task`：单任务 / 单 Agent 执行
- `workflow`：多步骤 DAG / 流式工作流执行

对外推荐统一围绕 `execution` 模型接入：

- 创建 task：`POST /api/v1/tasks`
- 执行 workflow：`POST /api/v1/workflows/execute`
- 查询 execution：`GET /api/v1/executions/:id`
- 取消 execution：`DELETE /api/v1/executions/:id`
- 订阅 execution SSE：`GET /api/v1/executions/:id/stream`

## 当前重点

当前版本已经完成这些主链路：

- 统一 execution 查询、取消、SSE 事件流
- workflow execution 持久化
- workflow recovery 状态字段：`recovery_status`
- workflow recovery handoff 字段：`superseded_by_execution_id`
- 最小端到端 demo：task / workflow / execution / SSE

## Agent 能力定位

当前内置的 Agent 分工可以简单理解为：

- `echo-agent-*`：用于联调、回显、健康检查、基础流程验证
- `deepseek-chat-agent`：用于通用文本任务，包括问答、总结、翻译、分析、推理、工具调用
- `openai-gpt-agent`：用于代码生成、代码审查、调试、复杂推理等更偏工程和编码的任务

如果只是验证通用文本处理链路，默认优先使用 `deepseek-chat-agent`。
如果任务明确偏代码、调试、审查，再优先使用 `openai-gpt-agent`。

## 快速开始

### 1. 启动服务

```bash
go run cmd/server/main.go
```

或先看更完整的启动步骤：

- [快速开始指南](./QUICKSTART.md)

### 2. 运行统一 execution 演示

服务启动后，直接运行：

```bash
./scripts/demo_unified_execution.sh
```

这个脚本会演示：

- 创建 task 并通过 execution 查询结果
- 创建 workflow 并通过 execution 查询结果
- 订阅 execution SSE
- 可选检查 recovery handoff

如果服务不在默认地址：

```bash
SERVER_URL=http://127.0.0.1:8080 ./scripts/demo_unified_execution.sh
```

如果要检查某个旧 execution 是否被新 execution 接管：

```bash
RECOVERY_EXECUTION_ID=<execution-id> ./scripts/demo_unified_execution.sh
```

## 推荐接入方式

### 创建入口

- 普通任务：调用 `POST /api/v1/tasks`
- 工作流：调用 `POST /api/v1/workflows/execute`

### 统一跟踪入口

无论从哪条入口创建，后续都推荐统一走：

- `GET /api/v1/executions/:id`
- `GET /api/v1/executions/:id/stream`

### recovery 字段

workflow execution 可能额外带：

- `recovery_status`
- `superseded_by_execution_id`

当旧 execution 被恢复链路接管时：

- 旧 execution：`recovery_status = "superseded"`
- 新 execution：`recovery_status = "resumed"`

如果旧 execution 响应里带有 `superseded_by_execution_id`，客户端应切换去跟踪新的 execution ID。

## 文档入口

- [快速开始指南](./QUICKSTART.md)
- [统一 execution 接入说明](./docs/api/UNIFIED_EXECUTION_INTEGRATION_GUIDE.md)
- [统一 execution API 使用指南](./docs/api/UNIFIED_EXECUTION_API_GUIDE.md)
- [文档目录](./docs/README.md)

## 项目结构

```text
AI-Agent-Arrange/
├── cmd/server/                 # 服务启动入口
├── internal/agent/             # Agent 实现
├── internal/orchestrator/      # task 调度系统
├── internal/workflow/          # workflow 执行系统
├── internal/api/               # HTTP API + SSE
├── internal/storage/mysql/     # MySQL 持久化
├── scripts/                    # demo / 测试脚本
├── examples/                   # 示例代码与 workflow YAML
└── docs/                       # 文档
```

## 当前状态

更准确地说，当前项目已经进入“统一 execution 平台收口阶段”，不是继续扩 DSL 的阶段。

当前更适合做的是：

- 用统一 execution API 接入真实场景
- 补接入文档和演示脚本
- 观察 recovery / SSE / execution 视图是否满足上层使用

而不是继续优先扩展 workflow 高阶语义。
