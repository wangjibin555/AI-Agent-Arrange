package workflow

import (
	"context"
	"fmt"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

type foreachStepRunner struct {
	engine *Engine
	items  []interface{}
}

func newForeachStepRunner(engine *Engine) *foreachStepRunner {
	return &foreachStepRunner{engine: engine}
}

func (r *foreachStepRunner) StartOptions(_ *Step) StepStartOptions {
	return StepStartOptions{}
}

func (r *foreachStepRunner) Prepare(_ context.Context, step *Step, execution *WorkflowExecution, _ *StepExecution) error {
	items, err := r.engine.resolveForeachItems(step, execution)
	if err != nil {
		return apperr.InvalidArgument("failed to resolve foreach items").WithCode("workflow_foreach_resolve_failed").WithCause(err)
	}
	r.items = items
	return nil
}

func (r *foreachStepRunner) RunAttempt(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	_ *StepExecution,
	_ int,
) (*StepAttemptOutcome, error) {
	results, err := r.engine.executeForeachItems(ctx, step, execution, r.items)
	if err != nil {
		return nil, err
	}

	aggregated := map[string]interface{}{
		"items": results,
		"count": len(results),
	}

	return &StepAttemptOutcome{
		Result: aggregated,
		CompletionOptions: StepCompletionOptions{
			EventType: "step_completed",
			Message:   fmt.Sprintf("Step %s completed successfully", step.ID),
			EventData: map[string]interface{}{
				"step_id":    step.ID,
				"result":     aggregated,
				"item_count": len(results),
				"foreach":    true,
			},
		},
	}, nil
}

func (r *foreachStepRunner) HandlePrepareError(
	_ context.Context,
	_ *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	err error,
) error {
	return r.engine.failStep(stepExec, execution, err)
}

func (r *foreachStepRunner) HandleExhausted(
	_ context.Context,
	_ *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	lastErr error,
) error {
	return r.engine.failStep(stepExec, execution, lastErr)
}
