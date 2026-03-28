package agent

import (
	"context"
	"time"
)

// Agent 定义所有 Agent 必须实现的核心接口。
type Agent interface {
	// GetName 返回 Agent 的唯一名称。
	GetName() string

	// GetDescription 返回 Agent 的职责描述。
	GetDescription() string

	// GetCapabilities 返回 Agent 支持的能力列表。
	GetCapabilities() []string

	// Execute 执行一次任务。
	Execute(ctx context.Context, input *TaskInput) (*TaskOutput, error)

	// Init 使用配置初始化 Agent。
	Init(config *Config) error

	// Shutdown 优雅关闭 Agent。
	Shutdown() error
}

// ActionContract 描述某个 Agent Action 的弱输入/输出契约。
type ActionContract struct {
	InputRequired  []string `json:"input_required,omitempty"`
	OutputRequired []string `json:"output_required,omitempty"`
}

// ActionContractProvider 是可选接口，用于让 Agent 暴露 action 契约信息。
type ActionContractProvider interface {
	GetActionContract(action string) (*ActionContract, bool)
}

// TaskInput 表示一次 Agent 任务的输入数据。
// 它同时承载静态参数、运行时上下文访问能力以及事件回调句柄。
type TaskInput struct {
	TaskID         string                             `json:"task_id"`
	Action         string                             `json:"action"`
	Parameters     map[string]interface{}             `json:"parameters"` // 静态参数（启动时渲染）
	Context        map[string]interface{}             `json:"context"`    // 遗留字段
	ParentTaskID   string                             `json:"parent_task_id,omitempty"`
	EventPublisher EventPublisher                     `json:"-"` // 用于发布流式 token、进度等事件
	StreamCallback func(chunk map[string]interface{}) `json:"-"` // 工作流流式步骤的 chunk 回调
	ContextReader  ContextReader                      `json:"-"` // 线程安全的动态上下文读取器
}

// EventPublisher 定义任务事件发布接口，例如 token 流、进度和状态变化。
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

// TaskOutput 表示 Agent 任务执行后的输出。
type TaskOutput struct {
	TaskID   string                 `json:"task_id"`
	Success  bool                   `json:"success"`
	Result   map[string]interface{} `json:"result"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Config 表示 Agent 初始化配置。
type Config struct {
	Name         string                 `yaml:"name"`
	Type         string                 `yaml:"type"`
	Description  string                 `yaml:"description"`
	Capabilities []string               `yaml:"capabilities"`
	LLMProvider  string                 `yaml:"llm_provider,omitempty"`
	Settings     map[string]interface{} `yaml:"settings,omitempty"`
}

// Status 表示 Agent 当前状态。
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
