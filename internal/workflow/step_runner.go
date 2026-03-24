package workflow

import (
	"context"
	"fmt"
)

// StepAttemptOutcome describes a successful single attempt result before lifecycle finalization.
type StepAttemptOutcome struct {
	Result            map[string]interface{}
	CompletionOptions StepCompletionOptions
}

// StepRunner encapsulates one step execution mode behind a shared lifecycle template.
type StepRunner interface {
	StartOptions(step *Step) StepStartOptions
	Prepare(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution) error
	RunAttempt(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution, attempt int) (*StepAttemptOutcome, error)
	HandlePrepareError(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution, err error) error
	HandleExhausted(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution, lastErr error) error
}

// StepExecutionTemplate centralizes common step lifecycle orchestration.
type StepExecutionTemplate struct {
	engine *Engine
	state  *workflowExecutionState
}

func NewStepExecutionTemplate(engine *Engine) *StepExecutionTemplate {
	return &StepExecutionTemplate{
		engine: engine,
		state:  engine.executionState(),
	}
}

func (t *StepExecutionTemplate) Execute(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	runner StepRunner,
) error {
	stepExec := t.state.StartStep(execution, step, runner.StartOptions(step))

	if err := runner.Prepare(ctx, step, execution, stepExec); err != nil {
		return runner.HandlePrepareError(ctx, step, execution, stepExec, err)
	}

	maxRetries := normalizedStepRetries(step)
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			stepExec.RetryCount++
			t.publishRetryEvent(execution.ID, step.ID, attempt, maxRetries)
		}

		outcome, err := runner.RunAttempt(ctx, step, execution, stepExec, attempt)
		if err != nil {
			lastErr = err
			continue
		}

		if outcome == nil {
			return nil
		}

		opts := outcome.CompletionOptions
		if opts.isZero() {
			return t.state.CompleteStep(execution, step, stepExec, outcome.Result)
		}
		return t.state.CompleteStepWithOptions(execution, step, stepExec, outcome.Result, opts)
	}

	return runner.HandleExhausted(ctx, step, execution, stepExec, lastErr)
}

func (t *StepExecutionTemplate) publishRetryEvent(executionID, stepID string, attempt, maxRetries int) {
	t.engine.publishEvent(executionID, "step_retry", string(WorkflowStatusRunning),
		fmt.Sprintf("Retrying step %s (attempt %d/%d)", stepID, attempt+1, maxRetries),
		map[string]interface{}{"step_id": stepID, "retry_count": attempt})
}

func normalizedStepRetries(step *Step) int {
	if step == nil || step.Retries == 0 {
		return 1
	}
	return step.Retries
}
