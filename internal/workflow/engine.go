package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// Engine executes workflows
type Engine struct {
	agentRegistry  *agent.Registry
	repository     Repository
	executionCache map[string]*WorkflowExecution // executionID -> execution
	executionStops map[string]context.CancelFunc // executionID -> cancel func
	mu             sync.RWMutex
	eventPublisher EventPublisher
	defaultTimeout time.Duration
	maxConcurrency int // Max parallel steps per workflow
	metrics        *monitor.WorkflowMetrics
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
		executionStops: make(map[string]context.CancelFunc),
		defaultTimeout: config.DefaultTimeout,
		maxConcurrency: config.MaxConcurrency,
	}
}

func (e *Engine) SetMetrics(metrics *monitor.WorkflowMetrics) {
	e.metrics = metrics
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
		RecoveryStatus:  RecoveryStatusNone,
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

	// Workflow execution is asynchronous and must outlive the HTTP request context.
	// Keep context values, but detach from request cancellation.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	e.registerExecutionStop(execution.ID, cancel)

	// Save to repository if available
	if err := e.persistWorkflowDefinition(ctx, workflow); err != nil {
		return nil, fmt.Errorf("failed to save workflow definition: %w", err)
	}
	if err := e.persistExecutionSnapshot(ctx, execution); err != nil {
		return nil, fmt.Errorf("failed to save execution: %w", err)
	}

	// Publish start event
	e.observeWorkflowStarted(workflow)
	e.publishEvent(execution.ID, "workflow_started", string(WorkflowStatusRunning), "Workflow execution started", nil)

	// Execute workflow in background
	go e.executeWorkflow(runCtx, workflow, dag, execution)

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
			if execCtx.Err() == context.Canceled {
				e.finishExecution(execution, WorkflowStatusCancelled, "Execution cancelled by user")
			} else {
				e.finishExecution(execution, WorkflowStatusFailed, "Workflow execution timeout")
			}
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
		if execCtx.Err() == context.Canceled {
			e.finishExecution(execution, WorkflowStatusCancelled, "Execution cancelled by user")
			return
		}

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
		return false, apperr.InvalidArgumentf("unknown condition type: %s", cond.Type).WithCode("workflow_condition_type_invalid")
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
				return false, "", apperr.Internalf("route step %s completed without selection", depID).WithCode("workflow_route_selection_missing")
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
		Metadata: map[string]interface{}{
			"step_type": workflowStepType(step),
		},
	}

	e.mu.Lock()
	execution.StepExecutions[step.ID] = stepExec
	execution.CurrentStep = step.ID
	e.mu.Unlock()
	_ = e.persistExecutionSnapshot(context.Background(), execution)

	e.publishEvent(execution.ID, "step_started", string(WorkflowStatusRunning),
		fmt.Sprintf("Step %s started", step.ID), map[string]interface{}{"step_id": step.ID})
	e.observeStepStarted(execution.WorkflowID, step, stepExec)

	if step.Foreach != nil {
		return e.executeForeachStep(ctx, step, execution, stepExec)
	}

	// Render step parameters with template engine
	engine := NewTemplateEngine(execution.Context)
	renderedParams, err := engine.RenderParameters(step.Parameters)
	if err != nil {
		return e.failStep(stepExec, execution, apperr.InvalidArgument("failed to render parameters").WithCode("workflow_step_params_render_failed").WithCause(err))
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
			lastErr = apperr.NotFoundf("agent not found: %s", step.AgentName).WithCode("workflow_step_agent_not_found")
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
			lastErr = apperr.Internalf("step execution failed: %s", output.Error).WithCode("workflow_step_execution_failed")
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
				return e.failStep(stepExec, execution, apperr.Internal("failed to apply route selection").WithCode("workflow_route_apply_failed").WithCause(err))
			}
			stepExec.Metadata["route"] = selectedRoute
			stepExec.Metadata["route_targets"] = targets
		}
		_ = e.persistExecutionSnapshot(ctx, execution)

		e.publishEvent(execution.ID, "step_completed", string(WorkflowStatusCompleted),
			fmt.Sprintf("Step %s completed successfully", step.ID),
			map[string]interface{}{"step_id": step.ID, "result": output.Result})
		e.observeStepFinished(execution.WorkflowID, stepExec, string(WorkflowStatusCompleted))

		//执行成功返回nil，跳出重试
		return nil
	}

	// All retries failed
	return e.failStep(stepExec, execution, lastErr)
}

