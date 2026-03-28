package workflow

import (
	"context"
	"fmt"
)

// StepAttemptOutcome 表示某次步骤尝试成功后的产物。
// 它还未真正落盘到执行状态，需要再经过统一的生命周期收尾逻辑。
type StepAttemptOutcome struct {
	Result            map[string]interface{}
	CompletionOptions StepCompletionOptions
}

// StepRunner 抽象一种步骤执行模式。
// 普通阻塞步骤、流式步骤、foreach 步骤都复用同一套生命周期模板，只替换具体执行策略。
type StepRunner interface {
	StartOptions(step *Step) StepStartOptions
	Prepare(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution) error
	RunAttempt(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution, attempt int) (*StepAttemptOutcome, error)
	HandlePrepareError(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution, err error) error
	HandleExhausted(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution, lastErr error) error
}

// StepExecutionTemplate 抽取步骤执行的公共生命周期模板。
// 它统一处理开始、准备、重试、完成和失败收尾，避免各类 StepRunner 重复实现。
type StepExecutionTemplate struct {
	engine *Engine
	state  *workflowExecutionState
}

// NewStepExecutionTemplate 创建步骤生命周期模板执行器。
func NewStepExecutionTemplate(engine *Engine) *StepExecutionTemplate {
	return &StepExecutionTemplate{
		engine: engine,
		state:  engine.executionState(),
	}
}

// Execute 按统一模板执行一个步骤，并把差异化逻辑委托给具体的 StepRunner。
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

// publishRetryEvent 发布步骤重试事件，便于前端和监控感知当前重试进度。
func (t *StepExecutionTemplate) publishRetryEvent(executionID, stepID string, attempt, maxRetries int) {
	t.engine.publishEvent(executionID, "step_retry", string(WorkflowStatusRunning),
		fmt.Sprintf("Retrying step %s (attempt %d/%d)", stepID, attempt+1, maxRetries),
		map[string]interface{}{"step_id": stepID, "retry_count": attempt})
}

// normalizedStepRetries 将步骤重试配置归一化为实际执行次数。
func normalizedStepRetries(step *Step) int {
	if step == nil || step.Retries == 0 {
		return 1
	}
	return step.Retries
}
