package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// DeepSeekAgent is an agent that uses DeepSeek API for general-purpose tasks
// Suitable for: Q&A, summarization, translation, analysis, reasoning, and tool use
type DeepSeekAgent struct {
	name         string
	description  string
	capabilities []string
	client       *openai.Client
	model        string
	temperature  float32
	maxTokens    int
	toolRegistry *tool.Registry // Function calling support
}

// NewDeepSeekAgent creates a new DeepSeek agent
func NewDeepSeekAgent(name string, apiKey string) *DeepSeekAgent {
	// DeepSeek 兼容 OpenAI API 格式，只需要修改 base URL
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://api.deepseek.com/v1" // DeepSeek API endpoint

	return &DeepSeekAgent{
		name:        name,
		description: "DeepSeek agent for general-purpose text, analysis, reasoning, and tool tasks",
		capabilities: []string{
			"question-answering",   // 问答
			"summarization",        // 总结
			"knowledge-extraction", // 知识提取
			"text-generation",      // 文本生成
			"conversation",         // 对话
			"translation",          // 翻译
			"text-analysis",        // 文本分析
			"complex-reasoning",    // 复杂推理
			"web-search",           // 联网搜索（通过工具）
			"function-calling",     // 工具调用
		},
		client:       openai.NewClientWithConfig(config),
		model:        "deepseek-chat", // DeepSeek 默认模型
		temperature:  0.7,
		maxTokens:    4000,
		toolRegistry: tool.NewRegistry(), // Initialize tool registry
	}
}

// SetToolRegistry sets the tool registry for function calling
func (a *DeepSeekAgent) SetToolRegistry(registry *tool.Registry) {
	a.toolRegistry = registry
}

// GetToolRegistry returns the tool registry
func (a *DeepSeekAgent) GetToolRegistry() *tool.Registry {
	return a.toolRegistry
}

// GetName returns the agent's name
func (a *DeepSeekAgent) GetName() string {
	return a.name
}

// GetDescription returns the agent's description
func (a *DeepSeekAgent) GetDescription() string {
	return a.description
}

// GetCapabilities returns the agent's capabilities
func (a *DeepSeekAgent) GetCapabilities() []string {
	return a.capabilities
}

// Init initializes the agent with configuration
func (a *DeepSeekAgent) Init(config *Config) error {
	if config == nil {
		return nil
	}

	if config.Description != "" {
		a.description = config.Description
	}
	if len(config.Capabilities) > 0 {
		a.capabilities = append([]string(nil), config.Capabilities...)
	}

	// 从配置中读取模型设置
	if config.Settings != nil {
		if model, ok := config.Settings["model"].(string); ok && model != "" {
			a.model = model
		}
		if temp, ok := config.Settings["temperature"].(float64); ok {
			a.temperature = float32(temp)
		}
		if maxTokens, ok := config.Settings["max_tokens"].(int); ok {
			a.maxTokens = maxTokens
		}
	}

	return nil
}

// Shutdown gracefully shuts down the agent
func (a *DeepSeekAgent) Shutdown() error {
	// Client doesn't need cleanup
	return nil
}

