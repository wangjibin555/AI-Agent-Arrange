package workflow

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// Engine 是工作流执行引擎。
// 它负责把静态 Workflow 定义转成运行中的 WorkflowExecution，并驱动各步骤按 DAG 关系推进。
type Engine struct {
	agentRegistry  *agent.Registry
	repository     Repository
	executionCache map[string]*WorkflowExecution // executionID -> execution
	executionStops map[string]context.CancelFunc // executionID -> cancel func
	mu             sync.RWMutex
	eventPublisher EventPublisher
	runtimeEvents  *runtimeEventDispatcher
	defaultTimeout time.Duration
	maxConcurrency int // 单个工作流允许的最大并行步骤数
	metrics        *monitor.WorkflowMetrics
}

// EventPublisher 定义工作流事件发布接口。
type EventPublisher interface {
	PublishWorkflowEvent(executionID string, eventType string, status string, message string, data map[string]interface{})
}

// EngineConfig 定义工作流引擎的运行配置。
type EngineConfig struct {
	AgentRegistry  *agent.Registry
	Repository     Repository
	EventPublisher EventPublisher
	DefaultTimeout time.Duration
	MaxConcurrency int // 0 表示不限制
}

// NewEngine 创建工作流引擎，并初始化运行时事件分发器。
func NewEngine(config EngineConfig) *Engine {
	if config.DefaultTimeout == 0 {
		config.DefaultTimeout = 30 * time.Minute
	}
	if config.MaxConcurrency == 0 {
		config.MaxConcurrency = 10 // Default to 10 parallel steps
	}

	engine := &Engine{
		agentRegistry:  config.AgentRegistry,
		repository:     config.Repository,
		eventPublisher: config.EventPublisher,
		executionCache: make(map[string]*WorkflowExecution),
		executionStops: make(map[string]context.CancelFunc),
		defaultTimeout: config.DefaultTimeout,
		maxConcurrency: config.MaxConcurrency,
	}
	engine.runtimeEvents = engine.newRuntimeEventDispatcher()
	return engine
}

func (e *Engine) SetMetrics(metrics *monitor.WorkflowMetrics) {
	e.metrics = metrics
	e.runtimeEvents = e.newRuntimeEventDispatcher()
}

type StepDecision string

const (
	StepDecisionWait    StepDecision = "wait"    // 依赖未满足，继续等待
	StepDecisionExecute StepDecision = "execute" // 满足执行条件，立即执行
	StepDecisionSkip    StepDecision = "skip"    // 明确不需要执行，标记跳过
)

// Execute 启动一次新的工作流执行。
// 这里会先完成编译和执行实例初始化，再异步进入真正的调度循环。
func (e *Engine) Execute(ctx context.Context, workflow *Workflow, variables map[string]interface{}) (*WorkflowExecution, error) {
	compiled, err := CompileWorkflow(workflow)
	if err != nil {
		return nil, err
	}

	execution, runCtx, err := e.bootstrapExecution(ctx, workflow, compiled, executionBootstrapOptions{
		Variables: variables,
	})
	if err != nil {
		return nil, err
	}

	// Execute workflow in background
	go e.executeWorkflow(runCtx, compiled, execution)

	return execution, nil
}

// executeWorkflow 按编译后的 DAG 关系推进步骤执行。
// 实际的步骤调度、状态变更、回滚与收尾逻辑会下沉到 runtime kernel 中统一处理。
func (e *Engine) executeWorkflow(ctx context.Context, workflow *CompiledWorkflow, execution *WorkflowExecution) {
	kernel := newRuntimeKernel(
		e,
		nil,
		workflow,
		execution,
		e.newStepExecutor(),
		make(map[string]bool),
		make(map[string]bool),
		func(ctx context.Context, completed map[string]bool) {
			e.rollbackWorkflow(ctx, execution, completed)
		},
	)
	e.runExecutionLifecycle(ctx, workflow.Source, execution, kernel.Run)
}

