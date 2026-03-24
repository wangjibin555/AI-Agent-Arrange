package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/workflow"
)

// ExecutionType identifies the underlying execution mode.
type ExecutionType string

const (
	ExecutionTypeTask     ExecutionType = "task"
	ExecutionTypeWorkflow ExecutionType = "workflow"
)

// UnifiedExecutionStatus is the platform-facing execution status.
type UnifiedExecutionStatus string

const (
	ExecutionStatusPending   UnifiedExecutionStatus = "pending"
	ExecutionStatusRunning   UnifiedExecutionStatus = "running"
	ExecutionStatusCompleted UnifiedExecutionStatus = "completed"
	ExecutionStatusFailed    UnifiedExecutionStatus = "failed"
	ExecutionStatusSkipped   UnifiedExecutionStatus = "skipped"
	ExecutionStatusCancelled UnifiedExecutionStatus = "cancelled"
)

// ExecutionHandle is the lightweight response returned when an execution starts.
type ExecutionHandle struct {
	ID   string        `json:"id"`
	Type ExecutionType `json:"type"`
}

// ExecutionStepView is the unified step detail view.
type ExecutionStepView struct {
	ID          string                 `json:"id"`
	Status      UnifiedExecutionStatus `json:"status"`
	Error       string                 `json:"error,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	StartedAt   *time.Time             `json:"started_at,omitempty"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// ExecutionView is the platform-facing execution view.
type ExecutionView struct {
	ID                      string                 `json:"id"`
	Type                    ExecutionType          `json:"type"`
	Status                  UnifiedExecutionStatus `json:"status"`
	RecoveryStatus          string                 `json:"recovery_status,omitempty"`
	SupersededByExecutionID string                 `json:"superseded_by_execution_id,omitempty"`
	Error                   string                 `json:"error,omitempty"`
	StartedAt               *time.Time             `json:"started_at,omitempty"`
	CompletedAt             *time.Time             `json:"completed_at,omitempty"`
	Summary                 map[string]interface{} `json:"summary,omitempty"`
	Steps                   []ExecutionStepView    `json:"steps,omitempty"`
}

// TaskExecutionRequest is the unified task execution request.
type TaskExecutionRequest struct {
	ID         string
	AgentName  string
	Capability string
	Action     string
	Parameters map[string]interface{}
	Timeout    time.Duration
	Priority   int
}

// WorkflowExecutionRequest is the unified workflow execution request.
type WorkflowExecutionRequest struct {
	Workflow  *workflow.Workflow
	Variables map[string]interface{}
}

// WorkflowRunner is the minimal workflow execution interface needed by the unified service.
type WorkflowRunner interface {
	Execute(ctx context.Context, workflow *workflow.Workflow, variables map[string]interface{}) (*workflow.WorkflowExecution, error)
	GetExecution(executionID string) (*workflow.WorkflowExecution, error)
	CancelExecution(executionID string) error
}

// ExecutionService unifies task and workflow execution behind one service boundary.
type ExecutionService struct {
	orchestrator   *Engine
	workflowEngine WorkflowRunner

	mu             sync.RWMutex
	executionTypes map[string]ExecutionType
}

// NewExecutionService creates a new unified execution service.
func NewExecutionService(orchestratorEngine *Engine, workflowEngine WorkflowRunner) *ExecutionService {
	return &ExecutionService{
		orchestrator:   orchestratorEngine,
		workflowEngine: workflowEngine,
		executionTypes: make(map[string]ExecutionType),
	}
}

// ExecuteTask submits a task through the orchestrator engine and returns a unified handle.
func (s *ExecutionService) ExecuteTask(ctx context.Context, req *TaskExecutionRequest) (*ExecutionHandle, error) {
	if req == nil {
		return nil, fmt.Errorf("task execution request is nil")
	}
	if s.orchestrator == nil {
		return nil, fmt.Errorf("orchestrator engine is not configured")
	}
	if req.Action == "" {
		return nil, fmt.Errorf("task action is required")
	}
	if req.AgentName == "" && req.Capability == "" {
		return nil, fmt.Errorf("either agent name or capability is required")
	}

	taskID := req.ID
	if taskID == "" {
		taskID = uuid.New().String()
	}

	task := &Task{
		ID:                 taskID,
		AgentName:          req.AgentName,
		RequiredCapability: req.Capability,
		Action:             req.Action,
		Parameters:         req.Parameters,
		Timeout:            req.Timeout,
		Priority:           req.Priority,
		Status:             TaskStatusPending,
		CreatedAt:          time.Now(),
	}

	if err := s.orchestrator.SubmitTask(task); err != nil {
		return nil, err
	}

	s.rememberExecutionType(taskID, ExecutionTypeTask)
	return &ExecutionHandle{ID: taskID, Type: ExecutionTypeTask}, nil
}

// ExecuteWorkflow runs a workflow through the configured workflow engine.
func (s *ExecutionService) ExecuteWorkflow(ctx context.Context, req *WorkflowExecutionRequest) (*ExecutionHandle, error) {
	if req == nil {
		return nil, fmt.Errorf("workflow execution request is nil")
	}
	if s.workflowEngine == nil {
		return nil, fmt.Errorf("workflow engine is not configured")
	}
	if req.Workflow == nil {
		return nil, fmt.Errorf("workflow definition is required")
	}

	execution, err := s.workflowEngine.Execute(ctx, req.Workflow, req.Variables)
	if err != nil {
		return nil, err
	}

	s.rememberExecutionType(execution.ID, ExecutionTypeWorkflow)
	return &ExecutionHandle{ID: execution.ID, Type: ExecutionTypeWorkflow}, nil
}

// GetExecution returns the unified execution view.
func (s *ExecutionService) GetExecution(ctx context.Context, executionID string) (*ExecutionView, error) {
	_ = ctx

	if executionID == "" {
		return nil, fmt.Errorf("execution id is required")
	}

	switch s.executionType(executionID) {
	case ExecutionTypeTask:
		return s.getTaskExecution(executionID)
	case ExecutionTypeWorkflow:
		return s.getWorkflowExecution(executionID)
	default:
		if view, err := s.getTaskExecution(executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeTask)
			return view, nil
		}
		if view, err := s.getWorkflowExecution(executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeWorkflow)
			return view, nil
		}
		return nil, fmt.Errorf("execution %s not found", executionID)
	}
}