// Execute executes a task using DeepSeek API with function calling support
func (a *DeepSeekAgent) Execute(ctx context.Context, input *TaskInput) (*TaskOutput, error) {
	output := &TaskOutput{
		TaskID:  input.TaskID,
		Success: false,
		Result:  make(map[string]interface{}),
		Metadata: map[string]interface{}{
			"agent":      a.name,
			"model":      a.model,
			"started_at": time.Now().Format(time.RFC3339),
		},
	}

	// 检查必需参数
	prompt, ok := input.Parameters["prompt"].(string)
	if !ok || prompt == "" {
		output.Error = "missing required parameter: prompt"
		return output, fmt.Errorf("%s", output.Error)
	}

	// 构建消息列表
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: prompt,
		},
	}

	// 如果有系统提示词
	if systemPrompt, ok := input.Parameters["system_prompt"].(string); ok && systemPrompt != "" {
		messages = append([]openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemPrompt,
			},
		}, messages...)
	}

	// 从参数中读取可选配置
	temperature := a.temperature
	maxTokens := a.maxTokens
	enableTools := true // 默认启用工具

	if temp, ok := input.Parameters["temperature"].(float64); ok {
		temperature = float32(temp)
	}
	if tokens, ok := input.Parameters["max_tokens"].(float64); ok {
		maxTokens = int(tokens)
	} else if tokens, ok := input.Parameters["max_tokens"].(int); ok {
		maxTokens = tokens
	}
	if enable, ok := input.Parameters["enable_tools"].(bool); ok {
		enableTools = enable
	}

	// 准备工具定义（如果启用）
	var tools []openai.Tool
	if enableTools && a.toolRegistry != nil && a.toolRegistry.Count() > 0 {
		tools = a.convertToolsToOpenAIFormat()
	}

	// 执行对话循环（支持多轮工具调用 + 流式输出）
	maxIterations := 10 // 防止无限循环
	toolCallHistory := make([]map[string]interface{}, 0)

	for iteration := 0; iteration < maxIterations; iteration++ {
		// 🔥 调用 DeepSeek API（流式，同时支持工具调用）
		req := openai.ChatCompletionRequest{
			Model:       a.model,
			Messages:    messages,
			Temperature: temperature,
			MaxTokens:   maxTokens,
			Tools:       tools,
			Stream:      true, // ✅ 启用流式
		}

		stream, err := a.client.CreateChatCompletionStream(ctx, req)
		if err != nil {
			output.Error = fmt.Sprintf("DeepSeek API error: %v", err)
			return output, err
		}

		// 流式接收响应
		var fullContent string
		var toolCalls []openai.ToolCall
		var finishReason string

		for {
			response, err := stream.Recv()
			if err == io.EOF {
				break // 流结束
			}
			if err != nil {
				stream.Close()
				output.Error = fmt.Sprintf("Stream error: %v", err)
				return output, err
			}

			if len(response.Choices) == 0 {
				continue
			}

			delta := response.Choices[0].Delta

			// 1. 处理文本内容（🌟 流式推送 Token）
			if delta.Content != "" {
				fullContent += delta.Content

				// 准备流式数据块
				chunkData := map[string]interface{}{
					"token":     delta.Content,
					"text":      fullContent,
					"iteration": iteration + 1,
				}

				// 实时发布 Token 事件（用于SSE）
				if input.EventPublisher != nil {
					input.EventPublisher.PublishTaskEvent(
						input.TaskID,
						"token_generated", // Token 流式事件
						"running",
						"",
						chunkData,
						"",
					)
				}

				// 调用工作流流式回调（用于workflow streaming）
				if input.StreamCallback != nil {
					input.StreamCallback(chunkData)
				}
			}

			// 2. 处理工具调用（累积）
			if len(delta.ToolCalls) > 0 {
				for _, toolCall := range delta.ToolCalls {
					if toolCall.Index != nil {
						idx := *toolCall.Index

						// 确保 toolCalls 数组足够大
						for len(toolCalls) <= idx {
							toolCalls = append(toolCalls, openai.ToolCall{})
						}

						// 累积数据
						if toolCall.ID != "" {
							toolCalls[idx].ID = toolCall.ID
						}
						if toolCall.Type != "" {
							toolCalls[idx].Type = toolCall.Type
						}
						if toolCall.Function.Name != "" {
							toolCalls[idx].Function.Name = toolCall.Function.Name
						}
						// 🔥 关键：拼接 arguments（流式返回）
						if toolCall.Function.Arguments != "" {
							toolCalls[idx].Function.Arguments += toolCall.Function.Arguments
						}
					}
				}
			}

			// 3. 记录完成原因
			if response.Choices[0].FinishReason != "" {
				finishReason = string(response.Choices[0].FinishReason)
			}
		}

		stream.Close()

		// 构建完整的 assistant 消息
		assistantMsg := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   fullContent,
			ToolCalls: toolCalls,
		}

		// 添加助手消息到对话历史
		messages = append(messages, assistantMsg)

		// 发布完整响应事件
		if fullContent != "" && input.EventPublisher != nil {
			input.EventPublisher.PublishTaskEvent(
				input.TaskID,
				"assistant_response",
				"running",
				"",
				map[string]interface{}{
					"content":   fullContent,
					"iteration": iteration + 1,
				},
				"",
			)
		}

		// 检查是否有工具调用
		if len(toolCalls) == 0 {
			// 没有工具调用，对话结束
			output.Success = true
			output.Result = map[string]interface{}{
				"text":             fullContent,
				"model":            a.model,
				"finish_reason":    finishReason,
				"tool_calls_count": len(toolCallHistory),
				"tool_calls":       toolCallHistory,
			}
			output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)
			output.Metadata["iterations"] = iteration + 1
			return output, nil
		}

		// 并发执行所有工具调用
		toolResults := a.executeToolCallsConcurrently(ctx, toolCalls, input)

		// 按原始顺序处理结果（保持消息顺序）
		for _, result := range toolResults {
			// 记录工具调用历史
			toolCallHistory = append(toolCallHistory, map[string]interface{}{
				"tool_name": result.ToolCall.Function.Name,
				"arguments": result.ToolCall.Function.Arguments,
				"result":    result.Result,
				"error":     result.Error,
			})

			// 添加工具结果到对话历史
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				Content:    result.ResultContent,
				ToolCallID: result.ToolCall.ID,
			})
		}
	}

	// 超过最大迭代次数
	output.Error = "exceeded maximum iterations for tool calling"
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)
	output.Metadata["iterations"] = maxIterations
	return output, fmt.Errorf("%s", output.Error)
}

