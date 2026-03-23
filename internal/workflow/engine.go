package workflow

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
)

// Engine executes workflows
type Engine struct {
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
		agentRegistry:  config.AgentRegistry,
		repository:     config.Repository,
		eventPublisher: config.EventPublisher,
		executionCache: make(map[string]*WorkflowExecution),
		defaultTimeout: config.DefaultTimeout,
		maxConcurrency: config.MaxConcurrency,
	}
}

type StepDecision string

const (
	StepDecisionWait    StepDecision = "wait"
	StepDecisionExecute StepDecision = "execute"
	StepDecisionSkip    StepDecision = "skip"
)

// Execute starts execution of a workflow
func (e *Engine) Execute(ctx context.Context, workflow *Workflow, variables map[string]interface{}) (*WorkflowExecution, error) {
	// Validate workflow
	dag, err := NewDAG(workflow.Steps)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}

	// Create execution
	execution := &WorkflowExecution{
		ID:              uuid.New().String(),
		WorkflowID:      workflow.ID,
		Status:          WorkflowStatusRunning,
		StepExecutions:  make(map[string]*StepExecution),
		Context:         NewExecutionContext(variables),
		RouteSelections: make(map[string]string),
		StartedAt:       time.Now(),
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
			decision, reason, err := e.evaluateStepDecision(workflow, step, execution, completedSteps, failedSteps)
			if err != nil {
				e.finishExecution(execution, WorkflowStatusFailed, fmt.Sprintf("Error evaluating step condition: %v", err))
				return
			}

			if decision == StepDecisionWait {
				e.finishExecution(execution, WorkflowStatusFailed,
					fmt.Sprintf("Step %s remained waiting in topological execution", step.ID))
				return
			}

			if decision == StepDecisionSkip {
				mu.Lock()
				completedSteps[step.ID] = true
				mu.Unlock()
				e.skipStep(step, execution, reason)
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

// evaluateStepDecision determines if a step should execute, wait, or be skipped.
func (e *Engine) evaluateStepDecision(
	workflow *Workflow,
	step *Step,
	execution *WorkflowExecution,
	completed, failed map[string]bool,
) (StepDecision, string, error) {
	// Check if all dependencies are met
	for _, depID := range step.DependsOn {
		//如果失败，并且没有设置继续执行不管当前节点或者忽略错误，则返回跳过
		if failed[depID] && (step.ContinueOn == nil || !step.ContinueOn.OnError) {
			return StepDecisionSkip, fmt.Sprintf("dependency_failed:%s", depID), nil
		}
		//这个是正常执行，只是依赖不够，需要等待
		if !completed[depID] && !failed[depID] {
			return StepDecisionWait, "", nil
		}
	}

	routeAllowed, routeReason, err := e.routeAllowsStep(workflow, step, execution)
	if err != nil {
		return StepDecisionWait, "", err
	}
	if !routeAllowed {
		return StepDecisionSkip, routeReason, nil
	}

	// 如果当前step有配置condition则评估当前情况进行执行
	if step.Condition != nil {
		shouldExecute, err := e.evaluateCondition(step.Condition, execution.Context, completed)
		if err != nil {
			return StepDecisionWait, "", err
		}
		if !shouldExecute {
			return StepDecisionSkip, "condition_false", nil
		}
	}

	return StepDecisionExecute, "", nil
}

// evaluateCondition 评估步骤条件
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

func (e *Engine) routeAllowsStep(workflow *Workflow, step *Step, execution *WorkflowExecution) (bool, string, error) {
	for _, depID := range step.DependsOn {
		//找到依赖的step节点
		routerStep := findStepByID(workflow.Steps, depID)
		if routerStep == nil || routerStep.Route == nil {
			continue
		}

		e.mu.RLock()
		// 获取路由选择
		selectedRoute := execution.RouteSelections[depID]
		e.mu.RUnlock()

		// 检查这个Step是否受到route控制，同时它是否可以被路由所选中
		// 传入参数是
		targeted, allowed := routeTargetsStep(routerStep.Route, step.ID, selectedRoute)
		if !targeted {
			continue
		}
		if !allowed {
			if selectedRoute == "" {
				return false, "", fmt.Errorf("route step %s completed without selection", depID)
			}
			return false, fmt.Sprintf("route_filtered:%s=%s", depID, selectedRoute), nil
		}
	}

	return true, "", nil
}

func routeTargetsStep(route *RouteConfig, stepID, selected string) (bool, bool) {
	targeted := false
	//找到激活的下游stepId
	for _, targets := range route.Cases {
		if slices.Contains(targets, stepID) {
			targeted = true
			break
		}
	}
	if !targeted && slices.Contains(route.Default, stepID) {
		targeted = true
	}
	//返回当前step与这个route是否有关，无法拿当前路由进行限制，并且通知当前step是否是router控制下的分支节点，如果本次没有被选中，则返回false跳过
	if !targeted {
		return false, false
	}

	if targets, ok := route.Cases[selected]; ok && slices.Contains(targets, stepID) {
		return true, true
	}
	if _, ok := route.Cases[selected]; !ok && slices.Contains(route.Default, stepID) {
		return true, true
	}

	return true, false
}

func findStepByID(steps []*Step, stepID string) *Step {
	for _, step := range steps {
		if step.ID == stepID {
			return step
		}
	}
	return nil
}

func (e *Engine) newExecutionContextReader(execution *WorkflowExecution) agent.ContextReader {
	return &executionContextReader{
		execution: execution,
		mu:        &e.mu,
		cond:      e.getExecutionNotifier(execution),
	}
}

func (e *Engine) getExecutionNotifier(execution *WorkflowExecution) *sync.Cond {
	e.mu.Lock()
	defer e.mu.Unlock()

	if execution.notifier == nil {
		execution.notifier = sync.NewCond(&e.mu)
	}
	return execution.notifier
}

func (e *Engine) notifyExecutionContextChange(execution *WorkflowExecution) {
	e.mu.RLock()
	notifier := execution.notifier
	e.mu.RUnlock()
	if notifier != nil {
		notifier.Broadcast()
	}
}

// executeStep executes a single workflow step
func (e *Engine) executeStep(ctx context.Context, step *Step, execution *WorkflowExecution) error {
	stepExec := &StepExecution{
		StepID:    step.ID,
		Status:    WorkflowStatusRunning,
		StartedAt: time.Now(),
		Metadata:  make(map[string]interface{}),
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
			TaskID:        taskID,
			Action:        step.Action,
			Parameters:    renderedParams,
			Context:       make(map[string]interface{}),
			ContextReader: e.newExecutionContextReader(execution),
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
		e.notifyExecutionContextChange(execution)

		if step.Route != nil {
			selectedRoute, targets, err := e.applyRouteSelection(step, execution)
			if err != nil {
				return e.failStep(stepExec, execution, fmt.Errorf("failed to apply route selection: %w", err))
			}
			stepExec.Metadata["route"] = selectedRoute
			stepExec.Metadata["route_targets"] = targets
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

func (e *Engine) applyRouteSelection(step *Step, execution *WorkflowExecution) (string, []string, error) {
	templateEngine := NewTemplateEngine(execution.Context)
	rendered, err := templateEngine.Render(step.Route.Expression)
	if err != nil {
		return "", nil, err
	}

	selected := rendered
	targets, ok := step.Route.Cases[selected]
	if !ok {
		targets = step.Route.Default
	}
	if execution.RouteSelections == nil {
		execution.RouteSelections = make(map[string]string)
	}
	e.mu.Lock()
	execution.RouteSelections[step.ID] = selected
	e.mu.Unlock()

	e.publishEvent(execution.ID, "step_routed", string(WorkflowStatusCompleted),
		fmt.Sprintf("Step %s selected route %s", step.ID, selected),
		map[string]interface{}{
			"step_id": step.ID,
			"route":   selected,
			"targets": targets,
		})

	return selected, targets, nil
}

func (e *Engine) skipStep(step *Step, execution *WorkflowExecution, reason string) {
	now := time.Now()
	stepExec := &StepExecution{
		StepID:      step.ID,
		Status:      WorkflowStatusSkipped,
		StartedAt:   now,
		CompletedAt: &now,
		Metadata: map[string]interface{}{
			"skip_reason": reason,
		},
	}

	e.mu.Lock()
	execution.StepExecutions[step.ID] = stepExec
	e.mu.Unlock()

	e.publishEvent(execution.ID, "step_skipped", string(WorkflowStatusSkipped),
		fmt.Sprintf("Step %s skipped", step.ID),
		map[string]interface{}{
			"step_id": step.ID,
			"reason":  reason,
		})
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
