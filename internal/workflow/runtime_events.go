package workflow

import (
	"context"
	"time"
)

type RuntimeEvent struct {
	Type                    string
	Status                  string
	Message                 string
	Data                    map[string]interface{}
	Execution               *WorkflowExecution
	Workflow                *Workflow
	Step                    *Step
	StepExecution           *StepExecution
	PersistSnapshot         bool
	RecoveryStatus          string
	SupersededByExecutionID string
}

type RuntimeEventHandler interface {
	Handle(context.Context, RuntimeEvent)
}

type runtimeEventDispatcher struct {
	handlers []RuntimeEventHandler
}

func newRuntimeEventDispatcher(handlers ...RuntimeEventHandler) *runtimeEventDispatcher {
	return &runtimeEventDispatcher{handlers: handlers}
}

func (d *runtimeEventDispatcher) Emit(ctx context.Context, event RuntimeEvent) {
	if d == nil {
		return
	}
	for _, handler := range d.handlers {
		handler.Handle(ctx, event)
	}
}

type snapshotProjection struct {
	engine *Engine
}

func (p snapshotProjection) Handle(ctx context.Context, event RuntimeEvent) {
	if !event.PersistSnapshot || p.engine == nil || event.Execution == nil {
		return
	}
	_ = p.engine.persistExecutionSnapshot(ctx, event.Execution)
}

type publisherProjection struct {
	engine *Engine
}

func (p publisherProjection) Handle(_ context.Context, event RuntimeEvent) {
	if p.engine == nil || p.engine.eventPublisher == nil || event.Execution == nil || event.Type == "" {
		return
	}

	data := copyMap(event.Data)
	if data == nil {
		data = make(map[string]interface{})
	}
	if event.RecoveryStatus != "" {
		data["recovery_status"] = event.RecoveryStatus
	}
	if event.SupersededByExecutionID != "" {
		data["superseded_by_execution_id"] = event.SupersededByExecutionID
	}

	p.engine.eventPublisher.PublishWorkflowEvent(
		event.Execution.ID,
		event.Type,
		event.Status,
		event.Message,
		data,
	)
}

type workflowMetricsProjection struct {
	engine *Engine
}

func (p workflowMetricsProjection) Handle(_ context.Context, event RuntimeEvent) {
	if p.engine == nil || p.engine.metrics == nil || event.Execution == nil {
		return
	}

	switch event.Type {
	case "workflow_started":
		workflowID := event.Execution.WorkflowID
		if event.Workflow != nil && event.Workflow.ID != "" {
			workflowID = event.Workflow.ID
		}
		p.engine.metrics.ObserveWorkflowStarted(workflowID)
	case "workflow_completed":
		completedAt := time.Now()
		if event.Execution.CompletedAt != nil {
			completedAt = *event.Execution.CompletedAt
		}
		if event.Status == string(WorkflowStatusCancelled) {
			p.engine.metrics.ObserveCancel(event.Execution.WorkflowID)
		}
		p.engine.metrics.ObserveWorkflowFinished(event.Execution.WorkflowID, event.Status, completedAt.Sub(event.Execution.StartedAt))
	case "step_started":
		if event.Step == nil || event.StepExecution == nil {
			return
		}
		p.engine.metrics.ObserveStepStarted(event.Execution.WorkflowID, event.Step.ID, workflowStepType(event.Step))
	case "step_completed", "step_completed_partial", "step_completed_fallback":
		if event.StepExecution == nil {
			return
		}
		p.engine.observeStepFinished(event.Execution.WorkflowID, event.StepExecution, string(WorkflowStatusCompleted))
	case "step_failed":
		if event.StepExecution == nil {
			return
		}
		p.engine.metrics.ObserveStepFailed(event.Execution.WorkflowID, event.StepExecution.StepID)
		p.engine.observeStepFinished(event.Execution.WorkflowID, event.StepExecution, string(WorkflowStatusFailed))
	case "step_skipped":
		if event.Step == nil {
			return
		}
		reason, _ := event.Data["reason"].(string)
		p.engine.metrics.ObserveStepSkipped(event.Execution.WorkflowID, event.Step.ID, reason)
	case "workflow_interrupted", "workflow_superseded":
		if event.RecoveryStatus != "" {
			p.engine.metrics.ObserveRecovery(event.Execution.WorkflowID, event.RecoveryStatus)
		}
	}
}

func (e *Engine) newRuntimeEventDispatcher() *runtimeEventDispatcher {
	return newRuntimeEventDispatcher(
		snapshotProjection{engine: e},
		publisherProjection{engine: e},
		workflowMetricsProjection{engine: e},
	)
}

func (e *Engine) emitRuntimeEvent(ctx context.Context, event RuntimeEvent) {
	if e == nil || e.runtimeEvents == nil {
		return
	}
	e.runtimeEvents.Emit(ctx, event)
}