// convertToolsToOpenAIFormat converts internal tool definitions to OpenAI format
func (a *DeepSeekAgent) convertToolsToOpenAIFormat() []openai.Tool {
	toolDefs := a.toolRegistry.GetDefinitions()
	openaiTools := make([]openai.Tool, 0, len(toolDefs))

	for _, def := range toolDefs {
		// 构建参数定义
		parameters := make(map[string]interface{})
		parameters["type"] = def.Parameters.Type

		// 转换属性
		if def.Parameters.Properties != nil {
			props := make(map[string]interface{})
			for key, prop := range def.Parameters.Properties {
				propDef := map[string]interface{}{
					"type":        prop.Type,
					"description": prop.Description,
				}
				if len(prop.Enum) > 0 {
					propDef["enum"] = prop.Enum
				}
				if prop.Items != nil {
					propDef["items"] = map[string]interface{}{
						"type":        prop.Items.Type,
						"description": prop.Items.Description,
					}
				}
				props[key] = propDef
			}
			parameters["properties"] = props
		}

		// 必需参数
		if len(def.Parameters.Required) > 0 {
			parameters["required"] = def.Parameters.Required
		}

		openaiTools = append(openaiTools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  parameters,
			},
		})
	}

	return openaiTools
}

// deepSeekToolCallResult holds the result of a tool call execution
type deepSeekToolCallResult struct {
	Index         int                    // Original index in toolCalls array
	ToolCall      openai.ToolCall        // Original tool call
	Result        map[string]interface{} // Execution result
	Error         error                  // Execution error
	ResultContent string                 // Formatted result for LLM
}

// executeToolCallsConcurrently executes multiple tool calls concurrently
func (a *DeepSeekAgent) executeToolCallsConcurrently(ctx context.Context, toolCalls []openai.ToolCall, input *TaskInput) []*deepSeekToolCallResult {
	if len(toolCalls) == 0 {
		return nil
	}

	// Create result channel with buffer
	resultChan := make(chan *deepSeekToolCallResult, len(toolCalls))

	// Execute all tools concurrently
	for i, toolCall := range toolCalls {
		go func(index int, tc openai.ToolCall) {
			result := &deepSeekToolCallResult{
				Index:    index,
				ToolCall: tc,
			}

			// Panic recovery
			defer func() {
				if r := recover(); r != nil {
					result.Error = fmt.Errorf("tool execution panicked: %v", r)
					result.ResultContent = fmt.Sprintf("Error: Tool execution panicked: %v", r)
					resultChan <- result
				}
			}()

			// Publish start event
			if input.EventPublisher != nil {
				input.EventPublisher.PublishTaskEvent(
					input.TaskID,
					"tool_call_started",
					"running",
					"",
					map[string]interface{}{
						"tool_name": tc.Function.Name,
						"arguments": tc.Function.Arguments,
						"index":     index,
					},
					"",
				)
			}

			// Execute tool
			toolResult, err := a.executeToolCall(ctx, tc)
			result.Result = toolResult
			result.Error = err

			// Format result content for LLM
			if err != nil {
				if toolErr, ok := err.(*tool.ToolError); ok {
					result.ResultContent = toolErr.ToLLMMessage()
				} else {
					result.ResultContent = fmt.Sprintf("Error: %v", err)
				}
			} else {
				resultBytes, _ := json.Marshal(toolResult)
				result.ResultContent = string(resultBytes)
			}

			// Publish completion event
			if input.EventPublisher != nil {
				resultData := map[string]interface{}{
					"tool_name": tc.Function.Name,
					"success":   err == nil,
					"index":     index,
				}
				if err != nil {
					resultData["error"] = err.Error()
				} else {
					resultData["result"] = toolResult
				}
				input.EventPublisher.PublishTaskEvent(
					input.TaskID,
					"tool_call_completed",
					"running",
					"",
					resultData,
					"",
				)
			}

			resultChan <- result
		}(i, toolCall)
	}

	// Collect all results
	results := make([]*deepSeekToolCallResult, len(toolCalls))
	for i := 0; i < len(toolCalls); i++ {
		result := <-resultChan
		results[result.Index] = result // Maintain original order
	}

	return results
}

// executeToolCall executes a single tool call with comprehensive error handling
func (a *DeepSeekAgent) executeToolCall(ctx context.Context, toolCall openai.ToolCall) (map[string]interface{}, error) {
	// 解析参数
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return nil, tool.ErrInvalidParams(
			toolCall.Function.Name,
			fmt.Sprintf("failed to parse JSON: %v", err),
		).WithDetails("raw_arguments", toolCall.Function.Arguments).
			WithSuggestedFix("Ensure arguments are valid JSON format")
	}

	// 使用 Executor 执行工具（包含超时、重试、参数验证）
	executor := tool.NewExecutor(a.toolRegistry, tool.DefaultExecutionConfig())
	result, err := executor.Execute(ctx, toolCall.Function.Name, params)
	if err != nil {
		return nil, err // Already a ToolError with full context
	}

	return result.Data, nil
}
