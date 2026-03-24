package workflow

import (
	"context"
	"fmt"
	"sync"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// MemoryRepository is an in-memory implementation of Repository
// Useful for testing and development
type MemoryRepository struct {
	workflows  map[string]*Workflow
	executions map[string]*WorkflowExecution
	mu         sync.RWMutex
}

// NewMemoryRepository creates a new in-memory repository
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workflows:  make(map[string]*Workflow),
		executions: make(map[string]*WorkflowExecution),
	}
}

// SaveWorkflow saves a workflow
func (r *MemoryRepository) SaveWorkflow(_ context.Context, workflow *Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Deep copy to avoid external modifications
	r.workflows[workflow.ID] = workflow
	return nil
}

// GetWorkflow retrieves a workflow by ID
func (r *MemoryRepository) GetWorkflow(_ context.Context, id string) (*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflow, exists := r.workflows[id]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}

	return workflow, nil
}

// ListWorkflows returns all workflows
func (r *MemoryRepository) ListWorkflows(_ context.Context) ([]*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflows := make([]*Workflow, 0, len(r.workflows))
	for _, wf := range r.workflows {
		workflows = append(workflows, wf)
	}

	return workflows, nil
}

// DeleteWorkflow deletes a workflow
func (r *MemoryRepository) DeleteWorkflow(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.workflows[id]; !exists {
		return fmt.Errorf("workflow not found: %s", id)
	}

	delete(r.workflows, id)
	return nil
}

// SaveExecution saves a workflow execution
func (r *MemoryRepository) SaveExecution(_ context.Context, execution *WorkflowExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.executions[execution.ID] = execution
	return nil
}

// GetExecution retrieves an execution by ID
func (r *MemoryRepository) GetExecution(_ context.Context, id string) (*WorkflowExecution, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	execution, exists := r.executions[id]
	if !exists {
		return nil, apperr.NotFoundf("execution not found: %s", id).WithCode("execution_not_found")
	}

	return execution, nil
}

// ListExecutions returns all executions for a workflow
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

// GetRunningExecutions returns all currently running executions
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