func (e *Engine) executeForeachStep(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
) error {
	items, err := e.resolveForeachItems(step, execution)
	if err != nil {
		return e.failStep(stepExec, execution, apperr.InvalidArgument("failed to resolve foreach items").WithCode("workflow_foreach_resolve_failed").WithCause(err))
	}

	maxRetries := step.Retries
	if maxRetries == 0 {
		maxRetries = 1
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			stepExec.RetryCount++
			e.publishEvent(execution.ID, "step_retry", string(WorkflowStatusRunning),
				fmt.Sprintf("Retrying step %s (attempt %d/%d)", step.ID, attempt+1, maxRetries),
				map[string]interface{}{"step_id": step.ID, "retry_count": attempt})
		}

		results, err := e.executeForeachItems(ctx, step, execution, items)
		if err != nil {
			lastErr = err
			continue
		}

		aggregated := map[string]interface{}{
			"items": results,
			"count": len(results),
		}

		stepExec.Result = aggregated
		stepExec.Status = WorkflowStatusCompleted
		now := time.Now()
		stepExec.CompletedAt = &now

		execution.Context.SetStepOutput(step.ID, aggregated)
		if step.OutputAlias != "" {
			execution.Context.SetVariable(step.OutputAlias, aggregated)
		}
		e.notifyExecutionContextChange(execution)

		if step.Route != nil {
			selectedRoute, targets, err := e.applyRouteSelection(step, execution)
			if err != nil {
				return e.failStep(stepExec, execution, apperr.Internal("failed to apply route selection").WithCode("workflow_route_apply_failed").WithCause(err))
			}
			stepExec.Metadata["route"] = selectedRoute
			stepExec.Metadata["route_targets"] = targets
		}
		_ = e.persistExecutionSnapshot(ctx, execution)

		e.publishEvent(execution.ID, "step_completed", string(WorkflowStatusCompleted),
			fmt.Sprintf("Step %s completed successfully", step.ID),
			map[string]interface{}{
				"step_id":    step.ID,
				"result":     aggregated,
				"item_count": len(results),
				"foreach":    true,
			})
		e.observeStepFinished(execution.WorkflowID, stepExec, string(WorkflowStatusCompleted))

		return nil
	}

	return e.failStep(stepExec, execution, lastErr)
}

func (e *Engine) resolveForeachItems(step *Step, execution *WorkflowExecution) ([]interface{}, error) {
	templateEngine := NewTemplateEngine(execution.Context)
	resolved, err := templateEngine.ResolveValue(step.Foreach.From)
	if err != nil {
		return nil, err
	}

	switch items := resolved.(type) {
	case []interface{}:
		return items, nil
	case []string:
		out := make([]interface{}, len(items))
		for i, item := range items {
			out[i] = item
		}
		return out, nil
	case []map[string]interface{}:
		out := make([]interface{}, len(items))
		for i, item := range items {
			out[i] = item
		}
		return out, nil
	default:
		return nil, apperr.InvalidArgumentf("foreach.from must resolve to an array, got %T", resolved).WithCode("workflow_foreach_input_invalid")
	}
}

func (e *Engine) executeForeachItems(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	items []interface{},
) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, len(items))
	maxParallel := step.Foreach.MaxParallel
	if maxParallel <= 0 {
		maxParallel = e.maxConcurrency
		if maxParallel <= 0 {
			maxParallel = 1
		}
	}

	semaphore := make(chan struct{}, maxParallel)
	errCh := make(chan error, len(items))
	var wg sync.WaitGroup

	for idx, item := range items {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(itemIndex int, currentItem interface{}) {
			defer wg.Done()
			defer func() { <-semaphore }()

			result, err := e.executeForeachItem(ctx, step, execution, itemIndex, currentItem)
			if err != nil {
				errCh <- fmt.Errorf("foreach item %d failed: %w", itemIndex, err)
				return
			}
			results[itemIndex] = result
		}(idx, item)
	}

	wg.Wait()
	close(errCh)

	if len(errCh) > 0 {
		return nil, <-errCh
	}
	return results, nil
}

