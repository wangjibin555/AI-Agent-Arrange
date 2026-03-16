package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/sashabaranov/go-openai"
)

// OpenAIAgent is an agent that uses OpenAI API for complex tasks
// Suitable for: coding, complex reasoning, debugging
type OpenAIAgent struct {
	name         string
	description  string
	capabilities []string
	client       *openai.Client
	model        string
	temperature  float32
	maxTokens    int
}

// NewOpenAIAgent creates a new OpenAI agent
func NewOpenAIAgent(name string, apiKey string) *OpenAIAgent {
	return &OpenAIAgent{
		name:        name,
		description: "OpenAI GPT agent for complex tasks, coding, and reasoning",
		capabilities: []string{
			"code-generation",   // 代码生成
			"code-review",       // 代码审查
			"debugging",         // 调试
			"complex-reasoning", // 复杂推理
			"translation",       // 翻译
			"text-generation",   // 文本生成（通用能力）
		},
		client:      openai.NewClient(apiKey),
		model:       "gpt-3.5-turbo", // 默认模型
		temperature: 0.7,
		maxTokens:   2000,
	}
}

// GetName returns the agent's name
func (a *OpenAIAgent) GetName() string {
	return a.name
}

// GetDescription returns the agent's description
func (a *OpenAIAgent) GetDescription() string {
	return a.description
}

// GetCapabilities returns the agent's capabilities
func (a *OpenAIAgent) GetCapabilities() []string {
	return a.capabilities
}

// Init initializes the agent with configuration
func (a *OpenAIAgent) Init(config *Config) error {
	if config == nil {
		return nil
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
func (a *OpenAIAgent) Shutdown() error {
	// OpenAI client doesn't need cleanup
	return nil
}

// Execute executes a task using OpenAI API
func (a *OpenAIAgent) Execute(ctx context.Context, input *TaskInput) (*TaskOutput, error) {
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
		return output, fmt.Errorf(output.Error)
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

	if temp, ok := input.Parameters["temperature"].(float64); ok {
		temperature = float32(temp)
	}
	if tokens, ok := input.Parameters["max_tokens"].(float64); ok {
		maxTokens = int(tokens)
	} else if tokens, ok := input.Parameters["max_tokens"].(int); ok {
		maxTokens = tokens
	}

	// 调用 OpenAI API（流式）
	req := openai.ChatCompletionRequest{
		Model:       a.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Stream:      true, // 启用流式
	}

	stream, err := a.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		output.Error = fmt.Sprintf("OpenAI API error: %v", err)
		return output, err
	}
	defer stream.Close()

	// 接收流式 Token
	var fullResponse string
	var finishReason string

	for {
		response, err := stream.Recv()
		if err != nil {
			// 流结束
			break
		}

		if len(response.Choices) == 0 {
			continue
		}

		// 获取 Token
		delta := response.Choices[0].Delta.Content
		if delta != "" {
			fullResponse += delta

			// 发布 Token 事件（实时推送）
			if input.EventPublisher != nil {
				input.EventPublisher.PublishTaskEvent(
					input.TaskID,
					"token_generated", // 事件类型
					"running",
					"",
					map[string]interface{}{
						"token": delta,
						"text":  fullResponse, // 累积的完整文本
					},
					"",
				)
			}
		}

		// 记录完成原因
		if response.Choices[0].FinishReason != "" {
			finishReason = string(response.Choices[0].FinishReason)
		}
	}

	// 检查响应
	if fullResponse == "" {
		output.Error = "no response from OpenAI API"
		return output, fmt.Errorf(output.Error)
	}

	// 构建成功响应
	output.Success = true
	output.Result = map[string]interface{}{
		"text":          fullResponse,
		"model":         a.model,
		"finish_reason": finishReason,
	}
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)

	return output, nil
}
