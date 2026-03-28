package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// StepStartOptions 自定义步骤开始时对外发送的事件内容。
type StepStartOptions struct {
	EventType string
	Message   string
	EventData map[string]interface{}
}

// StepCompletionOptions 自定义步骤完成时的事件和附加副作用。
// 特殊步骤模式可以通过它覆盖默认的 “step_completed” 行为。
type StepCompletionOptions struct {
	EventType string
	Message   string
	EventData map[string]interface{}
}

func (o StepCompletionOptions) isZero() bool {
	return o.EventType == "" && o.Message == "" && o.EventData == nil
}

// CompleteStepWithOptions 在步骤成功后统一落状态、写上下文、应用路由并发布事件。
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

	// 步骤结果会写回共享上下文，供下游模板渲染和条件判断使用。
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

// validateStepOutputSchema 校验步骤输出是否满足声明的弱 schema。
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

// newStreamingCheckpoint 根据当前流式缓冲区快照创建 checkpoint。
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
