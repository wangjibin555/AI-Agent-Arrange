package workflow

import (
	"sync"
	"time"
)

// WorkflowStatus represents the status of a workflow execution
type WorkflowStatus string

const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusCompleted WorkflowStatus = "completed"
	WorkflowStatusFailed    WorkflowStatus = "failed"
	WorkflowStatusCancelled WorkflowStatus = "cancelled"
	WorkflowStatusSkipped   WorkflowStatus = "skipped"
)

// Workflow 表示完整的工作流定义
type Workflow struct {
	ID          string                 `json:"id" yaml:"id"`
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string                 `json:"version,omitempty" yaml:"version,omitempty"`
	Steps       []*Step                `json:"steps" yaml:"steps"`
	Variables   map[string]interface{} `json:"variables,omitempty" yaml:"variables,omitempty"` // 全局工作流变量
	OnFailure   *FailurePolicy         `json:"on_failure,omitempty" yaml:"on_failure,omitempty"`
	Timeout     *time.Duration         `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	CreatedAt   time.Time              `json:"created_at" yaml:"-"`
	UpdatedAt   time.Time              `json:"updated_at" yaml:"-"`
}

// Step 表示工作流中的单个步骤
type Step struct {
	ID                 string                 `json:"id" yaml:"id"`                                                       // 步骤唯一标识符
	Name               string                 `json:"name,omitempty" yaml:"name,omitempty"`                               // 可读的步骤名称
	AgentName          string                 `json:"agent" yaml:"agent"`                                                 // 执行此步骤的Agent名称
	Action             string                 `json:"action" yaml:"action"`                                               // 要执行的动作
	Parameters         map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`                           // 步骤参数（可使用模板）
	DependsOn          []string               `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`                   // 此步骤依赖的步骤ID列表
	Condition          *Condition             `json:"condition,omitempty" yaml:"condition,omitempty"`                     // 条件执行规则
	Route              *RouteConfig           `json:"route,omitempty" yaml:"route,omitempty"`                             // 动态路由配置
	Timeout            *time.Duration         `json:"timeout,omitempty" yaml:"timeout,omitempty"`                         // 步骤超时时间
	Retries            int                    `json:"retries,omitempty" yaml:"retries,omitempty"`                         // 失败时的重试次数
	OnFailure          *StepFailurePolicy     `json:"on_failure,omitempty" yaml:"on_failure,omitempty"`                   // 失败时的处理策略
	ContinueOn         *ContinuePolicy        `json:"continue_on,omitempty" yaml:"continue_on,omitempty"`                 // 在错误时是否继续执行
	OutputAlias        string                 `json:"output_as,omitempty" yaml:"output_as,omitempty"`                     // 步骤输出在上下文中的别名
	Streaming          *StreamingConfig       `json:"streaming,omitempty" yaml:"streaming,omitempty"`                     // 流式执行配置
	CompensationAction string                 `json:"compensation_action,omitempty" yaml:"compensation_action,omitempty"` // 回滚时补偿动作
	CompensationParams map[string]interface{} `json:"compensation_params,omitempty" yaml:"compensation_params,omitempty"` // 补偿动作参数
}

// Condition 表示条件执行规则
type Condition struct {
	Type       ConditionType `json:"type" yaml:"type"`                                 // 条件类型（表达式、状态等）
	Expression string        `json:"expression,omitempty" yaml:"expression,omitempty"` // 条件表达式（例如："{{step1.result.value}} > 10"）
	Status     string        `json:"status,omitempty" yaml:"status,omitempty"`         // 依赖步骤所需的状态
}

// RouteConfig 定义步骤完成后的动态路由规则
type RouteConfig struct {
	Expression string              `json:"expression" yaml:"expression"`               // 路由表达式，渲染结果作为 case key
	Cases      map[string][]string `json:"cases,omitempty" yaml:"cases,omitempty"`     // route key -> 激活的下游步骤ID
	Default    []string            `json:"default,omitempty" yaml:"default,omitempty"` // 未命中任何 case 时默认激活的步骤
}

// ConditionType represents the type of condition
type ConditionType string

const (
	ConditionTypeExpression ConditionType = "expression" // Evaluate an expression
	ConditionTypeStatus     ConditionType = "status"     // Check step status
	ConditionTypeAlways     ConditionType = "always"     // Always execute
)

// FailurePolicy defines what happens when the workflow fails
type FailurePolicy struct {
	Rollback bool     `json:"rollback,omitempty" yaml:"rollback,omitempty"` // Rollback executed steps
	Notify   []string `json:"notify,omitempty" yaml:"notify,omitempty"`     // Notification channels
}

// StepFailurePolicy defines what happens when a step fails
type StepFailurePolicy struct {
	Action string `json:"action" yaml:"action"` // "fail", "continue", "retry", "rollback"
}

// ContinuePolicy defines when to continue execution despite errors
type ContinuePolicy struct {
	OnError bool `json:"on_error,omitempty" yaml:"on_error,omitempty"` // Continue on error
}

