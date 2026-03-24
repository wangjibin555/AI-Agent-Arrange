package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

type StreamError struct {
	StepID    string
	ErrorType string
	Message   string
	Timestamp time.Time
}

func (e *StreamingEngine) startStreamErrorMonitor(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	buffer *StreamBuffer,
	report func(error),
) {
	if step.Streaming == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if buffer.IsCompleted() {
					return
				}
				if step.Streaming.BufferSize > 0 && buffer.Size() > step.Streaming.BufferSize {
					report(apperr.Conflict(fmt.Sprintf("buffer overflow detected for step %s", step.ID)).WithCode("workflow_stream_buffer_overflow"))
					return
				}

				timeout := step.Streaming.StreamTimeout
				if timeout <= 0 {
					continue
				}

				// Monitor the current step's output buffer. If it stops producing chunks
				// for too long while the step is still running, treat it as a stalled stream.
				if e.isStepStillRunning(execution, stepExec.StepID) &&
					time.Since(stepExec.StartedAt) > time.Duration(timeout)*time.Second &&
					buffer.TimeSinceLastChunk() > time.Duration(timeout)*time.Second {
					report(apperr.Conflict(fmt.Sprintf("stream stalled for step %s", step.ID)).WithCode("workflow_stream_stalled"))
					return
				}
			}
		}
	}()
}

func (e *StreamingEngine) isStepStillRunning(execution *WorkflowExecution, stepID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stepExec := execution.StepExecutions[stepID]
	return stepExec != nil && stepExec.Status == WorkflowStatusRunning
}

func (e *StreamingEngine) isCheckpointEnabled(step *Step) bool {
	if step.Streaming == nil {
		return false
	}
	if step.Streaming.CheckpointEnabled == nil {
		return true
	}
	return *step.Streaming.CheckpointEnabled
}

func (e *StreamingEngine) saveStreamingCheckpoint(
	execution *WorkflowExecution,
	stepID string,
	buffer *StreamBuffer,
	explicitOutput map[string]interface{},
) {
	output := explicitOutput
	if output == nil {
		output = e.collectPartialStreamResult(buffer)
	}

	_ = e.executionState().SaveCheckpoint(execution, stepID, newStreamingCheckpoint(stepID, buffer, output))
}

func (e *StreamingEngine) restoreFromCheckpoint(
	execution *WorkflowExecution,
	stepID string,
	buffer *StreamBuffer,
) bool {
	e.mu.RLock()
	checkpoint := execution.Checkpoints[stepID]
	e.mu.RUnlock()
	if checkpoint == nil {
		return false
	}

	if buffer != nil {
		buffer.TruncateAfter(checkpoint.ChunkIndex)
	}
	if checkpoint.Output != nil {
		execution.Context.SetStepOutput(stepID, copyMap(checkpoint.Output))
	} else {
		execution.Context.RemoveStepOutput(stepID)
	}

	return true
}

func (e *StreamingEngine) rollbackWorkflow(ctx context.Context, execution *WorkflowExecution, completedSteps map[string]bool) {
	e.publishEvent(execution.ID, "rollback_started", string(WorkflowStatusFailed),
		"Starting workflow rollback", nil)

	for stepID := range completedSteps {
		buffer := e.getBuffer(execution.ID, stepID)
		if !e.restoreFromCheckpoint(execution, stepID, buffer) {
			execution.Context.RemoveStepOutput(stepID)
		}
		execution.Context.DeleteVariable(stepID + "_partial")
	}

	e.publishEvent(execution.ID, "rollback_completed", string(WorkflowStatusFailed),
		"Workflow rollback completed", nil)
}

func (e *StreamingEngine) executeCompensation(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
) error {
	if step.CompensationAction == "" {
		return nil
	}

	ag, err := e.resolveStepAgent(step)
	if err != nil {
		return apperr.NotFound("compensation agent resolution failed").WithCode("workflow_compensation_agent_not_found").WithCause(err)
	}

	params := copyMap(step.CompensationParams)
	if params == nil {
		params = make(map[string]interface{})
	}
	if output, ok := execution.Context.GetStepOutput(step.ID); ok {
		params["original_output"] = output
	}

	taskInput := &agent.TaskInput{
		TaskID:     uuid.New().String(),
		Action:     step.CompensationAction,
		Parameters: params,
		Context:    make(map[string]interface{}),
	}

	output, err := ag.Execute(ctx, taskInput)
	if err != nil {
		return err
	}
	if !output.Success {
		return apperr.Internalf("compensation failed: %s", output.Error).WithCode("workflow_compensation_failed")
	}

	e.publishEvent(execution.ID, "step_compensated", string(WorkflowStatusFailed),
		fmt.Sprintf("Step %s compensation executed", step.ID),
		map[string]interface{}{"step_id": step.ID, "result": output.Result})
	return nil
}

