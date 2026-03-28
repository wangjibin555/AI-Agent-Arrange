package workflow

import (
	"context"
	"fmt"
	"sync"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// MemoryRepository 是 Repository 的内存版实现。
// 主要用于测试、示例和不需要持久化的开发场景。
type MemoryRepository struct {
	workflows  map[string]*Workflow
	executions map[string]*WorkflowExecution
	mu         sync.RWMutex
}

// NewMemoryRepository 创建内存仓储。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workflows:  make(map[string]*Workflow),
		executions: make(map[string]*WorkflowExecution),
	}
}

// SaveWorkflow 保存工作流定义。
func (r *MemoryRepository) SaveWorkflow(_ context.Context, workflow *Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Deep copy to avoid external modifications
	r.workflows[workflow.ID] = workflow
	return nil
}

// GetWorkflow 按 ID 获取工作流定义。
func (r *MemoryRepository) GetWorkflow(_ context.Context, id string) (*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflow, exists := r.workflows[id]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}

	return workflow, nil
}

// ListWorkflows 返回全部工作流定义。
func (r *MemoryRepository) ListWorkflows(_ context.Context) ([]*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflows := make([]*Workflow, 0, len(r.workflows))
	for _, wf := range r.workflows {
		workflows = append(workflows, wf)
	}

	return workflows, nil
}

// DeleteWorkflow 删除工作流定义。
func (r *MemoryRepository) DeleteWorkflow(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.workflows[id]; !exists {
		return fmt.Errorf("workflow not found: %s", id)
	}

	delete(r.workflows, id)
	return nil
}

// SaveExecution 保存工作流执行快照。
func (r *MemoryRepository) SaveExecution(_ context.Context, execution *WorkflowExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.executions[execution.ID] = execution
	return nil
}

// GetExecution 按 ID 获取工作流执行快照。
func (r *MemoryRepository) GetExecution(_ context.Context, id string) (*WorkflowExecution, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	execution, exists := r.executions[id]
	if !exists {
		return nil, apperr.NotFoundf("execution not found: %s", id).WithCode("execution_not_found")
	}

	return execution, nil
}

// ListExecutions 返回某个工作流的全部执行记录。
func (r *MemoryRepository) ListExecutions(_ context.Context, workflowID string) ([]*WorkflowExecution, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	executions := make([]*WorkflowExecution, 0)
	for _, exec := range r.executions {
		if exec.WorkflowID == workflowID {
			executions = append(executions, exec)
		}
	}

	return executions, nil
}

// GetRunningExecutions 返回当前仍处于 running 状态的执行。
func (r *MemoryRepository) GetRunningExecutions(_ context.Context) ([]*WorkflowExecution, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	executions := make([]*WorkflowExecution, 0)
	for _, exec := range r.executions {
		if exec.Status == WorkflowStatusRunning {
			executions = append(executions, exec)
		}
	}

	return executions, nil
}
