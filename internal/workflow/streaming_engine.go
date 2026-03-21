package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
)

// StreamingEngine 扩展Engine，增加流式执行能力
type StreamingEngine struct {
	*Engine                                       // 嵌入原有引擎
	buffers   map[string]map[string]*StreamBuffer // executionID -> stepID -> 缓冲区
	buffersMu sync.RWMutex                        // 缓冲区锁
	enabled   bool                                // 是否启用流式功能
}

// agentEventPublisherAdapter 适配器：将workflow.EventPublisher适配为agent.EventPublisher
type agentEventPublisherAdapter struct {
	workflowPublisher EventPublisher
	executionID       string
}

// PublishTaskEvent 实现agent.EventPublisher接口
func (a *agentEventPublisherAdapter) PublishTaskEvent(taskID string, eventType string, status string, message string, result map[string]interface{}, errorMsg string) {
	// 将task事件转换为workflow事件
	data := make(map[string]interface{})
	if result != nil {
		data["result"] = result
	}
	if errorMsg != "" {
		data["error"] = errorMsg
	}
	data["task_id"] = taskID

	a.workflowPublisher.PublishWorkflowEvent(a.executionID, eventType, status, message, data)
}

// NewStreamingEngine 创建新的流式工作流引擎
func NewStreamingEngine(config EngineConfig) *StreamingEngine {
	baseEngine := NewEngine(config)

	return &StreamingEngine{
		Engine:  baseEngine,                                // 复用基础工作流执行能力，流式引擎是在其上扩展流式调度
		buffers: make(map[string]map[string]*StreamBuffer), // 运行中的流式步骤输出缓冲区：executionID -> stepID -> buffer
		enabled: true,                                      // 当前实例启用流式能力
	}
}

// Execute 执行工作流（覆盖基类方法，支持流式）
func (e *StreamingEngine) Execute(ctx context.Context, workflow *Workflow, variables map[string]interface{}) (*WorkflowExecution, error) {
	// 如果没有任何步骤启用流式，使用原有引擎
	if !e.hasStreamingSteps(workflow) {
		return e.Engine.Execute(ctx, workflow, variables)
	}

	// 验证工作流
	dag, err := NewDAG(workflow.Steps)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}

	// 创建执行上下文
	execution := &WorkflowExecution{
		ID:             uuid.New().String(),
		WorkflowID:     workflow.ID,
		Status:         WorkflowStatusRunning,
		StepExecutions: make(map[string]*StepExecution),
		Context:        NewExecutionContext(variables),
		Checkpoints:    make(map[string]*StreamCheckpoint),
		StartedAt:      time.Now(),
	}

	// 初始化workflow变量
	if workflow.Variables != nil {
		for k, v := range workflow.Variables {
			execution.Context.SetVariable(k, v)
		}
	}

	// 缓存执行
	e.mu.Lock()
	e.executionCache[execution.ID] = execution
	e.mu.Unlock()

	// 保存到repository
	if e.repository != nil {
		if err := e.repository.SaveExecution(execution); err != nil {
			return nil, fmt.Errorf("failed to save execution: %w", err)
		}
	}

	// 发布开始事件
	e.publishEvent(execution.ID, "workflow_started", string(WorkflowStatusRunning), "Workflow execution started", nil)

	// 在后台执行工作流
	go e.executeWorkflowStreaming(ctx, workflow, dag, execution)

	return execution, nil
}

// hasStreamingSteps 检查是否有步骤启用了流式
func (e *StreamingEngine) hasStreamingSteps(workflow *Workflow) bool {
	for _, step := range workflow.Steps {
		if step.Streaming != nil && step.Streaming.Enabled {
			return true
		}
	}
	return false
}

// executeWorkflowStreaming 执行流式工作流
func (e *StreamingEngine) executeWorkflowStreaming(ctx context.Context, workflow *Workflow, dag *DAG, execution *WorkflowExecution) {
	e.executeWorkflowStreamingWithState(ctx, workflow, dag, execution, make(map[string]bool), make(map[string]bool))
}