// CancelExecution cancels a unified execution if the underlying engine supports it.
func (s *ExecutionService) CancelExecution(ctx context.Context, executionID string) error {
	_ = ctx

	switch s.executionType(executionID) {
	case ExecutionTypeTask:
		return s.cancelTaskExecution(executionID)
	case ExecutionTypeWorkflow:
		return s.cancelWorkflowExecution(executionID)
	default:
		if err := s.cancelTaskExecution(executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeTask)
			return nil
		}
		if _, err := s.getWorkflowExecution(executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeWorkflow)
			return s.cancelWorkflowExecution(executionID)
		}
		return fmt.Errorf("execution %s not found", executionID)
	}
}

func (s *ExecutionService) cancelTaskExecution(executionID string) error {
	if s.orchestrator == nil {
		return fmt.Errorf("orchestrator engine is not configured")
	}

	return s.orchestrator.GetTaskManager().CancelTask(executionID)
}

func (s *ExecutionService) cancelWorkflowExecution(executionID string) error {
	if s.workflowEngine == nil {
		return fmt.Errorf("workflow engine is not configured")
	}
	return s.workflowEngine.CancelExecution(executionID)
}

func (s *ExecutionService) getTaskExecution(executionID string) (*ExecutionView, error) {
	if s.orchestrator == nil {
		return nil, fmt.Errorf("orchestrator engine is not configured")
	}

	task, err := s.orchestrator.GetTask(executionID)
	if err != nil {
		return nil, err
	}

	summary := map[string]interface{}{
		"agent_name": task.AgentName,
		"action":     task.Action,
	}
	if task.RequiredCapability != "" {
		summary["capability"] = task.RequiredCapability
	}
	if task.Result != nil {
		summary["result"] = task.Result
	}

	return &ExecutionView{
		ID:          task.ID,
		Type:        ExecutionTypeTask,
		Status:      mapTaskStatus(task.Status),
		Error:       task.Error,
		StartedAt:   task.StartedAt,
		CompletedAt: task.CompletedAt,
		Summary:     summary,
	}, nil
}

