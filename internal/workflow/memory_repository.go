package workflow

import (
	"fmt"
	"sync"
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
func (r *MemoryRepository) SaveWorkflow(workflow *Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Deep copy to avoid external modifications
	r.workflows[workflow.ID] = workflow
	return nil
}

// GetWorkflow retrieves a workflow by ID
func (r *MemoryRepository) GetWorkflow(id string) (*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflow, exists := r.workflows[id]
	if !exists {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}

	return workflow, nil
}

// ListWorkflows returns all workflows
func (r *MemoryRepository) ListWorkflows() ([]*Workflow, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workflows := make([]*Workflow, 0, len(r.workflows))
	for _, wf := range r.workflows {
		workflows = append(workflows, wf)
	}

	return workflows, nil
}

// DeleteWorkflow deletes a workflow
func (r *MemoryRepository) DeleteWorkflow(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.workflows[id]; !exists {
		return fmt.Errorf("workflow not found: %s", id)
	}

	delete(r.workflows, id)
	return nil
}

// SaveExecution saves a workflow execution
func (r *MemoryRepository) SaveExecution(execution *WorkflowExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.executions[execution.ID] = execution
	return nil
}

// GetExecution retrieves an execution by ID
func (r *MemoryRepository) GetExecution(id string) (*WorkflowExecution, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	execution, exists := r.executions[id]
	if !exists {
		return nil, fmt.Errorf("execution not found: %s", id)
	}

	return execution, nil
}

// ListExecutions returns all executions for a workflow
func (r *MemoryRepository) ListExecutions(workflowID string) ([]*WorkflowExecution, error) {
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
func (r *MemoryRepository) GetRunningExecutions() ([]*WorkflowExecution, error) {
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