func (e *StreamingEngine) executeWorkflowStreamingWithState(
	ctx context.Context,
	workflow *Workflow,
	dag *DAG,
	execution *WorkflowExecution,
	completedSteps map[string]bool,
	failedSteps map[string]bool,
) {
	// 设置超时
	timeout := e.defaultTimeout
	if workflow.Timeout != nil {
		timeout = *workflow.Timeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 清理资源
	defer e.cleanupBuffers(execution.ID)

	// 跟踪步骤状态
	runningSteps := make(map[string]bool)
	mu := &sync.Mutex{}
	triggerManager := NewTriggerManager()

	// 统计总步骤数
	totalSteps := len(workflow.Steps)

	// 为每个步骤创建Trigger
	stepMap := make(map[string]*Step)
	for _, step := range workflow.Steps {
		stepMap[step.ID] = step

		// 获取所有依赖的缓冲区
		buffers := make(map[string]*StreamBuffer)
		for _, depID := range step.DependsOn {
			if buffer := e.getBuffer(execution.ID, depID); buffer != nil {
				buffers[depID] = buffer
			}
		}

		// 创建并注册多buffer Trigger
		trigger := NewMultiTrigger(step.ID, step, buffers)
		triggerManager.RegisterTrigger(step.ID, trigger)
	}

	// 并发控制
	semaphore := make(chan struct{}, e.maxConcurrency)
	var wg sync.WaitGroup

	// 启动异步触发监控循环
	monitorDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond) // 每100ms检查一次
		defer ticker.Stop()

		for {
			select {
			case <-execCtx.Done():
				close(monitorDone)
				return
			case <-ticker.C:
				// 检查是否所有步骤都完成
				mu.Lock()
				finishedCount := len(completedSteps) + len(failedSteps)
				mu.Unlock()

				if finishedCount >= totalSteps {
					close(monitorDone)
					return
				}

				// 检查哪些步骤应该触发
				for _, step := range workflow.Steps {
					trigger := triggerManager.GetTrigger(step.ID)
					if trigger == nil {
						continue
					}

					// 检查是否已在运行或已完成
					mu.Lock()
					isRunning := runningSteps[step.ID]
					isCompleted := completedSteps[step.ID] || failedSteps[step.ID]
					mu.Unlock()

					if isRunning || isCompleted {
						continue
					}

					// 动态更新所有依赖的buffers（因为创建trigger时buffer可能还不存在）
					if len(step.DependsOn) > 0 {
						buffers := make(map[string]*StreamBuffer)
						for _, depID := range step.DependsOn {
							if buffer := e.getBuffer(execution.ID, depID); buffer != nil {
								buffers[depID] = buffer
							}
						}
						// 更新trigger的buffers
						trigger.UpdateBuffers(buffers)
					}

					// 检查是否应该触发
					shouldTrigger, reason := trigger.ShouldTrigger()
					if !shouldTrigger && execution.ResumeState != nil &&
						step.Streaming != nil && step.Streaming.WaitFor == "full" {
						allDepsCompleted := true
						for _, depID := range step.DependsOn {
							if !completedSteps[depID] {
								allDepsCompleted = false
								break
							}
						}
						if allDepsCompleted {
							shouldTrigger = true
							reason = "resume_all_deps_completed"
						}
					}
					if !shouldTrigger {
						continue
					}

					// 检查依赖是否满足
					// 注意：对于流式步骤，不需要等待依赖完成，
					// 只需要满足trigger条件（如MinStartTokens）即可
					depsReady := true
					for _, depID := range step.DependsOn {
						mu.Lock()
						// 检查依赖是否失败
						if failedSteps[depID] {
							depsReady = false
						}
						// 对于流式步骤，只要依赖已开始（有buffer）即可，不需要等待完成
						mu.Unlock()
						if !depsReady {
							break
						}
					}

					if !depsReady {
						continue
					}

					// 标记为已触发
					trigger.MarkTriggered()

					// 标记为运行中
					mu.Lock()
					runningSteps[step.ID] = true
					mu.Unlock()

					// 发布触发事件
					e.publishEvent(execution.ID, "step_triggered", string(WorkflowStatusRunning),
						fmt.Sprintf("Step %s triggered (reason: %s)", step.ID, reason),
						map[string]interface{}{
							"step_id": step.ID,
							"reason":  reason,
						})

					// 异步执行步骤
					wg.Add(1)
					semaphore <- struct{}{} // 获取信号量

					go func(s *Step) {
						defer wg.Done()
						defer func() { <-semaphore }() // 释放信号量

						// 执行步骤
						var err error
						if s.Streaming != nil && s.Streaming.Enabled {
							//流式状态执行
							err = e.executeStepStreaming(execCtx, s, execution)
						} else {
							//阻塞执行
							err = e.Engine.executeStep(execCtx, s, execution)
						}

						// 更新状态
						mu.Lock()
						delete(runningSteps, s.ID)
						if err != nil {
							failedSteps[s.ID] = true
						} else {
							completedSteps[s.ID] = true
						}
						mu.Unlock()
					}(step)
				}
			}
		}
	}()

	// 等待监控循环结束
	<-monitorDone

	// 等待所有执行的步骤完成
	wg.Wait()

	// 检查结果
	mu.Lock()
	hasFailed := len(failedSteps) > 0
	mu.Unlock()

	if hasFailed {
		mu.Lock()
		failedStepList := make([]string, 0, len(failedSteps))
		for stepID := range failedSteps {
			failedStepList = append(failedStepList, stepID)
		}
		mu.Unlock()

		if workflow.OnFailure != nil && workflow.OnFailure.Rollback {
			e.rollbackStreamingWorkflow(execCtx, workflow, execution, completedSteps)
		}

		e.finishExecution(execution, WorkflowStatusFailed,
			fmt.Sprintf("Workflow failed: steps %v failed", failedStepList))
		return
	}

	// 所有步骤成功完成
	e.finishExecution(execution, WorkflowStatusCompleted, "Workflow completed successfully")
}

