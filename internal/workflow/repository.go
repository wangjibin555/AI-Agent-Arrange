package workflow

import "context"

// Repository 定义工作流持久化接口。
// 它既保存静态定义，也保存每次执行的运行时快照。
type Repository interface {
	// 工作流定义管理
	SaveWorkflow(ctx context.Context, workflow *Workflow) error
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	ListWorkflows(ctx context.Context) ([]*Workflow, error)
	DeleteWorkflow(ctx context.Context, id string) error

	// 工作流执行快照管理
	SaveExecution(ctx context.Context, execution *WorkflowExecution) error
	GetExecution(ctx context.Context, id string) (*WorkflowExecution, error)
	ListExecutions(ctx context.Context, workflowID string) ([]*WorkflowExecution, error)
	GetRunningExecutions(ctx context.Context) ([]*WorkflowExecution, error)
}
