package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// ExecutionState 抽象了工作流执行状态的写入入口。
// 这样可以把“更新内存状态”和“发送运行时事件/持久化快照”等副作用集中管理。
type ExecutionState interface {
	StartStep(execution *WorkflowExecution, step *Step, opts StepStartOptions) *StepExecution
	CompleteStep(execution *WorkflowExecution, step *Step, stepExec *StepExecution, result map[string]interface{}) error
	FailStep(execution *WorkflowExecution, stepExec *StepExecution, err error) error
	SkipStep(execution *WorkflowExecution, step *Step, reason string)
	ApplyRoute(execution *WorkflowExecution, step *Step, stepExec *StepExecution) error
	FinishExecution(execution *WorkflowExecution, status WorkflowStatus, message string)
	SaveCheckpoint(execution *WorkflowExecution, stepID string, checkpoint *StreamCheckpoint) error
}

// workflowExecutionState 是 Engine 对 ExecutionState 的默认实现。
type workflowExecutionState struct {
	engine *Engine
}

func (e *Engine) executionState() *workflowExecutionState {
	return &workflowExecutionState{engine: e}
}

// StartStep 初始化步骤执行记录，并发布开始事件。
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

// CompleteStep 使用默认完成选项结束一个步骤。
func (s *workflowExecutionState) CompleteStep(execution *WorkflowExecution, step *Step, stepExec *StepExecution, result map[string]interface{}) error {
	return s.CompleteStepWithOptions(execution, step, stepExec, result, StepCompletionOptions{})
}

// FailStep 将步骤标记为失败，并记录结构化错误信息与运行时事件。
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

// SkipStep 将步骤标记为跳过，并把原因写入 metadata。
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

// ApplyRoute 在路由步骤完成后计算本次命中的分支，并写回执行状态。
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

// FinishExecution 在工作流结束时统一封口状态、错误信息和完成时间。
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

// SaveCheckpoint 保存流式步骤的 checkpoint，用于恢复和观测。
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
