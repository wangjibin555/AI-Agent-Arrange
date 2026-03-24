package workflow

import "context"

// Repository defines the interface for workflow persistence
type Repository interface {
	// Workflow management
	SaveWorkflow(ctx context.Context, workflow *Workflow) error
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	ListWorkflows(ctx context.Context) ([]*Workflow, error)
	DeleteWorkflow(ctx context.Context, id string) error

	// Execution management
	SaveExecution(ctx context.Context, execution *WorkflowExecution) error
	GetExecution(ctx context.Context, id string) (*WorkflowExecution, error)
	ListExecutions(ctx context.Context, workflowID string) ([]*WorkflowExecution, error)
	GetRunningExecutions(ctx context.Context) ([]*WorkflowExecution, error)
}