func (e *StreamingEngine) rollbackStreamingWorkflow(
	ctx context.Context,
	workflow *CompiledWorkflow,
	execution *WorkflowExecution,
	completedSteps map[string]bool,
) {
	e.publishEvent(execution.ID, "rollback_started", string(WorkflowStatusFailed),
		"Starting workflow rollback", nil)

	for i := len(workflow.Steps) - 1; i >= 0; i-- {
		step := workflow.Steps[i].Runtime
		if !completedSteps[step.ID] {
			continue
		}

		if err := e.executeCompensation(ctx, step, execution); err != nil {
			e.publishEvent(execution.ID, "step_compensation_failed", string(WorkflowStatusFailed),
				fmt.Sprintf("Step %s compensation failed", step.ID),
				map[string]interface{}{"step_id": step.ID, "error": err.Error()})
		}

		buffer := e.getBuffer(execution.ID, step.ID)
		if buffer != nil {
			buffer.TruncateAfter(-1)
		}
		execution.Context.RemoveStepOutput(step.ID)
		if step.OutputAlias != "" {
			execution.Context.DeleteVariable(step.OutputAlias)
		}
		execution.Context.DeleteVariable(step.ID + "_partial")
	}

	e.publishEvent(execution.ID, "rollback_completed", string(WorkflowStatusFailed),
		"Workflow rollback completed", nil)
}

func (e *StreamingEngine) ResumeExecution(
	ctx context.Context,
	workflow *Workflow,
	sourceExecutionID string,
	variables map[string]interface{},
) (*WorkflowExecution, error) {
	source, err := e.GetExecution(ctx, sourceExecutionID)
	if err != nil {
		return nil, err
	}

	execution := &WorkflowExecution{
		ID:             uuid.New().String(),
		WorkflowID:     workflow.ID,
		Status:         WorkflowStatusRunning,
		RecoveryStatus: RecoveryStatusResumed,
		StepExecutions: make(map[string]*StepExecution),
		Context:        NewExecutionContext(variables),
		Checkpoints:    make(map[string]*StreamCheckpoint),
		ResumeState: &ExecutionResumeState{
			SourceExecutionID: sourceExecutionID,
			RestoredSteps:     make([]string, 0),
		},
		StartedAt: time.Now(),
	}

	for k, v := range source.Context.SnapshotVariables() {
		execution.Context.SetVariable(k, v)
	}
	for k, v := range variables {
		execution.Context.SetVariable(k, v)
	}
	for k, v := range workflow.Variables {
		execution.Context.SetVariable(k, v)
	}

	restoredSet := make(map[string]bool)
	for stepID, checkpoint := range source.Checkpoints {
		if checkpoint == nil || checkpoint.Output == nil {
			continue
		}
		execution.Context.SetStepOutput(stepID, copyMap(checkpoint.Output))
		execution.Checkpoints[stepID] = &StreamCheckpoint{
			StepID:      checkpoint.StepID,
			ChunkIndex:  checkpoint.ChunkIndex,
			TotalChunks: checkpoint.TotalChunks,
			TotalTokens: checkpoint.TotalTokens,
			Output:      copyMap(checkpoint.Output),
			Timestamp:   checkpoint.Timestamp,
		}
		execution.StepExecutions[stepID] = &StepExecution{
			StepID:    stepID,
			Status:    WorkflowStatusCompleted,
			Result:    copyMap(checkpoint.Output),
			StartedAt: checkpoint.Timestamp,
		}
		restoredSet[stepID] = true
		execution.ResumeState.RestoredSteps = append(execution.ResumeState.RestoredSteps, stepID)
	}

	resumedWorkflow := cloneWorkflowForResume(workflow, restoredSet)
	compiledWorkflow, err := CompileWorkflow(resumedWorkflow)
	if err != nil {
		return nil, fmt.Errorf("invalid resumed workflow: %w", err)
	}

	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	e.emitRuntimeEvent(ctx, RuntimeEvent{
		Type:            "workflow_resumed",
		Status:          string(WorkflowStatusRunning),
		Message:         fmt.Sprintf("Workflow resumed from execution %s", sourceExecutionID),
		Data:            map[string]interface{}{"source_execution_id": sourceExecutionID},
		Execution:       execution,
		PersistSnapshot: true,
		RecoveryStatus:  string(RecoveryStatusResumed),
	})

	go e.executeWorkflowStreamingWithState(ctx, compiledWorkflow, execution, make(map[string]bool), make(map[string]bool))
	return execution, nil
}

