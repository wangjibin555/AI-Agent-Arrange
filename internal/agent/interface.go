package agent

import "context"

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
	TaskID         string                 `json:"task_id"`
	Action         string                 `json:"action"`
	Parameters     map[string]interface{} `json:"parameters"`
	Context        map[string]interface{} `json:"context"`
	ParentTaskID   string                 `json:"parent_task_id,omitempty"`
	EventPublisher EventPublisher         `json:"-"` // For streaming token events
}

// EventPublisher defines the interface for publishing task events (token streaming, progress, etc.)
type EventPublisher interface {
	PublishTaskEvent(taskID string, eventType string, status string, message string, result map[string]interface{}, errorMsg string)
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
