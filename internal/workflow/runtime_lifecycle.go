package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// StepCompletionOptions customizes completion side effects for special step modes.
type StepStartOptions struct {
	EventType string
	Message   string
	EventData map[string]interface{}
}

// StepCompletionOptions customizes completion side effects for special step modes.
type StepCompletionOptions struct {
	EventType string
	Message   string
	EventData map[string]interface{}
}

func (o StepCompletionOptions) isZero() bool {
	return o.EventType == "" && o.Message == "" && o.EventData == nil
}

func (s *workflowExecutionState) CompleteStepWithOptions(
	execution *WorkflowExecution,
	step *Step,
	stepExec *StepExecution,
	result map[string]interface{},
	opts StepCompletionOptions,
) error {
	if result == nil {
		result = make(map[string]interface{})
	}
	if err := validateStepOutputSchema(step, result); err != nil {
		return s.FailStep(execution, stepExec, err)
	}

	stepExec.Result = result
	stepExec.Status = WorkflowStatusCompleted
	now := time.Now()
	stepExec.CompletedAt = &now

	execution.Context.SetStepOutput(step.ID, result)
	if step.OutputAlias != "" {
		execution.Context.SetVariable(step.OutputAlias, result)
	}
	s.engine.notifyExecutionContextChange(execution)

	if err := s.ApplyRoute(execution, step, stepExec); err != nil {
		return s.FailStep(execution, stepExec, err)
	}

	eventType := opts.EventType
	if eventType == "" {
		eventType = "step_completed"
	}
	message := opts.Message
	if message == "" {
		message = fmt.Sprintf("Step %s completed successfully", step.ID)
	}
	eventData := opts.EventData
	if eventData == nil {
		eventData = map[string]interface{}{
			"step_id": step.ID,
			"result":  result,
		}
	}

	s.engine.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:            eventType,
		Status:          string(WorkflowStatusCompleted),
		Message:         message,
		Data:            eventData,
		Execution:       execution,
		Step:            step,
		StepExecution:   stepExec,
		PersistSnapshot: true,
	})

	return nil
}

func validateStepOutputSchema(step *Step, result map[string]interface{}) error {
	if step == nil || step.OutputSchema == nil || len(step.OutputSchema.Required) == 0 {
		return nil
	}
	for _, field := range step.OutputSchema.Required {
		if _, ok := result[field]; !ok {
			return apperr.InvalidArgumentf("missing required output field: %s", field).WithCode("workflow_step_output_schema_invalid")
		}
	}
	return nil
}

func newStreamingCheckpoint(stepID string, buffer *StreamBuffer, output map[string]interface{}) *StreamCheckpoint {
	if buffer == nil {
		return nil
	}
	return &StreamCheckpoint{
		StepID:      stepID,
		ChunkIndex:  buffer.TotalChunks() - 1,
		TotalChunks: buffer.TotalChunks(),
		TotalTokens: buffer.TotalTokens(),
		Output:      copyMap(output),
		Timestamp:   time.Now(),
	}
}
