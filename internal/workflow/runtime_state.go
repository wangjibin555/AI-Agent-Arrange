package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// ExecutionState centralizes workflow execution state mutations and side effects.
type ExecutionState interface {
	StartStep(execution *WorkflowExecution, step *Step, opts StepStartOptions) *StepExecution
	CompleteStep(execution *WorkflowExecution, step *Step, stepExec *StepExecution, result map[string]interface{}) error
	FailStep(execution *WorkflowExecution, stepExec *StepExecution, err error) error
	SkipStep(execution *WorkflowExecution, step *Step, reason string)
	ApplyRoute(execution *WorkflowExecution, step *Step, stepExec *StepExecution) error
	FinishExecution(execution *WorkflowExecution, status WorkflowStatus, message string)
	SaveCheckpoint(execution *WorkflowExecution, stepID string, checkpoint *StreamCheckpoint) error
}

type workflowExecutionState struct {
	engine *Engine
}

func (e *Engine) executionState() *workflowExecutionState {
	return &workflowExecutionState{engine: e}
}

func (s *workflowExecutionState) StartStep(execution *WorkflowExecution, step *Step, opts StepStartOptions) *StepExecution {
	now := time.Now()
	stepExec := &StepExecution{
		StepID:    step.ID,
		Status:    WorkflowStatusRunning,
		StartedAt: now,
		Metadata: map[string]interface{}{
			"step_type": workflowStepType(step),
		},
	}

	s.engine.mu.Lock()
	execution.StepExecutions[step.ID] = stepExec
	execution.CurrentStep = step.ID
	s.engine.mu.Unlock()

	eventType := opts.EventType
	if eventType == "" {
		eventType = "step_started"
	}
	message := opts.Message
	if message == "" {
		message = fmt.Sprintf("Step %s started", step.ID)
	}
	eventData := opts.EventData
	if eventData == nil {
		eventData = map[string]interface{}{"step_id": step.ID}
	}

	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:            eventType,
		Status:          string(WorkflowStatusRunning),
		Message:         message,
		Data:            eventData,
		Execution:       execution,
		Step:            step,
		StepExecution:   stepExec,
		PersistSnapshot: true,
	})

	return stepExec
}

func (s *workflowExecutionState) CompleteStep(execution *WorkflowExecution, step *Step, stepExec *StepExecution, result map[string]interface{}) error {
	return s.CompleteStepWithOptions(execution, step, stepExec, result, StepCompletionOptions{})
}

func (s *workflowExecutionState) FailStep(execution *WorkflowExecution, stepExec *StepExecution, err error) error {
	stepExec.Status = WorkflowStatusFailed
	stepExec.Error = err.Error()
	if stepExec.Metadata == nil {
		stepExec.Metadata = make(map[string]interface{})
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Code != "" {
		stepExec.Metadata["error_code"] = appErr.Code
	}
	now := time.Now()
	stepExec.CompletedAt = &now
	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:            "step_failed",
		Status:          string(WorkflowStatusFailed),
		Message:         fmt.Sprintf("Step %s failed: %v", stepExec.StepID, err),
		Data:            map[string]interface{}{"step_id": stepExec.StepID, "error": err.Error()},
		Execution:       execution,
		StepExecution:   stepExec,
		PersistSnapshot: true,
	})

	return err
}

func (s *workflowExecutionState) SkipStep(execution *WorkflowExecution, step *Step, reason string) {
	now := time.Now()
	stepExec := &StepExecution{
		StepID:      step.ID,
		Status:      WorkflowStatusSkipped,
		StartedAt:   now,
		CompletedAt: &now,
		Metadata: map[string]interface{}{
			"skip_reason": reason,
			"step_type":   workflowStepType(step),
		},
	}

	s.engine.mu.Lock()
	execution.StepExecutions[step.ID] = stepExec
	s.engine.mu.Unlock()
	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:            "step_skipped",
		Status:          string(WorkflowStatusSkipped),
		Message:         fmt.Sprintf("Step %s skipped", step.ID),
		Data:            map[string]interface{}{"step_id": step.ID, "reason": reason},
		Execution:       execution,
		Step:            step,
		StepExecution:   stepExec,
		PersistSnapshot: true,
	})
}

func (s *workflowExecutionState) ApplyRoute(execution *WorkflowExecution, step *Step, stepExec *StepExecution) error {
	if step == nil || step.Route == nil {
		return nil
	}

	selectedRoute, targets, err := s.engine.resolveRouteSelection(step, execution)
	if err != nil {
		return apperr.Internal("failed to apply route selection").WithCode("workflow_route_apply_failed").WithCause(err)
	}
	if execution.RouteSelections == nil {
		execution.RouteSelections = make(map[string]string)
	}
	s.engine.mu.Lock()
	execution.RouteSelections[step.ID] = selectedRoute
	s.engine.mu.Unlock()
	if stepExec.Metadata == nil {
		stepExec.Metadata = make(map[string]interface{})
	}
	stepExec.Metadata["route"] = selectedRoute
	stepExec.Metadata["route_targets"] = targets
	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:          "step_routed",
		Status:        string(WorkflowStatusCompleted),
		Message:       fmt.Sprintf("Step %s selected route %s", step.ID, selectedRoute),
		Data:          map[string]interface{}{"step_id": step.ID, "route": selectedRoute, "targets": targets},
		Execution:     execution,
		Step:          step,
		StepExecution: stepExec,
	})
	return nil
}

func (s *workflowExecutionState) FinishExecution(execution *WorkflowExecution, status WorkflowStatus, message string) {
	s.engine.mu.Lock()
	if execution.CompletedAt != nil || (execution.Status != WorkflowStatusRunning && execution.Status != WorkflowStatusPending) {
		s.engine.mu.Unlock()
		return
	}
	execution.Status = status
	if status == WorkflowStatusFailed || status == WorkflowStatusCancelled {
		execution.Error = message
	}
	now := time.Now()
	execution.CompletedAt = &now
	delete(s.engine.executionStops, execution.ID)
	s.engine.mu.Unlock()

	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:            "workflow_completed",
		Status:          string(status),
		Message:         message,
		Execution:       execution,
		PersistSnapshot: true,
	})
}

func (s *workflowExecutionState) SaveCheckpoint(execution *WorkflowExecution, stepID string, checkpoint *StreamCheckpoint) error {
	if checkpoint == nil {
		return nil
	}

	s.engine.mu.Lock()
	if execution.Checkpoints == nil {
		execution.Checkpoints = make(map[string]*StreamCheckpoint)
	}
	execution.Checkpoints[stepID] = checkpoint.Clone()
	s.engine.mu.Unlock()

	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:            "checkpoint_saved",
		Status:          string(execution.Status),
		Message:         fmt.Sprintf("Checkpoint saved for step %s", stepID),
		Data:            map[string]interface{}{"step_id": stepID},
		Execution:       execution,
		PersistSnapshot: true,
	})
	return nil
}