// evaluateStepDecision 判断步骤当前应执行、等待还是跳过。
// 这个决策会综合依赖完成情况、上游失败策略、动态路由和条件表达式。
func (e *Engine) evaluateStepDecision(
	workflow *CompiledWorkflow,
	step *CompiledStep,
	execution *WorkflowExecution,
	completed, failed map[string]bool,
) (StepDecision, string, error) {
	// 先检查普通依赖与 foreach 依赖是否满足。
	for _, edge := range workflow.Incoming[step.ID] {
		if edge.Kind != CompiledEdgeDependency && edge.Kind != CompiledEdgeForeach {
			continue
		}
		depID := edge.FromStepID
		//如果失败，并且没有设置继续执行不管当前节点或者忽略错误，则返回跳过
		if failed[depID] && (step.Runtime.ContinueOn == nil || !step.Runtime.ContinueOn.OnError) {
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
		if routeReason == "" {
			return StepDecisionWait, "", nil
		}
		return StepDecisionSkip, routeReason, nil
	}

	// 如果当前step有配置condition则评估当前情况进行执行
	if step.Runtime.Condition != nil {
		shouldExecute, err := e.evaluateCondition(step.Runtime.Condition, execution.Context, completed)
		if err != nil {
			return StepDecisionWait, "", err
		}
		if !shouldExecute {
			return StepDecisionSkip, "condition_false", nil
		}
	}

	return StepDecisionExecute, "", nil
}

// evaluateCondition 评估步骤条件配置。
func (e *Engine) evaluateCondition(cond *Condition, ctx *ExecutionContext, completed map[string]bool) (bool, error) {
	engine := NewTemplateEngine(ctx)

	switch cond.Type {
	case ConditionTypeAlways:
		return true, nil

	case ConditionTypeStatus:
		// 通过 completed 集合判断某个步骤是否已达到目标状态。
		return completed[cond.Status], nil

	case ConditionTypeExpression:
		// 使用模板引擎计算表达式。
		return engine.EvaluateCondition(cond.Expression)

	default:
		return false, apperr.InvalidArgumentf("unknown condition type: %s", cond.Type).WithCode("workflow_condition_type_invalid")
	}
}

// routeAllowsStep 根据上游路由步骤的选择结果判断当前步骤是否允许执行。
func (e *Engine) routeAllowsStep(workflow *CompiledWorkflow, step *CompiledStep, execution *WorkflowExecution) (bool, string, error) {
	for _, edge := range workflow.Incoming[step.ID] {
		if edge.Kind != CompiledEdgeRoute {
			continue
		}

		e.mu.RLock()
		selectedRoute := execution.RouteSelections[edge.FromStepID]
		routerExec := execution.StepExecutions[edge.FromStepID]
		e.mu.RUnlock()

		if selectedRoute == "" {
			if routerExec == nil || routerExec.Status == WorkflowStatusRunning || routerExec.Status == WorkflowStatusPending {
				return false, "", nil
			}
			return false, "", apperr.Internalf("route step %s completed without selection", edge.FromStepID).WithCode("workflow_route_selection_missing")
		}
		if edge.DefaultRoute {
			routerStep := workflow.StepByID[edge.FromStepID]
			if routerStep == nil || routerStep.Runtime.Route == nil {
				continue
			}
			if _, ok := routerStep.Runtime.Route.Cases[selectedRoute]; !ok {
				return true, "", nil
			}
			return false, fmt.Sprintf("route_filtered:%s=%s", edge.FromStepID, selectedRoute), nil
		}
		if edge.RouteKey != selectedRoute {
			if selectedRoute == "" {
				return false, "", apperr.Internalf("route step %s completed without selection", edge.FromStepID).WithCode("workflow_route_selection_missing")
			}
			return false, fmt.Sprintf("route_filtered:%s=%s", edge.FromStepID, selectedRoute), nil
		}
	}

	return true, "", nil
}

// routeTargetsStep 判断一个步骤是否受 route 控制，以及当前选中的分支是否命中该步骤。
func routeTargetsStep(route *RouteConfig, stepID, selected string) (bool, bool) {
	targeted := false
	// 先判断当前步骤是否属于该 route 管理的任何下游节点。
	for _, targets := range route.Cases {
		if slices.Contains(targets, stepID) {
			targeted = true
			break
		}
	}
	if !targeted && slices.Contains(route.Default, stepID) {
		targeted = true
	}
	// 返回值分别表示：
	// 1. 当前步骤是否属于该路由的下游集合
	// 2. 在本次 selected 分支下，该步骤是否应被放行执行
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

func (e *Engine) newExecutionContextReader(execution *WorkflowExecution) agent.ContextReader {
	return &executionContextReader{
		execution: execution,
		mu:        &e.mu,
		notifier:  e.getExecutionNotifier(execution),
	}
}

func (e *Engine) getExecutionNotifier(execution *WorkflowExecution) *executionNotifier {
	e.mu.Lock()
	defer e.mu.Unlock()

	if execution.notifier == nil {
		execution.notifier = newExecutionNotifier()
	}
	return execution.notifier
}

func (e *Engine) notifyExecutionContextChange(execution *WorkflowExecution) {
	e.mu.RLock()
	notifier := execution.notifier
	e.mu.RUnlock()
	if notifier != nil {
		notifier.Notify()
	}
}

// executeStep executes a single workflow step
func (e *Engine) executeStep(ctx context.Context, step *Step, execution *WorkflowExecution) error {
	return e.newStepExecutor().Execute(ctx, &CompiledStep{
		ID:      step.ID,
		Runtime: step,
	}, execution)
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

	renderedParams, err := e.renderStepParameters(localCtx, step)
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

	ag, err := e.resolveStepAgent(step)
	if err != nil {
		return nil, err
	}
	if err := validateAgentInputContract(ag, step, taskInput.Parameters); err != nil {
		return nil, err
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
	if output != nil && output.Success {
		if err := validateAgentOutputContract(ag, step, output.Result); err != nil {
			return nil, err
		}
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
	if src == nil {
		return NewExecutionContext(nil)
	}
	return src.Clone()
}

func (e *Engine) resolveRouteSelection(step *Step, execution *WorkflowExecution) (string, []string, error) {
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
	return selected, targets, nil
}

func (e *Engine) skipStep(step *Step, execution *WorkflowExecution, reason string) {
	e.executionState().SkipStep(execution, step, reason)
}

// failStep marks a step as failed
func (e *Engine) failStep(stepExec *StepExecution, execution *WorkflowExecution, err error) error {
	return e.executionState().FailStep(execution, stepExec, err)
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
	e.executionState().FinishExecution(execution, status, message)
}

// publishEvent publishes a workflow event
func (e *Engine) publishEvent(executionID, eventType, status, message string, data map[string]interface{}) {
	e.mu.RLock()
	execution := e.executionCache[executionID]
	e.mu.RUnlock()
	if execution == nil && e.repository != nil {
		execution, _ = e.repository.GetExecution(context.Background(), executionID)
	}
	e.emitRuntimeEvent(context.Background(), RuntimeEvent{
		Type:      eventType,
		Status:    status,
		Message:   message,
		Data:      data,
		Execution: execution,
	})
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

		e.emitRuntimeEvent(ctx, RuntimeEvent{
			Type:            "workflow_interrupted",
			Status:          string(execution.Status),
			Message:         execution.Error,
			Execution:       execution,
			PersistSnapshot: true,
			RecoveryStatus:  string(execution.RecoveryStatus),
		})

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
