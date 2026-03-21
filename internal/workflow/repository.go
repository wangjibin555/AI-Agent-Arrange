package workflow

// Repository defines the interface for workflow persistence
type Repository interface {
	// Workflow management
	SaveWorkflow(workflow *Workflow) error
	GetWorkflow(id string) (*Workflow, error)
	ListWorkflows() ([]*Workflow, error)
	DeleteWorkflow(id string) error

	// Execution management
	SaveExecution(execution *WorkflowExecution) error
	GetExecution(id string) (*WorkflowExecution, error)
	ListExecutions(workflowID string) ([]*WorkflowExecution, error)
	GetRunningExecutions() ([]*WorkflowExecution, error)
}
