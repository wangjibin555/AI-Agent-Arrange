package workflow

import (
	"encoding/json"
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

// RecoveryStatus represents restart recovery lifecycle for a workflow execution.
type RecoveryStatus string

const (
	RecoveryStatusNone        RecoveryStatus = "none"
	RecoveryStatusInterrupted RecoveryStatus = "interrupted"
	RecoveryStatusResumed     RecoveryStatus = "resumed"
	RecoveryStatusSuperseded  RecoveryStatus = "superseded"
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
	Capability         string                 `json:"capability,omitempty" yaml:"capability,omitempty"`                   // 按能力选择执行Agent
	Action             string                 `json:"action" yaml:"action"`                                               // 要执行的动作
	Inputs             map[string]interface{} `json:"inputs,omitempty" yaml:"inputs,omitempty"`                           // 显式输入映射，优先于 params 模板主路径
	Parameters         map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`                           // 步骤参数（可使用模板）
	DependsOn          []string               `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`                   // 此步骤依赖的步骤ID列表
	Condition          *Condition             `json:"condition,omitempty" yaml:"condition,omitempty"`                     // 条件执行规则
	Route              *RouteConfig           `json:"route,omitempty" yaml:"route,omitempty"`                             // 动态路由配置
	Foreach            *ForeachConfig         `json:"foreach,omitempty" yaml:"foreach,omitempty"`                         // foreach / fan-out 配置
	Timeout            *time.Duration         `json:"timeout,omitempty" yaml:"timeout,omitempty"`                         // 步骤超时时间
	Retries            int                    `json:"retries,omitempty" yaml:"retries,omitempty"`                         // 失败时的重试次数
	OnFailure          *StepFailurePolicy     `json:"on_failure,omitempty" yaml:"on_failure,omitempty"`                   // 失败时的处理策略
	ContinueOn         *ContinuePolicy        `json:"continue_on,omitempty" yaml:"continue_on,omitempty"`                 // 在错误时是否继续执行
	OutputAlias        string                 `json:"output_as,omitempty" yaml:"output_as,omitempty"`                     // 步骤输出在上下文中的别名
	InputSchema        *StepSchema            `json:"input_schema,omitempty" yaml:"input_schema,omitempty"`               // 显式输入契约（弱 schema）
	OutputSchema       *StepSchema            `json:"output_schema,omitempty" yaml:"output_schema,omitempty"`             // 输出契约（弱 schema）
	Streaming          *StreamingConfig       `json:"streaming,omitempty" yaml:"streaming,omitempty"`                     // 流式执行配置
	CompensationAction string                 `json:"compensation_action,omitempty" yaml:"compensation_action,omitempty"` // 回滚时补偿动作
	CompensationParams map[string]interface{} `json:"compensation_params,omitempty" yaml:"compensation_params,omitempty"` // 补偿动作参数
}

type StepSchema struct {
	Required []string `json:"required,omitempty" yaml:"required,omitempty"`
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

// ForeachConfig 定义步骤内部的 fan-out / 批处理展开语义
type ForeachConfig struct {
	From        string `json:"from" yaml:"from"`                                     // 数组来源表达式
	ItemAs      string `json:"item_as,omitempty" yaml:"item_as,omitempty"`           // 当前 item 注入到模板中的变量名
	IndexAs     string `json:"index_as,omitempty" yaml:"index_as,omitempty"`         // 当前 item 索引注入到模板中的变量名
	MaxParallel int    `json:"max_parallel,omitempty" yaml:"max_parallel,omitempty"` // foreach 内部最大并发度
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
	ID                      string                       `json:"id"`
	WorkflowID              string                       `json:"workflow_id"`
	Status                  WorkflowStatus               `json:"status"`
	RecoveryStatus          RecoveryStatus               `json:"recovery_status,omitempty"`
	CurrentStep             string                       `json:"current_step,omitempty"`
	SupersededByExecutionID string                       `json:"superseded_by_execution_id,omitempty"`
	StepExecutions          map[string]*StepExecution    `json:"step_executions"` // stepID -> execution details
	Context                 *ExecutionContext            `json:"context"`         // Workflow execution context
	Checkpoints             map[string]*StreamCheckpoint `json:"checkpoints,omitempty"`
	ResumeState             *ExecutionResumeState        `json:"resume_state,omitempty"`
	RouteSelections         map[string]string            `json:"route_selections,omitempty"` // router stepID -> selected route key
	Error                   string                       `json:"error,omitempty"`
	StartedAt               time.Time                    `json:"started_at"`
	CompletedAt             *time.Time                   `json:"completed_at,omitempty"`
	notifier                *executionNotifier           `json:"-"`
}

type executionNotifier struct {
	mu sync.Mutex
	ch chan struct{}
}

func newExecutionNotifier() *executionNotifier {
	return &executionNotifier{
		ch: make(chan struct{}),
	}
}

func (n *executionNotifier) WaitChan() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

func (n *executionNotifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()

	close(n.ch)
	n.ch = make(chan struct{})
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

func (c *StreamCheckpoint) Clone() *StreamCheckpoint {
	if c == nil {
		return nil
	}
	return &StreamCheckpoint{
		StepID:      c.StepID,
		ChunkIndex:  c.ChunkIndex,
		TotalChunks: c.TotalChunks,
		TotalTokens: c.TotalTokens,
		Output:      copyMap(c.Output),
		Timestamp:   c.Timestamp,
	}
}

type ExecutionResumeState struct {
	SourceExecutionID string   `json:"source_execution_id,omitempty"`
	RestoredSteps     []string `json:"restored_steps,omitempty"`
}

// ExecutionContext holds runtime data for workflow execution
type ExecutionContext struct {
	mu        sync.RWMutex
	variables map[string]interface{}
	outputs   map[string]map[string]interface{}
}

// NewExecutionContext creates a new execution context
func NewExecutionContext(variables map[string]interface{}) *ExecutionContext {
	clonedVariables := copyMap(variables)
	if clonedVariables == nil {
		clonedVariables = make(map[string]interface{})
	}
	return &ExecutionContext{
		variables: clonedVariables,
		outputs:   make(map[string]map[string]interface{}),
	}
}

// SetStepOutput stores the output of a step
func (c *ExecutionContext) SetStepOutput(stepID string, output map[string]interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outputs[stepID] = copyMap(output)
}

// GetStepOutput retrieves the output of a step
func (c *ExecutionContext) GetStepOutput(stepID string) (map[string]interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	output, exists := c.outputs[stepID]
	if !exists || output == nil {
		return nil, false
	}
	return copyMap(output), true
}

// MergeStepOutput merges partial output into one step output and returns the merged snapshot.
func (c *ExecutionContext) MergeStepOutput(stepID string, output map[string]interface{}) map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	merged := c.outputs[stepID]
	if merged == nil {
		merged = make(map[string]interface{}, len(output))
		c.outputs[stepID] = merged
	}
	for key, value := range output {
		merged[key] = value
	}
	return copyMap(merged)
}

// SetVariable sets a global variable
func (c *ExecutionContext) SetVariable(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.variables == nil {
		c.variables = make(map[string]interface{})
	}
	c.variables[key] = value
}

// GetVariable retrieves a global variable
func (c *ExecutionContext) GetVariable(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, exists := c.variables[key]
	return value, exists
}

func (c *ExecutionContext) RemoveStepOutput(stepID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.outputs, stepID)
}

func (c *ExecutionContext) DeleteVariable(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.variables, key)
}

func (c *ExecutionContext) SnapshotVariables() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	variables := copyMap(c.variables)
	if variables == nil {
		return make(map[string]interface{})
	}
	return variables
}

func (c *ExecutionContext) SnapshotOutputs() map[string]map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	outputs := make(map[string]map[string]interface{}, len(c.outputs))
	for stepID, output := range c.outputs {
		outputs[stepID] = copyMap(output)
	}
	return outputs
}

func (c *ExecutionContext) Clone() *ExecutionContext {
	if c == nil {
		return NewExecutionContext(nil)
	}

	return &ExecutionContext{
		variables: c.SnapshotVariables(),
		outputs:   c.SnapshotOutputs(),
	}
}

func (c *ExecutionContext) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}

	payload := struct {
		Variables map[string]interface{}            `json:"variables"`
		Outputs   map[string]map[string]interface{} `json:"outputs"`
	}{
		Variables: c.SnapshotVariables(),
		Outputs:   c.SnapshotOutputs(),
	}
	return json.Marshal(payload)
}

func (c *ExecutionContext) UnmarshalJSON(data []byte) error {
	var payload struct {
		Variables map[string]interface{}            `json:"variables"`
		Outputs   map[string]map[string]interface{} `json:"outputs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.variables = copyMap(payload.Variables)
	c.outputs = make(map[string]map[string]interface{}, len(payload.Outputs))
	for stepID, output := range payload.Outputs {
		c.outputs[stepID] = copyMap(output)
	}
	if c.variables == nil {
		c.variables = make(map[string]interface{})
	}
	return nil
}