// executeStepStreaming 执行流式步骤
func (e *StreamingEngine) executeStepStreaming(ctx context.Context, step *Step, execution *WorkflowExecution) error {
	// 创建步骤执行记录
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
		fmt.Sprintf("Step %s started (streaming mode)", step.ID),
		map[string]interface{}{"step_id": step.ID, "streaming": true})

	// 创建流式缓冲区
	buffer := NewStreamBuffer(step.ID, step.Streaming)
	e.registerBuffer(execution.ID, step.ID, buffer)

	// 如果等待模式是partial，订阅上游并等待初始数据
	if step.Streaming.WaitFor == "partial" && len(step.DependsOn) > 0 {
		if err := e.subscribeToUpstream(ctx, step, execution, buffer); err != nil {
			switch e.getStreamingOnError(step) {
			case "continue":
				e.publishEvent(execution.ID, "step_stream_error_continue", string(WorkflowStatusRunning),
					fmt.Sprintf("Step %s continuing after upstream subscription error", step.ID),
					map[string]interface{}{
						"step_id": step.ID,
						"phase":   "subscribe_upstream",
						"error":   err.Error(),
					})
			case "fallback":
				return e.executeStepStreamingFallback(ctx, step, execution, stepExec, buffer,
					fmt.Errorf("failed to subscribe upstream: %w", err))
			default:
				buffer.MarkComplete()
				return e.failStep(stepExec, execution, fmt.Errorf("failed to subscribe upstream: %w", err))
			}
		}
	}

	// 渲染参数（此时已确保有初始数据）
	engine := NewTemplateEngine(execution.Context)
	renderedParams, err := engine.RenderParameters(step.Parameters)
	if err != nil {
		if e.getStreamingOnError(step) == "fallback" {
			return e.executeStepStreamingFallback(ctx, step, execution, stepExec, buffer,
				fmt.Errorf("failed to render parameters: %w", err))
		}
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, fmt.Errorf("failed to render parameters: %w", err))
	}

	// 执行步骤（带重试）
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

		// 创建任务
		taskID := uuid.New().String()
		stepExec.TaskID = taskID

		// 创建流式回调
		streamCallback := func(chunk map[string]interface{}) {
			// 写入缓冲区
			buffer.Write(StreamChunk{
				Index:     buffer.TotalChunks(),
				Data:      chunk,
				Tokens:    e.estimateTokens(chunk),
				Timestamp: time.Now(),
			})
			e.saveStreamingCheckpoint(execution, step.ID, buffer, nil)

			// 发布流式事件
			e.publishEvent(execution.ID, "step_streaming", string(WorkflowStatusRunning),
				fmt.Sprintf("Step %s streaming data", step.ID),
				map[string]interface{}{
					"step_id":      step.ID,
					"chunk_index":  buffer.TotalChunks() - 1,
					"total_tokens": buffer.TotalTokens(),
				})
		}

		// 创建ContextReader
		contextReader := e.newExecutionContextReader(execution)

		taskInput := &agent.TaskInput{
			TaskID:         taskID,
			Action:         step.Action,
			Parameters:     renderedParams,
			Context:        make(map[string]interface{}),
			StreamCallback: streamCallback,
			ContextReader:  contextReader, // 传递动态Context访问
		}

		// 如果引擎有eventPublisher，将其适配为agent.EventPublisher
		if e.eventPublisher != nil {
			taskInput.EventPublisher = &agentEventPublisherAdapter{
				workflowPublisher: e.eventPublisher,
				executionID:       execution.ID,
			}
		}

		// 获取agent
		ag, err := e.agentRegistry.Get(step.AgentName)
		if err != nil {
			lastErr = fmt.Errorf("agent not found: %s", step.AgentName)
			continue
		}

		// 设置超时
		stepCtx := ctx
		if step.Timeout != nil {
			var cancel context.CancelFunc
			stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
			defer cancel()
		}

		attemptCtx, cancelAttempt := context.WithCancel(stepCtx)
		detectedErrCh := make(chan error, 1)
		e.startStreamErrorMonitor(attemptCtx, step, execution, stepExec, buffer, func(err error) {
			select {
			case detectedErrCh <- err:
			default:
			}
			cancelAttempt()
		})

		// 执行agent任务
		output, err := ag.Execute(attemptCtx, taskInput)
		cancelAttempt()

		select {
		case detectedErr := <-detectedErrCh:
			lastErr = detectedErr
			if err == nil || err == context.Canceled {
				err = detectedErr
			}
		default:
		}
		if err != nil {
			lastErr = err
			continue
		}

		if !output.Success {
			lastErr = fmt.Errorf("step execution failed: %s", output.Error)
			continue
		}

		// 成功
		return e.completeStreamingStep(
			step,
			execution,
			stepExec,
			buffer,
			output.Result,
			fmt.Sprintf("Step %s completed successfully (streaming)", step.ID),
			"step_completed",
		)
	}

	// 所有重试失败
	e.restoreFromCheckpoint(execution, step.ID, buffer)
	switch e.getStreamingOnError(step) {
	case "continue":
		return e.completeStreamingStepWithPartialData(step, execution, stepExec, buffer, lastErr)
	case "fallback":
		return e.executeStepStreamingFallback(ctx, step, execution, stepExec, buffer, lastErr)
	default:
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, lastErr)
	}
}

