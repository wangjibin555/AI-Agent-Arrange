package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/orchestrator"
)

// Engine executes workflows
type Engine struct {
	taskManager    *orchestrator.TaskManager
	agentRegistry  *agent.Registry
	repository     Repository
	executionCache map[string]*WorkflowExecution // executionID -> execution
	mu             sync.RWMutex
	eventPublisher EventPublisher
	defaultTimeout time.Duration
	maxConcurrency int // Max parallel steps per workflow
}

// EventPublisher publishes workflow events
type EventPublisher interface {
	PublishWorkflowEvent(executionID string, eventType string, status string, message string, data map[string]interface{})
}

// EngineConfig holds configuration for the workflow engine
type EngineConfig struct {
	TaskManager    *orchestrator.TaskManager
	AgentRegistry  *agent.Registry
	Repository     Repository
	EventPublisher EventPublisher
	DefaultTimeout time.Duration
	MaxConcurrency int // 0 = unlimited
}

// NewEngine creates a new workflow engine
func NewEngine(config EngineConfig) *Engine {
	if config.DefaultTimeout == 0 {
		config.DefaultTimeout = 30 * time.Minute
	}
	if config.MaxConcurrency == 0 {
		config.MaxConcurrency = 10 // Default to 10 parallel steps
	}

	return &Engine{
		taskManager:    config.TaskManager,
		agentRegistry:  config.AgentRegistry,
		repository:     config.Repository,
		eventPublisher: config.EventPublisher,
		executionCache: make(map[string]*WorkflowExecution),
		defaultTimeout: config.DefaultTimeout,
		maxConcurrency: config.MaxConcurrency,
	}
}

// Execute starts execution of a workflow
func (e *Engine) Execute(ctx context.Context, workflow *Workflow, variables map[string]interface{}) (*WorkflowExecution, error) {
	// Validate workflow
	dag, err := NewDAG(workflow.Steps)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}

	// Create execution
	execution := &WorkflowExecution{
		ID:             uuid.New().String(),
		WorkflowID:     workflow.ID,
		Status:         WorkflowStatusRunning,
		StepExecutions: make(map[string]*StepExecution),
		Context:        NewExecutionContext(variables),
		StartedAt:      time.Now(),
	}

	// Initialize workflow variables
	if workflow.Variables != nil {
		for k, v := range workflow.Variables {
			execution.Context.SetVariable(k, v)
		}
	}

	// Cache execution
	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	// Save to repository if available
	if e.repository != nil {
		if err := e.repository.SaveExecution(execution); err != nil {
			return nil, fmt.Errorf("failed to save execution: %w", err)
		}
	}

	// Publish start event
	e.publishEvent(execution.ID, "workflow_started", string(WorkflowStatusRunning), "Workflow execution started", nil)

	// Execute workflow in background
	go e.executeWorkflow(ctx, workflow, dag, execution)

	return execution, nil
}