// StreamingConfig 定义步骤的流式执行行为
type StreamingConfig struct {
	// 是否启用流式模式
	Enabled bool `json:"enabled" yaml:"enabled"`

	// 等待模式："full"等待完整数据 / "partial"接受部分数据即开始
	WaitFor string `json:"wait_for,omitempty" yaml:"wait_for,omitempty"`

	// 最小启动数据量（token数），仅在WaitFor=partial时有效
	MinStartTokens int `json:"min_start_tokens,omitempty" yaml:"min_start_tokens,omitempty"`

	// 触发阈值：每接收多少个token触发下游（0=关闭，-1=实时）
	ChunkSize int `json:"chunk_size,omitempty" yaml:"chunk_size,omitempty"`

	// 是否自动触发依赖步骤（启用Pipeline模式）
	TriggerNext bool `json:"trigger_next,omitempty" yaml:"trigger_next,omitempty"`

	// 缓冲策略："none"无缓冲 / "memory"内存缓冲
	BufferStrategy string `json:"buffer_strategy,omitempty" yaml:"buffer_strategy,omitempty"`

	// 缓冲区大小（字节）
	BufferSize int `json:"buffer_size,omitempty" yaml:"buffer_size,omitempty"`

	// 超时配置：流式数据长时间无更新时的超时阈值（秒）
	StreamTimeout int `json:"stream_timeout,omitempty" yaml:"stream_timeout,omitempty"`

	// 错误处理："stop"停止 / "continue"继续使用部分数据 / "fallback"回退到阻塞模式
	OnError string `json:"on_error,omitempty" yaml:"on_error,omitempty"`

	// 是否启用checkpoint，默认对流式步骤启用
	CheckpointEnabled *bool `json:"checkpoint_enabled,omitempty" yaml:"checkpoint_enabled,omitempty"`
}

// 默认流式配置值
const (
	DefaultWaitFor        = "full"   // 默认等待完整数据
	DefaultBufferStrategy = "memory" // 默认使用内存缓冲
	DefaultBufferSize     = 1048576  // 默认1MB缓冲区
	DefaultStreamTimeout  = 30       // 默认30秒超时
	DefaultOnError        = "stop"   // 默认错误时停止
)

// WorkflowExecution represents a running or completed workflow instance
type WorkflowExecution struct {
	ID              string                       `json:"id"`
	WorkflowID      string                       `json:"workflow_id"`
	Status          WorkflowStatus               `json:"status"`
	CurrentStep     string                       `json:"current_step,omitempty"`
	StepExecutions  map[string]*StepExecution    `json:"step_executions"` // stepID -> execution details
	Context         *ExecutionContext            `json:"context"`         // Workflow execution context
	Checkpoints     map[string]*StreamCheckpoint `json:"checkpoints,omitempty"`
	ResumeState     *ExecutionResumeState        `json:"resume_state,omitempty"`
	RouteSelections map[string]string            `json:"route_selections,omitempty"` // router stepID -> selected route key
	Error           string                       `json:"error,omitempty"`
	StartedAt       time.Time                    `json:"started_at"`
	CompletedAt     *time.Time                   `json:"completed_at,omitempty"`
	notifier        *sync.Cond                   `json:"-"`
}

// StepExecution represents the execution status of a single step
type StepExecution struct {
	StepID      string                 `json:"step_id"`
	Status      WorkflowStatus         `json:"status"`
	TaskID      string                 `json:"task_id,omitempty"`     // Associated orchestrator task ID
	Result      map[string]interface{} `json:"result,omitempty"`      // Step execution result
	Error       string                 `json:"error,omitempty"`       // Error message if failed
	RetryCount  int                    `json:"retry_count,omitempty"` // Number of retries attempted
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	StartedAt   time.Time              `json:"started_at"`             // Step start time
	CompletedAt *time.Time             `json:"completed_at,omitempty"` // Step completion time
}

type StreamCheckpoint struct {
	StepID      string                 `json:"step_id"`
	ChunkIndex  int                    `json:"chunk_index"`
	TotalChunks int                    `json:"total_chunks"`
	TotalTokens int                    `json:"total_tokens"`
	Output      map[string]interface{} `json:"output,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
}

type ExecutionResumeState struct {
	SourceExecutionID string   `json:"source_execution_id,omitempty"`
	RestoredSteps     []string `json:"restored_steps,omitempty"`
}

// ExecutionContext holds runtime data for workflow execution
type ExecutionContext struct {
	Variables map[string]interface{}            `json:"variables"` // Global variables
	Outputs   map[string]map[string]interface{} `json:"outputs"`   // stepID -> output data
}

// NewExecutionContext creates a new execution context
func NewExecutionContext(variables map[string]interface{}) *ExecutionContext {
	if variables == nil {
		variables = make(map[string]interface{})
	}
	return &ExecutionContext{
		Variables: variables,
		Outputs:   make(map[string]map[string]interface{}),
	}
}

// SetStepOutput stores the output of a step
func (c *ExecutionContext) SetStepOutput(stepID string, output map[string]interface{}) {
	c.Outputs[stepID] = output
}

// GetStepOutput retrieves the output of a step
func (c *ExecutionContext) GetStepOutput(stepID string) (map[string]interface{}, bool) {
	output, exists := c.Outputs[stepID]
	return output, exists
}

// SetVariable sets a global variable
func (c *ExecutionContext) SetVariable(key string, value interface{}) {
	c.Variables[key] = value
}

// GetVariable retrieves a global variable
func (c *ExecutionContext) GetVariable(key string) (interface{}, bool) {
	value, exists := c.Variables[key]
	return value, exists
}

func (c *ExecutionContext) RemoveStepOutput(stepID string) {
	delete(c.Outputs, stepID)
}

func (c *ExecutionContext) DeleteVariable(key string) {
	delete(c.Variables, key)
}
