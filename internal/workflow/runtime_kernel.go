package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

type executionBootstrapOptions struct {
	Variables             map[string]interface{}
	InitializeCheckpoints bool
}

type runtimeRunResult struct {
	Status  WorkflowStatus
	Message string
}

type stepExecutor interface {
	Execute(ctx context.Context, step *CompiledStep, execution *WorkflowExecution) error
}

type workflowStepExecutor struct {
	engine    *Engine
	streaming *StreamingEngine
}

type runtimeKernel struct {
	engine         *Engine
	streaming      *StreamingEngine
	workflow       *CompiledWorkflow
	execution      *WorkflowExecution
	executor       stepExecutor
	rollback       func(context.Context, map[string]bool)
	completed      map[string]bool
	failed         map[string]bool
	running        map[string]bool
	triggerManager *TriggerManager
	mu             sync.Mutex
}

func (e *Engine) bootstrapExecution(
	ctx context.Context,
	workflow *Workflow,
	compiled *CompiledWorkflow,
	opts executionBootstrapOptions,
) (*WorkflowExecution, context.Context, error) {
	if compiled == nil {
		return nil, nil, apperr.InvalidArgument("compiled workflow is required").WithCode("workflow_compiled_required")
	}

	execution := &WorkflowExecution{
		ID:              uuid.New().String(),
		WorkflowID:      compiled.ID,
		Status:          WorkflowStatusRunning,
		RecoveryStatus:  RecoveryStatusNone,
		StepExecutions:  make(map[string]*StepExecution),
		Context:         NewExecutionContext(opts.Variables),
		RouteSelections: make(map[string]string),
		StartedAt:       time.Now(),
	}
	if opts.InitializeCheckpoints {
		execution.Checkpoints = make(map[string]*StreamCheckpoint)
	}

	for k, v := range workflow.Variables {
		execution.Context.SetVariable(k, v)
	}

	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	e.registerExecutionStop(execution.ID, cancel)

	if err := e.persistWorkflowDefinition(ctx, workflow); err != nil {
		return nil, nil, apperr.Internal("failed to save workflow definition").
			WithCode("workflow_definition_persist_failed").
			WithCause(err)
	}
	e.emitRuntimeEvent(ctx, RuntimeEvent{
		Type:            "workflow_started",
		Status:          string(WorkflowStatusRunning),
		Message:         "Workflow execution started",
		Execution:       execution,
		Workflow:        workflow,
		PersistSnapshot: true,
	})

	return execution, runCtx, nil
}

func (e *Engine) runExecutionLifecycle(
	ctx context.Context,
	workflow *Workflow,
	execution *WorkflowExecution,
	run func(context.Context) runtimeRunResult,
) {
	timeout := e.defaultTimeout
	if workflow.Timeout != nil {
		timeout = *workflow.Timeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result := run(execCtx)

	switch {
	case execCtx.Err() == context.Canceled:
		e.finishExecution(execution, WorkflowStatusCancelled, "Execution cancelled by user")
	case execCtx.Err() == context.DeadlineExceeded:
		e.finishExecution(execution, WorkflowStatusFailed, "Workflow execution timeout")
	case result.Status != "":
		e.finishExecution(execution, result.Status, result.Message)
	default:
		e.finishExecution(execution, WorkflowStatusCompleted, "Workflow completed successfully")
	}
}

func (e *Engine) newStepExecutor() stepExecutor {
	return workflowStepExecutor{engine: e}
}

func (e *StreamingEngine) newStepExecutor() stepExecutor {
	return workflowStepExecutor{
		engine:    e.Engine,
		streaming: e,
	}
}

func (x workflowStepExecutor) Execute(ctx context.Context, step *CompiledStep, execution *WorkflowExecution) error {
	runtimeStep := step.Runtime
	switch {
	case runtimeStep.Foreach != nil:
		return NewStepExecutionTemplate(x.engine).Execute(ctx, runtimeStep, execution, newForeachStepRunner(x.engine))
	case x.streaming != nil && runtimeStep.Streaming != nil && runtimeStep.Streaming.Enabled:
		return NewStepExecutionTemplate(x.engine).Execute(ctx, runtimeStep, execution, newStreamingStepRunner(x.streaming))
	default:
		return NewStepExecutionTemplate(x.engine).Execute(ctx, runtimeStep, execution, newBlockingStepRunner(x.engine))
	}
}

func newRuntimeKernel(
	engine *Engine,
	streaming *StreamingEngine,
	workflow *CompiledWorkflow,
	execution *WorkflowExecution,
	executor stepExecutor,
	completed map[string]bool,
	failed map[string]bool,
	rollback func(context.Context, map[string]bool),
) *runtimeKernel {
	kernel := &runtimeKernel{
		engine:         engine,
		streaming:      streaming,
		workflow:       workflow,
		execution:      execution,
		executor:       executor,
		rollback:       rollback,
		completed:      completed,
		failed:         failed,
		running:        make(map[string]bool),
		triggerManager: NewTriggerManager(),
	}

	for _, step := range workflow.Steps {
		buffers := make(map[string]*StreamBuffer)
		if streaming != nil {
			for _, depID := range step.Runtime.DependsOn {
				if buffer := streaming.getBuffer(execution.ID, depID); buffer != nil {
					buffers[depID] = buffer
				}
			}
		}
		kernel.triggerManager.RegisterTrigger(step.ID, NewMultiTrigger(step.ID, step.Runtime, buffers))
	}

	return kernel
}

func (k *runtimeKernel) Run(ctx context.Context) runtimeRunResult {
	semaphore := make(chan struct{}, k.engine.maxConcurrency)
	var wg sync.WaitGroup
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if k.allStepsTerminal() {
			break
		}

		k.tryScheduleReadySteps(ctx, &wg, semaphore)

		select {
		case <-ctx.Done():
			wg.Wait()
			return runtimeRunResult{}
		case <-ticker.C:
		}
	}

	wg.Wait()

	k.mu.Lock()
	hasFailed := len(k.failed) > 0
	failedStepList := make([]string, 0, len(k.failed))
	for stepID := range k.failed {
		failedStepList = append(failedStepList, stepID)
	}
	k.mu.Unlock()

	if hasFailed {
		if k.workflow.OnFailure != nil && k.workflow.OnFailure.Rollback && k.rollback != nil {
			k.rollback(ctx, k.copyCompletedSteps())
		}
		return runtimeRunResult{
			Status:  WorkflowStatusFailed,
			Message: fmt.Sprintf("Workflow failed: steps %v failed", failedStepList),
		}
	}

	return runtimeRunResult{
		Status:  WorkflowStatusCompleted,
		Message: "Workflow completed successfully",
	}
}