// executeWorkflow executes the workflow steps according to DAG
func (e *Engine) executeWorkflow(ctx context.Context, workflow *Workflow, dag *DAG, execution *WorkflowExecution) {
	// Set timeout
	timeout := e.defaultTimeout
	if workflow.Timeout != nil {
		timeout = *workflow.Timeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Track completed steps
	completedSteps := make(map[string]bool)
	failedSteps := make(map[string]bool)
	mu := &sync.Mutex{}

	// Get execution order
	executionLevels, err := dag.GetExecutionOrder()
	if err != nil {
		e.finishExecution(execution, WorkflowStatusFailed, fmt.Sprintf("Failed to get execution order: %v", err))
		return
	}

	// Execute each level
	for levelIdx, level := range executionLevels {
		select {
		case <-execCtx.Done():
			e.finishExecution(execution, WorkflowStatusFailed, "Workflow execution timeout")
			return
		default:
		}

		e.publishEvent(execution.ID, "level_started", string(WorkflowStatusRunning),
			fmt.Sprintf("Starting execution level %d with %d steps", levelIdx, len(level)), nil)

		// Execute all steps in this level in parallel (with concurrency limit)
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, e.maxConcurrency)
		errors := make(chan error, len(level))

		for _, step := range level {
			// Check if step should be executed based on conditions
			shouldExecute, err := e.shouldExecuteStep(step, execution.Context, completedSteps, failedSteps)
			if err != nil {
				e.finishExecution(execution, WorkflowStatusFailed, fmt.Sprintf("Error evaluating step condition: %v", err))
				return
			}

			if !shouldExecute {
				// Mark as skipped
				mu.Lock()
				completedSteps[step.ID] = true
				mu.Unlock()
				continue
			}

			wg.Add(1)
			semaphore <- struct{}{} // Acquire semaphore

			go func(s *Step) {
				defer wg.Done()
				defer func() { <-semaphore }() // Release semaphore

				if err := e.executeStep(execCtx, s, execution); err != nil {
					errors <- err
					mu.Lock()
					failedSteps[s.ID] = true
					mu.Unlock()
				} else {
					mu.Lock()
					completedSteps[s.ID] = true
					mu.Unlock()
				}
			}(step)
		}

		wg.Wait()
		close(errors)

		// Check for errors in this level
		if len(errors) > 0 {
			errMsg := "Step execution failed"
			for err := range errors {
				errMsg = fmt.Sprintf("%s; %v", errMsg, err)
			}

			// Check failure policy
			if workflow.OnFailure != nil && workflow.OnFailure.Rollback {
				e.rollbackWorkflow(execCtx, execution, completedSteps)
			}

			e.finishExecution(execution, WorkflowStatusFailed, errMsg)
			return
		}
	}

	// All steps completed successfully
	e.finishExecution(execution, WorkflowStatusCompleted, "Workflow completed successfully")
}

// shouldExecuteStep determines if a step should be executed based on conditions
func (e *Engine) shouldExecuteStep(step *Step, ctx *ExecutionContext, completed, failed map[string]bool) (bool, error) {
	// Check if all dependencies are met
	for _, depID := range step.DependsOn {
		if !completed[depID] {
			return false, nil
		}
		// If a dependency failed and we don't continue on error, skip
		if failed[depID] && (step.ContinueOn == nil || !step.ContinueOn.OnError) {
			return false, nil
		}
	}

	// Evaluate condition if present
	if step.Condition != nil {
		return e.evaluateCondition(step.Condition, ctx, completed)
	}

	return true, nil
}

// evaluateCondition evaluates a step condition
func (e *Engine) evaluateCondition(cond *Condition, ctx *ExecutionContext, completed map[string]bool) (bool, error) {
	engine := NewTemplateEngine(ctx)

	switch cond.Type {
	case ConditionTypeAlways:
		return true, nil

	case ConditionTypeStatus:
		// Check if a step has a specific status
		// Format: step_id in completed map
		return completed[cond.Status], nil

	case ConditionTypeExpression:
		// Evaluate expression
		return engine.EvaluateCondition(cond.Expression)

	default:
		return false, fmt.Errorf("unknown condition type: %s", cond.Type)
	}
}

// executeStep executes a single workflow step
func (e *Engine) executeStep(ctx context.Context, step *Step, execution *WorkflowExecution) error {
	stepExec := &StepExecution{
		StepID:    step.ID,
		Status:    WorkflowStatusRunning,
		StartedAt: time.Now(),
	}

	e.mu.Lock()
	execution.StepExecutions[step.ID] = stepExec
	execution.CurrentStep = step.ID
	e.mu.Unlock()

	e.publishEvent(execution.ID, "step_started", string(WorkflowStatusRunning),
		fmt.Sprintf("Step %s started", step.ID), map[string]interface{}{"step_id": step.ID})

	// Render step parameters with template engine
	engine := NewTemplateEngine(execution.Context)
	renderedParams, err := engine.RenderParameters(step.Parameters)
	if err != nil {
		return e.failStep(stepExec, execution, fmt.Errorf("failed to render parameters: %w", err))
	}

	// Execute step with retries
	maxRetries := step.Retries
	if maxRetries == 0 {
		maxRetries = 1 // At least one attempt
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			stepExec.RetryCount++
			e.publishEvent(execution.ID, "step_retry", string(WorkflowStatusRunning),
				fmt.Sprintf("Retrying step %s (attempt %d/%d)", step.ID, attempt+1, maxRetries),
				map[string]interface{}{"step_id": step.ID, "retry_count": attempt})
		}

		// Create task for this step
		taskID := uuid.New().String()
		stepExec.TaskID = taskID

		taskInput := &agent.TaskInput{
			TaskID:     taskID,
			Action:     step.Action,
			Parameters: renderedParams,
			Context:    make(map[string]interface{}),
		}

		// Get agent
		ag, err := e.agentRegistry.Get(step.AgentName)
		if err != nil {
			lastErr = fmt.Errorf("agent not found: %s", step.AgentName)
			continue
		}

		// Set timeout
		stepCtx := ctx
		if step.Timeout != nil {
			var cancel context.CancelFunc
			stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
			defer cancel()
		}

		// 调用Agent执行对应Task
		output, err := ag.Execute(stepCtx, taskInput)
		if err != nil {
			lastErr = err
			continue
		}

		//执行失败，进行重试
		if !output.Success {
			lastErr = fmt.Errorf("step execution failed: %s", output.Error)
			continue
		}

		// Success!
		stepExec.Result = output.Result
		stepExec.Status = WorkflowStatusCompleted
		now := time.Now()
		stepExec.CompletedAt = &now

		// Store result in context
		execution.Context.SetStepOutput(step.ID, output.Result)

		// Use output alias if specified
		if step.OutputAlias != "" {
			execution.Context.SetVariable(step.OutputAlias, output.Result)
		}

		e.publishEvent(execution.ID, "step_completed", string(WorkflowStatusCompleted),
			fmt.Sprintf("Step %s completed successfully", step.ID),
			map[string]interface{}{"step_id": step.ID, "result": output.Result})

		//执行成功返回nil，跳出重试
		return nil
	}

	// All retries failed
	return e.failStep(stepExec, execution, lastErr)
}