func (e *Engine) executeForeachItem(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	itemIndex int,
	item interface{},
) (map[string]interface{}, error) {
	localCtx := cloneExecutionContext(execution.Context)
	itemVar := step.Foreach.ItemAs
	if itemVar == "" {
		itemVar = "item"
	}
	indexVar := step.Foreach.IndexAs
	if indexVar == "" {
		indexVar = "index"
	}
	localCtx.SetVariable(itemVar, item)
	localCtx.SetVariable(indexVar, itemIndex)

	templateEngine := NewTemplateEngine(localCtx)
	renderedParams, err := templateEngine.RenderParameters(step.Parameters)
	if err != nil {
		return nil, apperr.InvalidArgument("failed to render foreach item parameters").WithCode("workflow_foreach_params_render_failed").WithCause(err)
	}

	taskInput := &agent.TaskInput{
		TaskID:        uuid.New().String(),
		Action:        step.Action,
		Parameters:    renderedParams,
		Context:       make(map[string]interface{}),
		ContextReader: e.newExecutionContextReader(execution),
	}

	ag, err := e.agentRegistry.Get(step.AgentName)
	if err != nil {
		return nil, apperr.NotFoundf("agent not found: %s", step.AgentName).WithCode("workflow_step_agent_not_found")
	}

	stepCtx := ctx
	if step.Timeout != nil {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
		defer cancel()
	}

	output, err := ag.Execute(stepCtx, taskInput)
	if err != nil {
		return nil, err
	}
	if !output.Success {
		return nil, apperr.Internalf("step execution failed: %s", output.Error).WithCode("workflow_step_execution_failed")
	}

	if output.Result == nil {
		output.Result = make(map[string]interface{})
	}
	output.Result["_item_index"] = itemIndex
	return output.Result, nil
}

func cloneExecutionContext(src *ExecutionContext) *ExecutionContext {
	clone := NewExecutionContext(nil)
	for key, value := range src.Variables {
		clone.Variables[key] = value
	}
	for stepID, output := range src.Outputs {
		clone.Outputs[stepID] = copyMap(output)
	}
	return clone
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
			"step_type":   workflowStepType(step),
		},
	}

	e.mu.Lock()
	execution.StepExecutions[step.ID] = stepExec
	e.mu.Unlock()
	_ = e.persistExecutionSnapshot(context.Background(), execution)

	e.publishEvent(execution.ID, "step_skipped", string(WorkflowStatusSkipped),
		fmt.Sprintf("Step %s skipped", step.ID),
		map[string]interface{}{
			"step_id": step.ID,
			"reason":  reason,
		})
	if e.metrics != nil {
		e.metrics.ObserveStepSkipped(execution.WorkflowID, step.ID, reason)
	}
}