func (e *StreamingEngine) getStreamingOnError(step *Step) string {
	if step.Streaming == nil || step.Streaming.OnError == "" {
		return DefaultOnError
	}
	return step.Streaming.OnError
}

func (e *StreamingEngine) completeStreamingStep(
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	buffer *StreamBuffer,
	result map[string]interface{},
	message string,
	eventType string,
) error {
	if result == nil {
		result = make(map[string]interface{})
	}
	stepExec.Result = result
	stepExec.Status = WorkflowStatusCompleted
	now := time.Now()
	stepExec.CompletedAt = &now

	e.saveStreamingCheckpoint(execution, step.ID, buffer, result)
	buffer.MarkComplete()

	execution.Context.SetStepOutput(step.ID, result)
	if step.OutputAlias != "" {
		execution.Context.SetVariable(step.OutputAlias, result)
	}

	e.publishEvent(execution.ID, eventType, string(WorkflowStatusCompleted), message, map[string]interface{}{
		"step_id":      step.ID,
		"result":       result,
		"total_tokens": buffer.TotalTokens(),
		"total_chunks": buffer.TotalChunks(),
	})

	return nil
}

func (e *StreamingEngine) completeStreamingStepWithPartialData(
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	buffer *StreamBuffer,
	cause error,
) error {
	partialResult := e.collectPartialStreamResult(buffer)
	if len(partialResult) == 0 {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, cause)
	}

	partialResult["_partial"] = true
	partialResult["_partial_error"] = cause.Error()
	execution.Context.SetVariable(step.ID+"_partial", true)

	return e.completeStreamingStep(
		step,
		execution,
		stepExec,
		buffer,
		partialResult,
		fmt.Sprintf("Step %s completed with partial streaming data", step.ID),
		"step_completed_partial",
	)
}

