package agent

import (
	"context"
	"time"
)

// Agent represents the core interface that all agents must implement
type Agent interface {
	// GetName returns the unique name of the agent
	GetName() string

	// GetDescription returns the description of what the agent does
	GetDescription() string

	// GetCapabilities returns a list of capabilities this agent supports
	GetCapabilities() []string

	// Execute executes a task with the given input
	Execute(ctx context.Context, input *TaskInput) (*TaskOutput, error)

	// Init initializes the agent with configuration
	Init(config *Config) error

	// Shutdown gracefully shuts down the agent
	Shutdown() error
}

// TaskInput represents the input data for an agent task
type TaskInput struct {
	TaskID         string                             `json:"task_id"`
	Action         string                             `json:"action"`
	Parameters     map[string]interface{}             `json:"parameters"` // 静态参数（启动时渲染）
	Context        map[string]interface{}             `json:"context"`    // 遗留字段
	ParentTaskID   string                             `json:"parent_task_id,omitempty"`
	EventPublisher EventPublisher                     `json:"-"` // For streaming token events
	StreamCallback func(chunk map[string]interface{}) `json:"-"` // For workflow streaming
	ContextReader  ContextReader                      `json:"-"` // For dynamic context access (thread-safe)
}

// EventPublisher defines the interface for publishing task events (token streaming, progress, etc.)
type EventPublisher interface {
	PublishTaskEvent(taskID string, eventType string, status string, message string, result map[string]interface{}, errorMsg string)
}

// ContextReader 提供对ExecutionContext的只读访问（线程安全）
type ContextReader interface {
	// GetStepOutput 获取指定步骤的输出（返回副本，线程安全）
	GetStepOutput(stepID string) (map[string]interface{}, bool)

	// GetVariable 获取全局变量
	GetVariable(key string) (interface{}, bool)

	// GetField 获取嵌套字段，支持路径如 "step1.result.summary"
	GetField(stepID string, fieldPath string) (interface{}, bool)

	// WaitForField 等待指定字段可用（主动等待，带超时）
	// 当字段不存在时，会阻塞等待直到：
	// 1. 字段出现
	// 2. 超时
	// 3. 上游步骤完成但字段仍不存在
	WaitForField(stepID string, fieldPath string, timeout time.Duration) (interface{}, error)

	// IsStepCompleted 检查步骤是否已完成
	IsStepCompleted(stepID string) bool
}

// TaskOutput represents the output from an agent task
type TaskOutput struct {
	TaskID   string                 `json:"task_id"`
	Success  bool                   `json:"success"`
	Result   map[string]interface{} `json:"result"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Config holds agent configuration
type Config struct {
	Name         string                 `yaml:"name"`
	Type         string                 `yaml:"type"`
	Description  string                 `yaml:"description"`
	Capabilities []string               `yaml:"capabilities"`
	LLMProvider  string                 `yaml:"llm_provider,omitempty"`
	Settings     map[string]interface{} `yaml:"settings,omitempty"`
}

// Status represents the current status of an agent
type Status int

const (
	StatusIdle Status = iota
	StatusBusy
	StatusError
	StatusShutdown
)

func (s Status) String() string {
	return [...]string{"Idle", "Busy", "Error", "Shutdown"}[s]
}
