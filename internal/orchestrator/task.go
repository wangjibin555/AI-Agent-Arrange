package orchestrator

import "time"

// Task represents a unit of work to be executed by an agent
type Task struct {
	ID                 string                 `json:"id"`                            // 任务唯一标识符（UUID）
	AgentName          string                 `json:"agent_name"`                    // 执行任务的 Agent 名称（可在创建时指定，否则自动选择）
	RequiredCapability string                 `json:"required_capability,omitempty"` // 任务需要的能力（用于自动选择 Agent）
	Action             string                 `json:"action"`                        // 任务动作类型（如 translate, query, search 等）
	Parameters         map[string]interface{} `json:"parameters"`                    // 任务参数（动作相关的输入数据）
	Dependencies       []string               `json:"dependencies,omitempty"`        // 依赖的任务 ID 列表（未实现）
	Priority           int                    `json:"priority"`                      // 任务优先级（数值越大优先级越高，未实现）
	Timeout            time.Duration          `json:"timeout"`                       // 任务执行超时时间
	Status             TaskStatus             `json:"status"`                        // 任务状态（pending, running, completed, failed, cancelled）
	Result             map[string]interface{} `json:"result,omitempty"`              // 任务执行结果（成功时返回）
	Error              string                 `json:"error,omitempty"`               // 任务错误信息（失败时返回）
	RetryCount         *int                   `json:"retry_count,omitempty"`         // 任务已重试次数（用于重试机制）
	CreatedAt          time.Time              `json:"created_at"`                    // 任务创建时间
	StartedAt          *time.Time             `json:"started_at,omitempty"`          // 任务开始执行时间
	CompletedAt        *time.Time             `json:"completed_at,omitempty"`        // 任务完成时间（成功或失败）
}

// TaskStatus represents the status of a task
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// Workflow represents a collection of tasks with dependencies
type Workflow struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Tasks       []*Task    `json:"tasks"`
	Status      TaskStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
}