func (e *StreamingEngine) collectPartialStreamResult(buffer *StreamBuffer) map[string]interface{} {
	chunks := buffer.ReadAll()
	if len(chunks) == 0 {
		return nil
	}

	result := make(map[string]interface{})
	for _, chunk := range chunks {
		for k, v := range chunk.Data {
			result[k] = v
		}
	}

	return result
}

func (e *StreamingEngine) executeStepStreamingFallback(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	buffer *StreamBuffer,
	cause error,
) error {
	e.publishEvent(execution.ID, "step_stream_error_fallback", string(WorkflowStatusRunning),
		fmt.Sprintf("Step %s falling back to blocking mode", step.ID),
		map[string]interface{}{
			"step_id": step.ID,
			"error":   cause.Error(),
		})

	if err := e.waitForDependenciesCompletion(ctx, step, execution); err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, fmt.Errorf("fallback dependency wait failed: %w", err))
	}

	engine := NewTemplateEngine(execution.Context)
	renderedParams, err := engine.RenderParameters(step.Parameters)
	if err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, fmt.Errorf("fallback render failed: %w", err))
	}

	taskID := uuid.New().String()
	stepExec.TaskID = taskID

	taskInput := &agent.TaskInput{
		TaskID:         taskID,
		Action:         step.Action,
		Parameters:     renderedParams,
		Context:        make(map[string]interface{}),
		ContextReader:  e.newExecutionContextReader(execution),
		StreamCallback: nil,
	}

	if e.eventPublisher != nil {
		taskInput.EventPublisher = &agentEventPublisherAdapter{
			workflowPublisher: e.eventPublisher,
			executionID:       execution.ID,
		}
	}

	ag, err := e.agentRegistry.Get(step.AgentName)
	if err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, fmt.Errorf("fallback agent not found: %s", step.AgentName))
	}

	stepCtx := ctx
	if step.Timeout != nil {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
		defer cancel()
	}

	output, err := ag.Execute(stepCtx, taskInput)
	if err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, fmt.Errorf("fallback execution failed: %w", err))
	}
	if !output.Success {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, fmt.Errorf("fallback step execution failed: %s", output.Error))
	}

	if output.Result == nil {
		output.Result = make(map[string]interface{})
	}

	_ = buffer.Write(StreamChunk{
		Index:     buffer.TotalChunks(),
		Data:      output.Result,
		Tokens:    e.estimateTokens(output.Result),
		Timestamp: time.Now(),
	})

	return e.completeStreamingStep(
		step,
		execution,
		stepExec,
		buffer,
		output.Result,
		fmt.Sprintf("Step %s completed successfully (fallback mode)", step.ID),
		"step_completed_fallback",
	)
}