func (s *ExecutionService) getWorkflowExecution(executionID string) (*ExecutionView, error) {
	if s.workflowEngine == nil {
		return nil, fmt.Errorf("workflow engine is not configured")
	}

	execution, err := s.workflowEngine.GetExecution(executionID)
	if err != nil {
		return nil, err
	}

	steps := make([]ExecutionStepView, 0, len(execution.StepExecutions))
	for _, stepExec := range execution.StepExecutions {
		startedAt := stepExec.StartedAt
		steps = append(steps, ExecutionStepView{
			ID:          stepExec.StepID,
			Status:      mapWorkflowStatus(stepExec.Status),
			Error:       stepExec.Error,
			Result:      stepExec.Result,
			StartedAt:   &startedAt,
			CompletedAt: stepExec.CompletedAt,
			Metadata:    stepExec.Metadata,
		})
	}

	summary := map[string]interface{}{
		"workflow_id":       execution.WorkflowID,
		"current_step":      execution.CurrentStep,
		"recovery_status":   execution.RecoveryStatus,
		"route_selections":  execution.RouteSelections,
		"step_count":        len(execution.StepExecutions),
		"checkpoint_count":  len(execution.Checkpoints),
		"restored_step_ids": execution.ResumeState,
	}
	if execution.SupersededByExecutionID != "" {
		summary["superseded_by_execution_id"] = execution.SupersededByExecutionID
	}
	if execution.Context != nil {
		summary["variables"] = execution.Context.Variables
		summary["outputs"] = execution.Context.Outputs
	}

	startedAt := execution.StartedAt
	return &ExecutionView{
		ID:                      execution.ID,
		Type:                    ExecutionTypeWorkflow,
		Status:                  mapWorkflowStatus(execution.Status),
		RecoveryStatus:          string(execution.RecoveryStatus),
		SupersededByExecutionID: execution.SupersededByExecutionID,
		Error:                   execution.Error,
		StartedAt:               &startedAt,
		CompletedAt:             execution.CompletedAt,
		Summary:                 summary,
		Steps:                   steps,
	}, nil
}

func (s *ExecutionService) rememberExecutionType(executionID string, executionType ExecutionType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executionTypes[executionID] = executionType
}

func (s *ExecutionService) executionType(executionID string) ExecutionType {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.executionTypes[executionID]
}

func mapTaskStatus(status TaskStatus) UnifiedExecutionStatus {
	switch status {
	case TaskStatusPending:
		return ExecutionStatusPending
	case TaskStatusRunning:
		return ExecutionStatusRunning
	case TaskStatusCompleted:
		return ExecutionStatusCompleted
	case TaskStatusFailed:
		return ExecutionStatusFailed
	case TaskStatusCancelled:
		return ExecutionStatusCancelled
	default:
		return UnifiedExecutionStatus(status)
	}
}

func mapWorkflowStatus(status workflow.WorkflowStatus) UnifiedExecutionStatus {
	switch status {
	case workflow.WorkflowStatusPending:
		return ExecutionStatusPending
	case workflow.WorkflowStatusRunning:
		return ExecutionStatusRunning
	case workflow.WorkflowStatusCompleted:
		return ExecutionStatusCompleted
	case workflow.WorkflowStatusFailed:
		return ExecutionStatusFailed
	case workflow.WorkflowStatusSkipped:
		return ExecutionStatusSkipped
	case workflow.WorkflowStatusCancelled:
		return ExecutionStatusCancelled
	default:
		return UnifiedExecutionStatus(status)
	}
}
