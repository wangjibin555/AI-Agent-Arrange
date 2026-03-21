package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
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
					report(fmt.Errorf("buffer overflow detected for step %s", step.ID))
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
					report(fmt.Errorf("stream stalled for step %s", step.ID))
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
	if execution.Checkpoints == nil {
		execution.Checkpoints = make(map[string]*StreamCheckpoint)
	}

	output := explicitOutput
	if output == nil {
		output = e.collectPartialStreamResult(buffer)
	}

	execution.Checkpoints[stepID] = &StreamCheckpoint{
		StepID:      stepID,
		ChunkIndex:  buffer.TotalChunks() - 1,
		TotalChunks: buffer.TotalChunks(),
		TotalTokens: buffer.TotalTokens(),
		Output:      copyMap(output),
		Timestamp:   time.Now(),
	}
}

func (e *StreamingEngine) restoreFromCheckpoint(
	execution *WorkflowExecution,
	stepID string,
	buffer *StreamBuffer,
) bool {
	checkpoint := execution.Checkpoints[stepID]
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

	ag, err := e.agentRegistry.Get(step.AgentName)
	if err != nil {
		return fmt.Errorf("compensation agent not found: %s", step.AgentName)
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
		return fmt.Errorf("compensation failed: %s", output.Error)
	}

	e.publishEvent(execution.ID, "step_compensated", string(WorkflowStatusFailed),
		fmt.Sprintf("Step %s compensation executed", step.ID),
		map[string]interface{}{"step_id": step.ID, "result": output.Result})
	return nil
}

func (e *StreamingEngine) rollbackStreamingWorkflow(
	ctx context.Context,
	workflow *Workflow,
	execution *WorkflowExecution,
	completedSteps map[string]bool,
) {
	e.publishEvent(execution.ID, "rollback_started", string(WorkflowStatusFailed),
		"Starting workflow rollback", nil)

	for i := len(workflow.Steps) - 1; i >= 0; i-- {
		step := workflow.Steps[i]
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
	source, err := e.GetExecution(sourceExecutionID)
	if err != nil {
		return nil, err
	}

	execution := &WorkflowExecution{
		ID:             uuid.New().String(),
		WorkflowID:     workflow.ID,
		Status:         WorkflowStatusRunning,
		StepExecutions: make(map[string]*StepExecution),
		Context:        NewExecutionContext(variables),
		Checkpoints:    make(map[string]*StreamCheckpoint),
		ResumeState: &ExecutionResumeState{
			SourceExecutionID: sourceExecutionID,
			RestoredSteps:     make([]string, 0),
		},
		StartedAt: time.Now(),
	}

	for k, v := range source.Context.Variables {
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
	resumedDAG, err := NewDAG(resumedWorkflow.Steps)
	if err != nil {
		return nil, fmt.Errorf("invalid resumed workflow: %w", err)
	}

	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	if e.repository != nil {
		if err := e.repository.SaveExecution(execution); err != nil {
			return nil, fmt.Errorf("failed to save resumed execution: %w", err)
		}
	}

	e.publishEvent(execution.ID, "workflow_resumed", string(WorkflowStatusRunning),
		fmt.Sprintf("Workflow resumed from execution %s", sourceExecutionID),
		map[string]interface{}{"source_execution_id": sourceExecutionID})

	go e.executeWorkflowStreamingWithState(ctx, resumedWorkflow, resumedDAG, execution, make(map[string]bool), make(map[string]bool))
	return execution, nil
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
