package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/sashabaranov/go-openai"
)

// DeepSeekAgent is an agent that uses DeepSeek API for lightweight tasks
// Suitable for: Q&A, summarization, knowledge extraction
type DeepSeekAgent struct {
	name         string
	description  string
	capabilities []string
	client       *openai.Client
	model        string
	temperature  float32
	maxTokens    int
}

// NewDeepSeekAgent creates a new DeepSeek agent
func NewDeepSeekAgent(name string, apiKey string) *DeepSeekAgent {
	// DeepSeek 兼容 OpenAI API 格式，只需要修改 base URL
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://api.deepseek.com/v1" // DeepSeek API endpoint

	return &DeepSeekAgent{
		name:        name,
		description: "DeepSeek agent for Q&A, summarization, and knowledge tasks",
		capabilities: []string{
			"question-answering",   // 问答
			"summarization",        // 总结
			"knowledge-extraction", // 知识提取
			"text-generation",      // 文本生成
			"conversation",         // 对话
		},
		client:      openai.NewClientWithConfig(config),
		model:       "deepseek-chat", // DeepSeek 默认模型
		temperature: 0.7,
		maxTokens:   2000,
	}
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

// Execute executes a task using DeepSeek API
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

	// 调用 DeepSeek API (兼容 OpenAI 格式)
	req := openai.ChatCompletionRequest{
		Model:       a.model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}

	resp, err := a.client.CreateChatCompletion(ctx, req)
	if err != nil {
		output.Error = fmt.Sprintf("DeepSeek API error: %v", err)
		return output, err
	}

	// 检查响应
	if len(resp.Choices) == 0 {
		output.Error = "no response from DeepSeek API"
		return output, fmt.Errorf(output.Error)
	}

	// 构建成功响应
	output.Success = true
	output.Result = map[string]interface{}{
		"text":          resp.Choices[0].Message.Content,
		"model":         resp.Model,
		"finish_reason": resp.Choices[0].FinishReason,
		"usage": map[string]interface{}{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"total_tokens":      resp.Usage.TotalTokens,
		},
	}
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)

	return output, nil
}
