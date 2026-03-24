package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

type streamingStepRunner struct {
	engine         *StreamingEngine
	buffer         *StreamBuffer
	renderedParams map[string]interface{}
}

func newStreamingStepRunner(engine *StreamingEngine) *streamingStepRunner {
	return &streamingStepRunner{engine: engine}
}

func (r *streamingStepRunner) StartOptions(step *Step) StepStartOptions {
	return StepStartOptions{
		EventType: "step_started",
		Message:   fmt.Sprintf("Step %s started (streaming mode)", step.ID),
		EventData: map[string]interface{}{"step_id": step.ID, "streaming": true},
	}
}

func (r *streamingStepRunner) Prepare(ctx context.Context, step *Step, execution *WorkflowExecution, stepExec *StepExecution) error {
	r.buffer = NewStreamBuffer(step.ID, step.Streaming)
	r.engine.registerBuffer(execution.ID, step.ID, r.buffer)

	if step.Streaming.WaitFor == "partial" && len(step.DependsOn) > 0 {
		if err := r.engine.subscribeToUpstream(ctx, step, execution, r.buffer); err != nil {
			switch r.engine.getStreamingOnError(step) {
			case "continue":
				r.engine.publishEvent(execution.ID, "step_stream_error_continue", string(WorkflowStatusRunning),
					fmt.Sprintf("Step %s continuing after upstream subscription error", step.ID),
					map[string]interface{}{
						"step_id": step.ID,
						"phase":   "subscribe_upstream",
						"error":   err.Error(),
					})
			case "fallback":
				return apperr.Internal("failed to subscribe upstream").WithCode("workflow_stream_subscribe_failed").WithCause(err)
			default:
				r.buffer.MarkComplete()
				return apperr.Internal("failed to subscribe upstream").WithCode("workflow_stream_subscribe_failed").WithCause(err)
			}
		}
	}

	params, err := r.engine.renderStepParameters(execution.Context, step)
	if err != nil {
		return err
	}
	r.renderedParams = params
	_ = stepExec
	return nil
}

func (r *streamingStepRunner) RunAttempt(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	_ int,
) (*StepAttemptOutcome, error) {
	taskID := uuid.New().String()
	stepExec.TaskID = taskID

	streamCallback := func(chunk map[string]interface{}) {
		r.buffer.Write(StreamChunk{
			Index:     r.buffer.TotalChunks(),
			Data:      chunk,
			Tokens:    r.engine.estimateTokens(chunk),
			Timestamp: time.Now(),
		})
		r.engine.saveStreamingCheckpoint(execution, step.ID, r.buffer, nil)

		r.engine.publishEvent(execution.ID, "step_streaming", string(WorkflowStatusRunning),
			fmt.Sprintf("Step %s streaming data", step.ID),
			map[string]interface{}{
				"step_id":      step.ID,
				"chunk_index":  r.buffer.TotalChunks() - 1,
				"total_tokens": r.buffer.TotalTokens(),
			})
	}

	taskInput := &agent.TaskInput{
		TaskID:         taskID,
		Action:         step.Action,
		Parameters:     r.renderedParams,
		Context:        make(map[string]interface{}),
		StreamCallback: streamCallback,
		ContextReader:  r.engine.newExecutionContextReader(execution),
	}
	if r.engine.eventPublisher != nil {
		taskInput.EventPublisher = &agentEventPublisherAdapter{
			workflowPublisher: r.engine.eventPublisher,
			executionID:       execution.ID,
			stepID:            step.ID,
		}
	}

	ag, err := r.engine.resolveStepAgent(step)
	if err != nil {
		return nil, err
	}

	stepCtx := ctx
	if step.Timeout != nil {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
		defer cancel()
	}

	attemptCtx, cancelAttempt := context.WithCancel(stepCtx)
	detectedErrCh := make(chan error, 1)
	r.engine.startStreamErrorMonitor(attemptCtx, step, execution, stepExec, r.buffer, func(err error) {
		select {
		case detectedErrCh <- err:
		default:
		}
		cancelAttempt()
	})

	output, err := ag.Execute(attemptCtx, taskInput)
	cancelAttempt()

	select {
	case detectedErr := <-detectedErrCh:
		if err == nil || err == context.Canceled {
			err = detectedErr
		}
	default:
	}
	if err != nil {
		return nil, err
	}
	if !output.Success {
		return nil, apperr.Internalf("step execution failed: %s", output.Error).WithCode("workflow_step_execution_failed")
	}

	r.engine.saveStreamingCheckpoint(execution, step.ID, r.buffer, output.Result)
	r.buffer.MarkComplete()

	return &StepAttemptOutcome{
		Result: output.Result,
		CompletionOptions: StepCompletionOptions{
			EventType: "step_completed",
			Message:   fmt.Sprintf("Step %s completed successfully (streaming)", step.ID),
			EventData: map[string]interface{}{
				"step_id":      step.ID,
				"result":       output.Result,
				"total_tokens": r.buffer.TotalTokens(),
				"total_chunks": r.buffer.TotalChunks(),
			},
		},
	}, nil
}

func (r *streamingStepRunner) HandlePrepareError(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	err error,
) error {
	switch r.engine.getStreamingOnError(step) {
	case "fallback":
		return r.engine.executeStepStreamingFallback(ctx, step, execution, stepExec, r.buffer, err)
	default:
		if r.buffer != nil {
			r.buffer.MarkComplete()
		}
		return r.engine.failStep(stepExec, execution, err)
	}
}

func (r *streamingStepRunner) HandleExhausted(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	lastErr error,
) error {
	r.engine.restoreFromCheckpoint(execution, step.ID, r.buffer)
	switch r.engine.getStreamingOnError(step) {
	case "continue":
		return r.engine.completeStreamingStepWithPartialData(step, execution, stepExec, r.buffer, lastErr)
	case "fallback":
		return r.engine.executeStepStreamingFallback(ctx, step, execution, stepExec, r.buffer, lastErr)
	default:
		r.buffer.MarkComplete()
		return r.engine.failStep(stepExec, execution, lastErr)
	}
}