// failStep marks a step as failed
func (e *Engine) failStep(stepExec *StepExecution, execution *WorkflowExecution, err error) error {
	stepExec.Status = WorkflowStatusFailed
	stepExec.Error = err.Error()
	if stepExec.Metadata == nil {
		stepExec.Metadata = make(map[string]interface{})
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Code != "" {
		stepExec.Metadata["error_code"] = appErr.Code
	}
	now := time.Now()
	stepExec.CompletedAt = &now
	_ = e.persistExecutionSnapshot(context.Background(), execution)

	e.publishEvent(execution.ID, "step_failed", string(WorkflowStatusFailed),
		fmt.Sprintf("Step %s failed: %v", stepExec.StepID, err),
		map[string]interface{}{"step_id": stepExec.StepID, "error": err.Error()})
	if e.metrics != nil {
		e.metrics.ObserveStepFailed(execution.WorkflowID, stepExec.StepID)
	}
	e.observeStepFinished(execution.WorkflowID, stepExec, string(WorkflowStatusFailed))

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
	if execution.CompletedAt != nil || (execution.Status != WorkflowStatusRunning && execution.Status != WorkflowStatusPending) {
		e.mu.Unlock()
		return
	}
	execution.Status = status
	if status == WorkflowStatusFailed || status == WorkflowStatusCancelled {
		execution.Error = message
	}
	now := time.Now()
	execution.CompletedAt = &now
	delete(e.executionStops, execution.ID)
	e.mu.Unlock()

	_ = e.persistExecutionSnapshot(context.Background(), execution)
	if status == WorkflowStatusCancelled && e.metrics != nil {
		e.metrics.ObserveCancel(execution.WorkflowID)
	}
	if e.metrics != nil {
		e.metrics.ObserveWorkflowFinished(execution.WorkflowID, string(status), now.Sub(execution.StartedAt))
	}

	e.publishEvent(execution.ID, "workflow_completed", string(status), message, nil)
}

// publishEvent publishes a workflow event
func (e *Engine) publishEvent(executionID, eventType, status, message string, data map[string]interface{}) {
	if e.eventPublisher != nil {
		e.eventPublisher.PublishWorkflowEvent(executionID, eventType, status, message, data)
	}
}

// SetEventPublisher updates the workflow event publisher.
func (e *Engine) SetEventPublisher(publisher EventPublisher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventPublisher = publisher
}

// GetExecution retrieves a workflow execution by ID
func (e *Engine) GetExecution(ctx context.Context, executionID string) (*WorkflowExecution, error) {
	e.mu.RLock()
	execution, exists := e.executionCache[executionID]
	e.mu.RUnlock()

	if exists {
		return execution, nil
	}

	// Try to load from repository
	if e.repository != nil {
		return e.repository.GetExecution(ctx, executionID)
	}

	return nil, apperr.NotFoundf("execution not found: %s", executionID).WithCode("execution_not_found")
}

// CancelExecution cancels a running workflow execution
func (e *Engine) CancelExecution(ctx context.Context, executionID string) error {
	execution, err := e.GetExecution(ctx, executionID)
	if err != nil {
		return err
	}

	if execution.Status != WorkflowStatusRunning {
		return apperr.Conflict("execution is not running").WithCode("execution_cancel_conflict")
	}

	e.mu.RLock()
	cancel := e.executionStops[executionID]
	e.mu.RUnlock()
	if cancel == nil {
		return apperr.Internal("execution cancel function not found").WithCode("execution_cancel_unavailable")
	}

	cancel()
	if e.metrics != nil {
		e.metrics.ObserveCancel(execution.WorkflowID)
	}
	return nil
}

func (e *Engine) registerExecutionStop(executionID string, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.executionStops[executionID] = cancel
}

func (e *Engine) persistWorkflowDefinition(ctx context.Context, workflow *Workflow) error {
	if e.repository == nil || workflow == nil {
		return nil
	}
	return e.repository.SaveWorkflow(ctx, workflow)
}

func (e *Engine) persistExecutionSnapshot(ctx context.Context, execution *WorkflowExecution) error {
	if e.repository == nil || execution == nil {
		return nil
	}
	return e.repository.SaveExecution(ctx, execution)
}

// RecoverRunningExecutions marks unfinished executions as interrupted after a restart
// and repopulates the in-memory execution cache.
func (e *Engine) RecoverRunningExecutions(ctx context.Context) (int, error) {
	if e.repository == nil {
		return 0, nil
	}

	runningExecutions, err := e.repository.GetRunningExecutions(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load running executions: %w", err)
	}

	recovered := 0
	for _, execution := range runningExecutions {
		if execution == nil {
			continue
		}

		e.mu.Lock()
		e.executionCache[execution.ID] = execution
		e.mu.Unlock()

		execution.Status = WorkflowStatusFailed
		execution.RecoveryStatus = RecoveryStatusInterrupted
		execution.Error = "workflow interrupted by server restart"
		now := time.Now()
		execution.CompletedAt = &now

		if err := e.persistExecutionSnapshot(ctx, execution); err != nil {
			return recovered, fmt.Errorf("failed to persist interrupted execution %s: %w", execution.ID, err)
		}
		if e.metrics != nil {
			e.metrics.ObserveRecovery(execution.WorkflowID, string(execution.RecoveryStatus))
		}

		recovered++
	}

	return recovered, nil
}

func workflowStepType(step *Step) string {
	if step == nil {
		return "unknown"
	}
	if step.Streaming != nil && step.Streaming.Enabled {
		return "streaming"
	}
	if step.Foreach != nil {
		return "foreach"
	}
	return "standard"
}

func (e *Engine) observeWorkflowStarted(workflow *Workflow) {
	if e.metrics == nil || workflow == nil {
		return
	}
	e.metrics.ObserveWorkflowStarted(workflow.ID)
}

func (e *Engine) observeStepStarted(workflowID string, step *Step, stepExec *StepExecution) {
	if e.metrics == nil || step == nil || stepExec == nil {
		return
	}
	e.metrics.ObserveStepStarted(workflowID, step.ID, workflowStepType(step))
}

func (e *Engine) observeStepFinished(workflowID string, stepExec *StepExecution, status string) {
	if e.metrics == nil || stepExec == nil {
		return
	}
	completedAt := time.Now()
	if stepExec.CompletedAt != nil {
		completedAt = *stepExec.CompletedAt
	}
	stepType, _ := stepExec.Metadata["step_type"].(string)
	if stepType == "" {
		stepType = "unknown"
	}
	e.metrics.ObserveStepFinished(workflowID, stepExec.StepID, stepType, status, completedAt.Sub(stepExec.StartedAt))
}