// RecoverRunningExecutions attempts startup recovery for persisted running executions.
// Streaming executions with checkpoints are resumed into new execution IDs.
// Non-streaming or non-checkpointed executions are marked as interrupted.
func (e *StreamingEngine) RecoverRunningExecutions(ctx context.Context) (int, int, error) {
	if e.repository == nil {
		return 0, 0, nil
	}

	runningExecutions, err := e.repository.GetRunningExecutions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to load running executions: %w", err)
	}

	resumedCount := 0
	interruptedCount := 0

	for _, execution := range runningExecutions {
		if execution == nil {
			continue
		}

		workflowDef, err := e.repository.GetWorkflow(ctx, execution.WorkflowID)
		if err != nil {
			if markErr := e.markExecutionInterrupted(ctx, execution, "workflow interrupted by server restart: definition unavailable"); markErr != nil {
				return resumedCount, interruptedCount, fmt.Errorf("failed to mark interrupted execution %s: %w", execution.ID, markErr)
			}
			interruptedCount++
			continue
		}

		if e.hasStreamingSteps(workflowDef) && len(execution.Checkpoints) > 0 {
			if resumedExecution, err := e.ResumeExecution(ctx, workflowDef, execution.ID, nil); err == nil {
				if markErr := e.markExecutionSuperseded(ctx, execution, resumedExecution.ID, "workflow superseded by resumed execution after server restart"); markErr != nil {
					return resumedCount, interruptedCount, fmt.Errorf("failed to mark resumed source execution %s: %w", execution.ID, markErr)
				}
				if e.metrics != nil {
					e.metrics.ObserveRecovery(execution.WorkflowID, string(RecoveryStatusResumed))
				}
				resumedCount++
				continue
			}
		}

		if err := e.markExecutionInterrupted(ctx, execution, "workflow interrupted by server restart"); err != nil {
			return resumedCount, interruptedCount, fmt.Errorf("failed to mark interrupted execution %s: %w", execution.ID, err)
		}
		interruptedCount++
	}

	return resumedCount, interruptedCount, nil
}

func (e *StreamingEngine) markExecutionInterrupted(ctx context.Context, execution *WorkflowExecution, message string) error {
	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	execution.Status = WorkflowStatusFailed
	execution.RecoveryStatus = RecoveryStatusInterrupted
	execution.Error = message
	now := time.Now()
	execution.CompletedAt = &now

	e.emitRuntimeEvent(ctx, RuntimeEvent{
		Type:            "workflow_interrupted",
		Status:          string(execution.Status),
		Message:         message,
		Execution:       execution,
		PersistSnapshot: true,
		RecoveryStatus:  string(execution.RecoveryStatus),
	})
	return nil
}

func (e *StreamingEngine) markExecutionSuperseded(ctx context.Context, execution *WorkflowExecution, resumedExecutionID, message string) error {
	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	execution.Status = WorkflowStatusFailed
	execution.RecoveryStatus = RecoveryStatusSuperseded
	execution.SupersededByExecutionID = resumedExecutionID
	execution.Error = message
	now := time.Now()
	execution.CompletedAt = &now

	e.emitRuntimeEvent(ctx, RuntimeEvent{
		Type:                    "workflow_superseded",
		Status:                  string(execution.Status),
		Message:                 message,
		Execution:               execution,
		PersistSnapshot:         true,
		RecoveryStatus:          string(execution.RecoveryStatus),
		SupersededByExecutionID: execution.SupersededByExecutionID,
	})
	return nil
}

func cloneWorkflowForResume(workflow *Workflow, restored map[string]bool) *Workflow {
	cloned := &Workflow{
		ID:          workflow.ID,
		Name:        workflow.Name,
		Description: workflow.Description,
		Version:     workflow.Version,
		Variables:   copyMap(workflow.Variables),
		OnFailure:   workflow.OnFailure,
		Timeout:     workflow.Timeout,
		CreatedAt:   workflow.CreatedAt,
		UpdatedAt:   workflow.UpdatedAt,
		Steps:       make([]*Step, 0, len(workflow.Steps)),
	}

	for _, step := range workflow.Steps {
		if restored[step.ID] {
			continue
		}

		stepCopy := *step
		filteredDeps := make([]string, 0, len(step.DependsOn))
		for _, depID := range step.DependsOn {
			if restored[depID] {
				continue
			}
			filteredDeps = append(filteredDeps, depID)
		}
		stepCopy.DependsOn = filteredDeps
		cloned.Steps = append(cloned.Steps, &stepCopy)
	}

	return cloned
}