// failStep marks a step as failed
func (e *Engine) failStep(stepExec *StepExecution, execution *WorkflowExecution, err error) error {
	stepExec.Status = WorkflowStatusFailed
	stepExec.Error = err.Error()
	now := time.Now()
	stepExec.CompletedAt = &now

	e.publishEvent(execution.ID, "step_failed", string(WorkflowStatusFailed),
		fmt.Sprintf("Step %s failed: %v", stepExec.StepID, err),
		map[string]interface{}{"step_id": stepExec.StepID, "error": err.Error()})

	return err
}

// rollbackWorkflow attempts to rollback completed steps
func (e *Engine) rollbackWorkflow(ctx context.Context, execution *WorkflowExecution, completedSteps map[string]bool) {
	e.publishEvent(execution.ID, "rollback_started", string(WorkflowStatusFailed),
		"Starting workflow rollback", nil)

	// TODO: Implement rollback logic
	// This would require steps to define rollback actions

	e.publishEvent(execution.ID, "rollback_completed", string(WorkflowStatusFailed),
		"Workflow rollback completed", nil)
}

// finishExecution marks execution as finished
func (e *Engine) finishExecution(execution *WorkflowExecution, status WorkflowStatus, message string) {
	e.mu.Lock()
	execution.Status = status
	if status == WorkflowStatusFailed {
		execution.Error = message
	}
	now := time.Now()
	execution.CompletedAt = &now
	e.mu.Unlock()

	// Save to repository
	if e.repository != nil {
		_ = e.repository.SaveExecution(execution)
	}

	e.publishEvent(execution.ID, "workflow_completed", string(status), message, nil)
}

// publishEvent publishes a workflow event
func (e *Engine) publishEvent(executionID, eventType, status, message string, data map[string]interface{}) {
	if e.eventPublisher != nil {
		e.eventPublisher.PublishWorkflowEvent(executionID, eventType, status, message, data)
	}
}

// GetExecution retrieves a workflow execution by ID
func (e *Engine) GetExecution(executionID string) (*WorkflowExecution, error) {
	e.mu.RLock()
	execution, exists := e.executionCache[executionID]
	e.mu.RUnlock()

	if exists {
		return execution, nil
	}

	// Try to load from repository
	if e.repository != nil {
		return e.repository.GetExecution(executionID)
	}

	return nil, fmt.Errorf("execution not found: %s", executionID)
}

// CancelExecution cancels a running workflow execution
func (e *Engine) CancelExecution(executionID string) error {
	execution, err := e.GetExecution(executionID)
	if err != nil {
		return err
	}

	if execution.Status != WorkflowStatusRunning {
		return fmt.Errorf("execution is not running")
	}

	e.finishExecution(execution, WorkflowStatusCancelled, "Execution cancelled by user")
	return nil
}
