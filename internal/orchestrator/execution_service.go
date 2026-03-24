package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/workflow"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
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
	GetExecution(ctx context.Context, executionID string) (*workflow.WorkflowExecution, error)
	CancelExecution(ctx context.Context, executionID string) error
}

// ExecutionService unifies task and workflow execution behind one service boundary.
type ExecutionService struct {
	orchestrator   *Engine
	workflowEngine WorkflowRunner

	mu             sync.RWMutex
	executionTypes map[string]ExecutionType
	metrics        *monitor.TaskMetrics
}

// NewExecutionService creates a new unified execution service.
func NewExecutionService(orchestratorEngine *Engine, workflowEngine WorkflowRunner) *ExecutionService {
	return &ExecutionService{
		orchestrator:   orchestratorEngine,
		workflowEngine: workflowEngine,
		executionTypes: make(map[string]ExecutionType),
	}
}

func (s *ExecutionService) SetMetrics(metrics *monitor.TaskMetrics) {
	s.metrics = metrics
}

// ExecuteTask submits a task through the orchestrator engine and returns a unified handle.
func (s *ExecutionService) ExecuteTask(ctx context.Context, req *TaskExecutionRequest) (*ExecutionHandle, error) {
	start := time.Now()
	const execType = ExecutionTypeTask
	if s.metrics != nil {
		s.metrics.IncExecutionInflight(string(execType))
		defer s.metrics.DecExecutionInflight(string(execType))
	}

	if req == nil {
		err := apperr.InvalidArgument("task execution request is nil").WithCode("task_request_invalid")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if s.orchestrator == nil {
		err := apperr.Internal("orchestrator engine is not configured")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if req.Action == "" {
		err := apperr.InvalidArgument("task action is required").WithCode("task_action_required")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if req.AgentName != "" && req.Capability != "" {
		err := apperr.InvalidArgument("cannot specify both 'agent_name' and 'capability', choose one").WithCode("task_agent_capability_conflict")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if req.AgentName == "" && req.Capability == "" {
		err := apperr.InvalidArgument("either agent name or capability is required").WithCode("task_agent_or_capability_required")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
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
	if meta, ok := midware.RequestMetadataFromContext(ctx); ok {
		task.RequestMetadata = midware.RequestMetadataToMap(meta)
	}

	if err := s.orchestrator.SubmitTask(ctx, task); err != nil {
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}

	s.rememberExecutionType(taskID, ExecutionTypeTask)
	s.observeExecutionRequest(execType, "success", start)
	return &ExecutionHandle{ID: taskID, Type: ExecutionTypeTask}, nil
}

// ExecuteWorkflow runs a workflow through the configured workflow engine.
func (s *ExecutionService) ExecuteWorkflow(ctx context.Context, req *WorkflowExecutionRequest) (*ExecutionHandle, error) {
	start := time.Now()
	const execType = ExecutionTypeWorkflow
	if s.metrics != nil {
		s.metrics.IncExecutionInflight(string(execType))
		defer s.metrics.DecExecutionInflight(string(execType))
	}

	if req == nil {
		err := apperr.InvalidArgument("workflow execution request is nil").WithCode("workflow_request_invalid")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if s.workflowEngine == nil {
		err := apperr.Internal("workflow engine is not configured")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if req.Workflow == nil {
		err := apperr.InvalidArgument("workflow definition is required").WithCode("workflow_definition_required")
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}
	if err := workflow.ValidateDefinition(req.Workflow); err != nil {
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}

	execution, err := s.workflowEngine.Execute(ctx, req.Workflow, req.Variables)
	if err != nil {
		s.observeExecutionRequest(execType, "failed", start)
		return nil, err
	}

	s.rememberExecutionType(execution.ID, ExecutionTypeWorkflow)
	s.observeExecutionRequest(execType, "success", start)
	return &ExecutionHandle{ID: execution.ID, Type: ExecutionTypeWorkflow}, nil
}

// GetExecution returns the unified execution view.
func (s *ExecutionService) GetExecution(ctx context.Context, executionID string) (*ExecutionView, error) {
	_ = ctx

	if executionID == "" {
		return nil, apperr.InvalidArgument("execution id is required").WithCode("execution_id_required")
	}

	switch s.executionType(executionID) {
	case ExecutionTypeTask:
		return s.getTaskExecution(ctx, executionID)
	case ExecutionTypeWorkflow:
		return s.getWorkflowExecution(ctx, executionID)
	default:
		if view, err := s.getTaskExecution(ctx, executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeTask)
			return view, nil
		}
		if view, err := s.getWorkflowExecution(ctx, executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeWorkflow)
			return view, nil
		}
		return nil, apperr.NotFoundf("execution %s not found", executionID).WithCode("execution_not_found")
	}
}

// CancelExecution cancels a unified execution if the underlying engine supports it.
func (s *ExecutionService) CancelExecution(ctx context.Context, executionID string) error {
	_ = ctx
	execType := s.executionType(executionID)
	recordCancel := func(status string) {
		if s.metrics != nil {
			s.metrics.ObserveExecutionCancel(string(execType), status)
		}
	}

	switch s.executionType(executionID) {
	case ExecutionTypeTask:
		err := s.cancelTaskExecution(ctx, executionID)
		recordCancel(cancelStatus(err))
		return err
	case ExecutionTypeWorkflow:
		err := s.cancelWorkflowExecution(ctx, executionID)
		recordCancel(cancelStatus(err))
		return err
	default:
		if err := s.cancelTaskExecution(ctx, executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeTask)
			execType = ExecutionTypeTask
			recordCancel("success")
			return nil
		}
		if _, err := s.getWorkflowExecution(ctx, executionID); err == nil {
			s.rememberExecutionType(executionID, ExecutionTypeWorkflow)
			execType = ExecutionTypeWorkflow
			err = s.cancelWorkflowExecution(ctx, executionID)
			recordCancel(cancelStatus(err))
			return err
		}
		recordCancel("not_found")
		return apperr.NotFoundf("execution %s not found", executionID).WithCode("execution_not_found")
	}
}

func (s *ExecutionService) observeExecutionRequest(execType ExecutionType, status string, start time.Time) {
	if s.metrics == nil {
		return
	}
	s.metrics.ObserveExecutionRequest(string(execType), status, time.Since(start))
}

func cancelStatus(err error) string {
	if err == nil {
		return "success"
	}

	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		switch appErr.Kind {
		case apperr.KindNotFound:
			return "not_found"
		case apperr.KindConflict:
			return "conflict"
		default:
			return "failed"
		}
	}
	return "failed"
}

func (s *ExecutionService) cancelTaskExecution(ctx context.Context, executionID string) error {
	if s.orchestrator == nil {
		return apperr.Internal("orchestrator engine is not configured")
	}

	return s.orchestrator.GetTaskManager().CancelTask(ctx, executionID)
}

func (s *ExecutionService) cancelWorkflowExecution(ctx context.Context, executionID string) error {
	if s.workflowEngine == nil {
		return apperr.Internal("workflow engine is not configured")
	}
	return s.workflowEngine.CancelExecution(ctx, executionID)
}

func (s *ExecutionService) getTaskExecution(ctx context.Context, executionID string) (*ExecutionView, error) {
	if s.orchestrator == nil {
		return nil, apperr.Internal("orchestrator engine is not configured")
	}

	task, err := s.orchestrator.GetTask(ctx, executionID)
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

func (s *ExecutionService) getWorkflowExecution(ctx context.Context, executionID string) (*ExecutionView, error) {
	if s.workflowEngine == nil {
		return nil, apperr.Internal("workflow engine is not configured")
	}

	execution, err := s.workflowEngine.GetExecution(ctx, executionID)
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
		summary["variables"] = execution.Context.SnapshotVariables()
		summary["outputs"] = execution.Context.SnapshotOutputs()
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