func (e *StreamingEngine) waitForDependenciesCompletion(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
) error {
	if len(step.DependsOn) == 0 {
		return nil
	}

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		allDone := true

		e.mu.RLock()
		for _, depID := range step.DependsOn {
			depExec := execution.StepExecutions[depID]
			if depExec == nil || depExec.Status == WorkflowStatusRunning || depExec.Status == WorkflowStatusPending {
				allDone = false
				break
			}
			if depExec.Status == WorkflowStatusFailed {
				e.mu.RUnlock()
				return fmt.Errorf("dependency %s failed", depID)
			}
		}
		e.mu.RUnlock()

		if allDone {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// subscribeToUpstream 订阅上游流式数据，等待初始数据到达后返回
func (e *StreamingEngine) subscribeToUpstream(ctx context.Context, step *Step, execution *WorkflowExecution, buffer *StreamBuffer) error {
	if len(step.DependsOn) == 0 {
		return nil
	}

	// 创建ContextReader用于通知
	contextReader := e.newExecutionContextReader(execution).(*executionContextReader)

	// 为每个依赖创建初始数据通知channel
	initialDataChannels := make(map[string]chan struct{})

	for _, depID := range step.DependsOn {
		depBuffer := e.getBuffer(execution.ID, depID)
		if depBuffer == nil {
			continue
		}

		// 订阅上游数据
		minTokens := step.Streaming.MinStartTokens
		if minTokens == 0 {
			minTokens = 1 // 至少1个token
		}

		subscriber := depBuffer.Subscribe(step.ID, minTokens)

		// 创建初始数据通知channel
		initialDataChan := make(chan struct{}, 1)
		initialDataChannels[depID] = initialDataChan

		// 启动协程处理流式输入
		go func(sub *StreamSubscriber, depStepID string, notifyChan chan struct{}) {
			initialDataReceived := false

			for chunk := range sub.Channel {
				// 上游步骤一旦完成，最终输出会写回 execution.Context。
				// 这里忽略完成后的 replay/尾部 chunk，避免旧的 partial 数据覆盖最终输出。
				e.mu.Lock()
				stepExec := execution.StepExecutions[depStepID]
				stepCompleted := stepExec != nil && stepExec.Status == WorkflowStatusCompleted
				if !stepCompleted {
					if execution.Context.Outputs[depStepID] == nil {
						execution.Context.Outputs[depStepID] = make(map[string]interface{})
					}
					// 累积部分数据
					for k, v := range chunk.Data {
						execution.Context.Outputs[depStepID][k] = v
					}
				}
				e.mu.Unlock()

				// 通知ContextReader数据变更
				contextReader.notifyContextChange()

				// 第一次收到数据时通知主流程
				if !initialDataReceived {
					initialDataReceived = true
					select {
					case notifyChan <- struct{}{}:
					default:
					}
				}
			}
		}(subscriber, depID, initialDataChan)
	}

	// 等待所有依赖的初始数据（带超时）
	timeout := 5 * time.Second
	if step.Streaming.StreamTimeout > 0 {
		timeout = time.Duration(step.Streaming.StreamTimeout) * time.Second
	}

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	for depID, notifyChan := range initialDataChannels {
		select {
		case <-notifyChan:
			// 收到初始数据
			continue
		case <-timeoutTimer.C:
			return fmt.Errorf("timeout waiting for initial data from dependency: %s", depID)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// estimateTokens 估算token数（简单实现）
func (e *StreamingEngine) estimateTokens(data map[string]interface{}) int {
	// 简单估算：如果有token字段，直接返回长度
	if token, ok := data["token"].(string); ok {
		return len(token)
	}
	// 否则返回1
	return 1
}

// registerBuffer 注册缓冲区
func (e *StreamingEngine) registerBuffer(executionID, stepID string, buffer *StreamBuffer) {
	e.buffersMu.Lock()
	defer e.buffersMu.Unlock()

	if e.buffers[executionID] == nil {
		e.buffers[executionID] = make(map[string]*StreamBuffer)
	}
	e.buffers[executionID][stepID] = buffer
}

// unregisterBuffer 注销缓冲区
func (e *StreamingEngine) unregisterBuffer(executionID, stepID string) {
	e.buffersMu.Lock()
	defer e.buffersMu.Unlock()

	executionBuffers := e.buffers[executionID]
	if executionBuffers == nil {
		return
	}

	delete(executionBuffers, stepID)
	if len(executionBuffers) == 0 {
		delete(e.buffers, executionID)
	}
}

// getBuffer 获取缓冲区
func (e *StreamingEngine) getBuffer(executionID, stepID string) *StreamBuffer {
	e.buffersMu.RLock()
	defer e.buffersMu.RUnlock()

	executionBuffers := e.buffers[executionID]
	if executionBuffers == nil {
		return nil
	}
	return executionBuffers[stepID]
}

// cleanupBuffers 清理所有缓冲区
func (e *StreamingEngine) cleanupBuffers(executionID string) {
	e.buffersMu.Lock()
	defer e.buffersMu.Unlock()

	for _, buffer := range e.buffers[executionID] {
		buffer.MarkComplete()
	}
	delete(e.buffers, executionID)
}

// GetStreamingStatus 获取流式执行状态
func (e *StreamingEngine) GetStreamingStatus(executionID string) map[string]interface{} {
	e.buffersMu.RLock()
	defer e.buffersMu.RUnlock()

	status := make(map[string]interface{})
	bufferStatus := make([]map[string]interface{}, 0)
	executionBuffers := e.buffers[executionID]

	for stepID, buffer := range executionBuffers {
		bufferStatus = append(bufferStatus, map[string]interface{}{
			"step_id":          stepID,
			"total_tokens":     buffer.TotalTokens(),
			"total_chunks":     buffer.TotalChunks(),
			"completed":        buffer.IsCompleted(),
			"subscriber_count": buffer.GetSubscriberCount(),
		})
	}

	status["buffers"] = bufferStatus
	status["buffer_count"] = len(executionBuffers)

	return status
}

// executionContextReader 实现ContextReader接口，提供线程安全的Context访问
type executionContextReader struct {
	execution *WorkflowExecution
	engine    *StreamingEngine
	cond      *sync.Cond // 条件变量，用于数据变更通知
}

// newExecutionContextReader 创建ContextReader
func (e *StreamingEngine) newExecutionContextReader(execution *WorkflowExecution) agent.ContextReader {
	return &executionContextReader{
		execution: execution,
		engine:    e,
		cond:      sync.NewCond(&e.mu),
	}
}

// GetStepOutput 获取步骤输出（返回副本，线程安全）
func (r *executionContextReader) GetStepOutput(stepID string) (map[string]interface{}, bool) {
	r.engine.mu.RLock()
	defer r.engine.mu.RUnlock()

	output, exists := r.execution.Context.Outputs[stepID]
	if !exists || output == nil {
		return nil, false
	}

	// 返回副本，避免并发修改
	return copyMap(output), true
}

// GetVariable 获取全局变量
func (r *executionContextReader) GetVariable(key string) (interface{}, bool) {
	r.engine.mu.RLock()
	defer r.engine.mu.RUnlock()

	value, exists := r.execution.Context.Variables[key]
	return value, exists
}

// GetField 获取嵌套字段，支持路径如 "result.summary"
func (r *executionContextReader) GetField(stepID string, fieldPath string) (interface{}, bool) {
	r.engine.mu.RLock()
	defer r.engine.mu.RUnlock()

	output, exists := r.execution.Context.Outputs[stepID]
	if !exists || output == nil {
		return nil, false
	}

	// 解析字段路径
	value := getNestedField(output, fieldPath)
	return value, value != nil
}

// WaitForField 等待字段可用（主动等待，带超时和通知机制）
func (r *executionContextReader) WaitForField(stepID string, fieldPath string, timeout time.Duration) (interface{}, error) {
	deadline := time.Now().Add(timeout)

	for {
		// 先尝试获取字段
		r.engine.mu.RLock()
		output := r.execution.Context.Outputs[stepID]
		stepExec := r.execution.StepExecutions[stepID]
		r.engine.mu.RUnlock()

		// 检查字段是否存在
		if output != nil {
			value := getNestedField(output, fieldPath)
			if value != nil {
				return value, nil
			}
		}

		// 检查步骤是否已完成
		if stepExec != nil && stepExec.Status == WorkflowStatusCompleted {
			// 步骤已完成但字段不存在
			return nil, fmt.Errorf("field %s.%s not found (step completed)", stepID, fieldPath)
		}

		if stepExec != nil && stepExec.Status == WorkflowStatusFailed {
			// 步骤失败
			return nil, fmt.Errorf("step %s failed, field unavailable", stepID)
		}

		// 检查超时
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for field %s.%s", stepID, fieldPath)
		}

		// 等待通知或超时
		waitChan := make(chan struct{})
		go func() {
			r.cond.L.Lock()
			r.cond.Wait()
			r.cond.L.Unlock()
			close(waitChan)
		}()

		select {
		case <-waitChan:
			// 收到通知，重新检查
			continue
		case <-time.After(100 * time.Millisecond):
			// 超时保护，避免死锁
			continue
		}
	}
}

// IsStepCompleted 检查步骤是否已完成
func (r *executionContextReader) IsStepCompleted(stepID string) bool {
	r.engine.mu.RLock()
	defer r.engine.mu.RUnlock()

	stepExec, exists := r.execution.StepExecutions[stepID]
	if !exists {
		return false
	}

	return stepExec.Status == WorkflowStatusCompleted
}

// notifyContextChange 通知Context变更（在数据写入后调用）
func (r *executionContextReader) notifyContextChange() {
	r.cond.Broadcast()
}

// copyMap 深拷贝map（避免并发修改）
func copyMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}

	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		// 简单拷贝（如果value是map/slice，仍是引用）
		// 对于workflow场景，这通常足够
		dst[k] = v
	}
	return dst
}

// getNestedField 获取嵌套字段，支持 "result.summary" 或 "data.items[0]"
func getNestedField(data map[string]interface{}, path string) interface{} {
	if data == nil || path == "" {
		return nil
	}

	parts := strings.Split(path, ".")
	var current interface{} = data

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = v[part]
			if !ok {
				return nil
			}
		default:
			return nil
		}
	}

	return current
}
