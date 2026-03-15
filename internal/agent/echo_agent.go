package agent

import (
	"context"
	"fmt"
	"time"
)

// EchoAgent is a simple agent for testing and demonstration purposes
// It echoes back input parameters and simulates various scenarios
type EchoAgent struct {
	name         string
	description  string
	capabilities []string
	config       *Config
	delay        time.Duration // 模拟处理延迟
}

// NewEchoAgent creates a new EchoAgent instance
func NewEchoAgent(name string) *EchoAgent {
	return &EchoAgent{
		name:        name,
		description: "A test agent that echoes input and simulates various scenarios",
		capabilities: []string{
			"text-processing", // 文本处理能力
			"health-check",    // 健康检查能力
		},
		delay: 100 * time.Millisecond, // 默认延迟
	}
}

// GetName returns the agent's unique name
func (e *EchoAgent) GetName() string {
	return e.name
}

// GetDescription returns the agent's description
func (e *EchoAgent) GetDescription() string {
	return e.description
}

// GetCapabilities returns the list of capabilities
func (e *EchoAgent) GetCapabilities() []string {
	return e.capabilities
}

// Init initializes the agent with configuration
func (e *EchoAgent) Init(config *Config) error {
	e.config = config

	// 从配置中读取延迟设置（如果有）
	if config != nil && config.Settings != nil {
		if delayMs, ok := config.Settings["delay_ms"].(int); ok {
			e.delay = time.Duration(delayMs) * time.Millisecond
		}
	}

	return nil
}

// Shutdown gracefully shuts down the agent
func (e *EchoAgent) Shutdown() error {
	// EchoAgent 没有需要清理的资源
	return nil
}

// Execute executes a task based on the action type
func (e *EchoAgent) Execute(ctx context.Context, input *TaskInput) (*TaskOutput, error) {
	output := &TaskOutput{
		TaskID:  input.TaskID,
		Success: false,
		Result:  make(map[string]interface{}),
		Metadata: map[string]interface{}{
			"agent":      e.name,
			"started_at": time.Now().Format(time.RFC3339),
		},
	}

	// 检查 context 是否已取消
	select {
	case <-ctx.Done():
		output.Error = "task cancelled before execution"
		return output, ctx.Err()
	default:
	}

	// 根据 action 类型执行不同操作
	switch input.Action {
	case "echo":
		return e.handleEcho(ctx, input, output)

	case "ping":
		return e.handlePing(ctx, input, output)

	case "sleep":
		return e.handleSleep(ctx, input, output)

	case "error":
		return e.handleError(ctx, input, output)

	case "process":
		return e.handleProcess(ctx, input, output)

	default:
		output.Error = fmt.Sprintf("unsupported action: %s", input.Action)
		return output, fmt.Errorf("unsupported action: %s", input.Action)
	}
}

// handleEcho echoes back the input parameters
func (e *EchoAgent) handleEcho(ctx context.Context, input *TaskInput, output *TaskOutput) (*TaskOutput, error) {
	// 模拟处理延迟
	select {
	case <-time.After(e.delay):
	case <-ctx.Done():
		output.Error = "task cancelled during echo"
		return output, ctx.Err()
	}

	// 回显所有参数
	output.Success = true
	output.Result["action"] = "echo"
	output.Result["echoed_parameters"] = input.Parameters
	output.Result["message"] = "Echo successful"
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)

	return output, nil
}

// handlePing handles health check requests
func (e *EchoAgent) handlePing(ctx context.Context, input *TaskInput, output *TaskOutput) (*TaskOutput, error) {
	// 快速响应，不延迟
	select {
	case <-ctx.Done():
		output.Error = "task cancelled during ping"
		return output, ctx.Err()
	default:
	}

	output.Success = true
	output.Result["action"] = "ping"
	output.Result["response"] = "pong"
	output.Result["timestamp"] = time.Now().Unix()
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)

	return output, nil
}

// handleSleep simulates a long-running task (for testing timeout)
func (e *EchoAgent) handleSleep(ctx context.Context, input *TaskInput, output *TaskOutput) (*TaskOutput, error) {
	// 从参数中读取睡眠时间（秒）
	sleepSeconds := 5 // 默认5秒

	if duration, ok := input.Parameters["duration"].(float64); ok {
		sleepSeconds = int(duration)
	} else if duration, ok := input.Parameters["duration"].(int); ok {
		sleepSeconds = duration
	}

	sleepDuration := time.Duration(sleepSeconds) * time.Second

	// 睡眠，同时监听 context 取消
	select {
	case <-time.After(sleepDuration):
		// 正常完成
		output.Success = true
		output.Result["action"] = "sleep"
		output.Result["slept_seconds"] = sleepSeconds
		output.Result["message"] = fmt.Sprintf("Slept for %d seconds", sleepSeconds)
		output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)
		return output, nil

	case <-ctx.Done():
		// 被取消或超时
		output.Error = fmt.Sprintf("task cancelled after sleeping (timeout or cancellation)")
		return output, ctx.Err()
	}
}

// handleError simulates a task failure (for testing retry mechanism)
func (e *EchoAgent) handleError(ctx context.Context, input *TaskInput, output *TaskOutput) (*TaskOutput, error) {
	// 模拟处理延迟
	select {
	case <-time.After(e.delay):
	case <-ctx.Done():
		output.Error = "task cancelled during error simulation"
		return output, ctx.Err()
	}

	// 从参数中读取错误信息
	errorMsg := "Simulated error"
	if msg, ok := input.Parameters["error_message"].(string); ok {
		errorMsg = msg
	}

	// 可选：模拟随机失败（用于测试重试）
	// 从参数中读取失败率（0-1之间）
	if failureRate, ok := input.Parameters["failure_rate"].(float64); ok {
		if failureRate > 0 && failureRate < 1 {
			// 这里可以用随机数判断是否失败
			// 简化起见，总是失败
		}
	}

	output.Success = false
	output.Error = errorMsg
	output.Result["action"] = "error"
	output.Result["message"] = "Error simulation completed"
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)

	return output, fmt.Errorf("task error: %s", errorMsg)
}

// handleProcess simulates simple text processing
func (e *EchoAgent) handleProcess(ctx context.Context, input *TaskInput, output *TaskOutput) (*TaskOutput, error) {
	// 模拟处理延迟
	select {
	case <-time.After(e.delay):
	case <-ctx.Done():
		output.Error = "task cancelled during processing"
		return output, ctx.Err()
	}

	// 从参数中读取文本
	text := ""
	if t, ok := input.Parameters["text"].(string); ok {
		text = t
	}

	// 简单处理：转换为大写
	processedText := fmt.Sprintf("PROCESSED: %s", text)

	output.Success = true
	output.Result["action"] = "process"
	output.Result["original_text"] = text
	output.Result["processed_text"] = processedText
	output.Result["message"] = "Text processing completed"
	output.Metadata["completed_at"] = time.Now().Format(time.RFC3339)

	return output, nil
}