func (k *runtimeKernel) tryScheduleReadySteps(ctx context.Context, wg *sync.WaitGroup, semaphore chan struct{}) {
	for _, step := range k.workflow.Steps {
		k.mu.Lock()
		if k.running[step.ID] || k.completed[step.ID] || k.failed[step.ID] {
			k.mu.Unlock()
			continue
		}
		k.mu.Unlock()

		decision, reason, err := k.engine.evaluateStepDecision(k.workflow, step, k.execution, k.copyCompletedSteps(), k.copyFailedSteps())
		if err != nil {
			k.markDecisionFailure(step, err)
			continue
		}
		if decision == StepDecisionWait {
			continue
		}
		if decision == StepDecisionSkip {
			k.mu.Lock()
			k.completed[step.ID] = true
			k.mu.Unlock()
			k.engine.skipStep(step.Runtime, k.execution, reason)
			if trigger := k.triggerManager.GetTrigger(step.ID); trigger != nil {
				trigger.MarkTriggered()
			}
			continue
		}

		start, triggerReason := k.canStartStep(step)
		if !start {
			continue
		}

		k.mu.Lock()
		if k.running[step.ID] || k.completed[step.ID] || k.failed[step.ID] {
			k.mu.Unlock()
			continue
		}
		k.running[step.ID] = true
		k.mu.Unlock()

		if triggerReason != "" {
			k.engine.publishEvent(k.execution.ID, "step_triggered", string(WorkflowStatusRunning),
				fmt.Sprintf("Step %s triggered (reason: %s)", step.ID, triggerReason),
				map[string]interface{}{"step_id": step.ID, "reason": triggerReason})
		}

		wg.Add(1)
		semaphore <- struct{}{}
		go func(currentStep *CompiledStep) {
			defer wg.Done()
			defer func() { <-semaphore }()

			err := k.executor.Execute(ctx, currentStep, k.execution)

			k.mu.Lock()
			delete(k.running, currentStep.ID)
			if err != nil {
				k.failed[currentStep.ID] = true
			} else {
				k.completed[currentStep.ID] = true
			}
			k.mu.Unlock()
		}(step)
	}
}

func (k *runtimeKernel) canStartStep(step *CompiledStep) (bool, string) {
	if step == nil || step.Runtime == nil {
		return false, ""
	}
	if k.streaming == nil || step.Runtime.Streaming == nil || !step.Runtime.Streaming.Enabled {
		return true, ""
	}
	if step.Runtime.Streaming.WaitFor != "partial" {
		return true, ""
	}

	trigger := k.triggerManager.GetTrigger(step.ID)
	if trigger == nil {
		return true, ""
	}

	buffers := make(map[string]*StreamBuffer)
	for _, edge := range k.workflow.Incoming[step.ID] {
		if edge.Kind != CompiledEdgeDependency && edge.Kind != CompiledEdgeForeach {
			continue
		}
		if buffer := k.streaming.getBuffer(k.execution.ID, edge.FromStepID); buffer != nil {
			buffers[edge.FromStepID] = buffer
		}
	}
	trigger.UpdateBuffers(buffers)

	shouldTrigger, reason := trigger.ShouldTrigger()
	if shouldTrigger {
		trigger.MarkTriggered()
		return true, reason
	}
	if k.execution.ResumeState != nil {
		allDepsCompleted := true
		for _, edge := range k.workflow.Incoming[step.ID] {
			if edge.Kind != CompiledEdgeDependency && edge.Kind != CompiledEdgeForeach {
				continue
			}
			k.mu.Lock()
			completed := k.completed[edge.FromStepID]
			k.mu.Unlock()
			if !completed {
				allDepsCompleted = false
				break
			}
		}
		if allDepsCompleted {
			trigger.MarkTriggered()
			return true, "resume_all_deps_completed"
		}
	}

	return false, ""
}

func (k *runtimeKernel) allStepsTerminal() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.completed)+len(k.failed) >= len(k.workflow.Steps)
}

func (k *runtimeKernel) copyCompletedSteps() map[string]bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make(map[string]bool, len(k.completed))
	for stepID, completed := range k.completed {
		out[stepID] = completed
	}
	return out
}

func (k *runtimeKernel) copyFailedSteps() map[string]bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make(map[string]bool, len(k.failed))
	for stepID, failed := range k.failed {
		out[stepID] = failed
	}
	return out
}

func (k *runtimeKernel) markDecisionFailure(step *CompiledStep, err error) {
	k.mu.Lock()
	k.failed[step.ID] = true
	k.mu.Unlock()
	k.engine.failStep(&StepExecution{
		StepID:    step.ID,
		Status:    WorkflowStatusRunning,
		StartedAt: time.Now(),
	}, k.execution, apperr.Internal("failed to evaluate step decision").
		WithCode("workflow_step_decision_failed").
		WithCause(err))
}
