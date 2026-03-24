package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
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
	stepID            string
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
	if a.stepID != "" {
		data["step_id"] = a.stepID
	}

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

	compiled, err := CompileWorkflow(workflow)
	if err != nil {
		return nil, err
	}

	execution, runCtx, err := e.bootstrapExecution(ctx, workflow, compiled, executionBootstrapOptions{
		Variables:             variables,
		InitializeCheckpoints: true,
	})
	if err != nil {
		return nil, err
	}

	// 在后台执行工作流
	go e.executeWorkflowStreaming(runCtx, compiled, execution)

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
func (e *StreamingEngine) executeWorkflowStreaming(ctx context.Context, workflow *CompiledWorkflow, execution *WorkflowExecution) {
	e.executeWorkflowStreamingWithState(ctx, workflow, execution, make(map[string]bool), make(map[string]bool))
}

func (e *StreamingEngine) executeWorkflowStreamingWithState(
	ctx context.Context,
	workflow *CompiledWorkflow,
	execution *WorkflowExecution,
	completedSteps map[string]bool,
	failedSteps map[string]bool,
) {
	// 清理资源
	defer e.cleanupBuffers(execution.ID)
	kernel := newRuntimeKernel(
		e.Engine,
		e,
		workflow,
		execution,
		e.newStepExecutor(),
		completedSteps,
		failedSteps,
		func(ctx context.Context, completed map[string]bool) {
			e.rollbackStreamingWorkflow(ctx, workflow, execution, completed)
		},
	)
	e.Engine.runExecutionLifecycle(ctx, workflow.Source, execution, kernel.Run)
}

// executeStepStreaming 执行流式步骤
func (e *StreamingEngine) executeStepStreaming(ctx context.Context, step *Step, execution *WorkflowExecution) error {
	return e.newStepExecutor().Execute(ctx, &CompiledStep{
		ID:      step.ID,
		Runtime: step,
	}, execution)
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
	e.saveStreamingCheckpoint(execution, step.ID, buffer, result)
	buffer.MarkComplete()

	eventData := map[string]interface{}{
		"step_id":      step.ID,
		"result":       result,
		"total_tokens": buffer.TotalTokens(),
		"total_chunks": buffer.TotalChunks(),
	}
	return e.executionState().CompleteStepWithOptions(execution, step, stepExec, result, StepCompletionOptions{
		EventType: eventType,
		Message:   message,
		EventData: eventData,
	})
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
	e.notifyExecutionContextChange(execution)

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
		return e.failStep(stepExec, execution, apperr.Internal("fallback dependency wait failed").WithCode("workflow_stream_fallback_dependency_wait_failed").WithCause(err))
	}

	renderedParams, err := e.renderStepParameters(execution.Context, step)
	if err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, apperr.InvalidArgument("fallback render failed").WithCode("workflow_stream_fallback_render_failed").WithCause(err))
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
			stepID:            step.ID,
		}
	}

	ag, err := e.resolveStepAgent(step)
	if err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, err)
	}
	if err := validateAgentInputContract(ag, step, taskInput.Parameters); err != nil {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, err)
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
		return e.failStep(stepExec, execution, apperr.Internal("fallback execution failed").WithCode("workflow_stream_fallback_execution_failed").WithCause(err))
	}
	if output != nil && output.Success {
		if err := validateAgentOutputContract(ag, step, output.Result); err != nil {
			buffer.MarkComplete()
			return e.failStep(stepExec, execution, err)
		}
	}
	if !output.Success {
		buffer.MarkComplete()
		return e.failStep(stepExec, execution, apperr.Internalf("fallback step execution failed: %s", output.Error).WithCode("workflow_step_execution_failed"))
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
				return apperr.Conflictf("dependency %s failed", depID).WithCode("workflow_dependency_failed")
			}
		}
		e.mu.RUnlock()

		if allDone {
			return nil
		}

		select {
		case <-ctx.Done():
			return apperr.Wrap(ctx.Err(), apperr.KindInternal, "workflow_dependency_wait_canceled", "dependency wait interrupted")
		case <-ticker.C:
		}
	}
}

func (e *StreamingEngine) seedUpstreamFinalOutput(execution *WorkflowExecution, depID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if output, exists := execution.Context.GetStepOutput(depID); exists && len(output) > 0 {
		return true
	}

	stepExec := execution.StepExecutions[depID]
	if stepExec == nil || stepExec.Status != WorkflowStatusCompleted || len(stepExec.Result) == 0 {
		return false
	}

	execution.Context.SetStepOutput(depID, stepExec.Result)
	return true
}

// subscribeToUpstream 订阅上游流式数据，等待初始数据到达后返回
func (e *StreamingEngine) subscribeToUpstream(ctx context.Context, step *Step, execution *WorkflowExecution, buffer *StreamBuffer) error {
	if len(step.DependsOn) == 0 {
		return nil
	}

	// 为每个依赖创建初始数据通知channel
	initialDataChannels := make(map[string]chan struct{})

	for _, depID := range step.DependsOn {
		initialDataChan := make(chan struct{}, 1)
		initialDataChannels[depID] = initialDataChan

		depBuffer := e.getBuffer(execution.ID, depID)
		if depBuffer == nil {
			if e.seedUpstreamFinalOutput(execution, depID) {
				e.notifyExecutionContextChange(execution)
				initialDataChan <- struct{}{}
			}
			continue
		}

		if depBuffer.TotalChunks() == 0 && depBuffer.IsCompleted() && e.seedUpstreamFinalOutput(execution, depID) {
			e.notifyExecutionContextChange(execution)
			initialDataChan <- struct{}{}
			continue
		}

		// 订阅上游数据
		minTokens := step.Streaming.MinStartTokens
		if minTokens == 0 {
			minTokens = 1 // 至少1个token
		}

		subscriber := depBuffer.Subscribe(step.ID, minTokens)

		// 启动协程处理流式输入
		go func(sub *StreamSubscriber, depStepID string, notifyChan chan struct{}) {
			initialDataReceived := false

			for chunk := range sub.Channel {
				// 上游步骤一旦完成，最终输出会写回 execution.Context。
				// 这里忽略完成后的 replay/尾部 chunk，避免旧的 partial 数据覆盖最终输出。
				e.mu.Lock()
				stepExec := execution.StepExecutions[depStepID]
				stepCompleted := stepExec != nil && stepExec.Status == WorkflowStatusCompleted
				e.mu.Unlock()
				if !stepCompleted {
					execution.Context.MergeStepOutput(depStepID, chunk.Data)
				}

				// 通知等待中的 ContextReader 重新检查字段
				e.notifyExecutionContextChange(execution)

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
			return apperr.Conflictf("timeout waiting for initial data from dependency: %s", depID).WithCode("workflow_stream_dependency_timeout")
		case <-ctx.Done():
			return apperr.Wrap(ctx.Err(), apperr.KindInternal, "workflow_stream_dependency_wait_canceled", "waiting for upstream data interrupted")
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
	mu        *sync.RWMutex
	notifier  *executionNotifier
}

// newExecutionContextReader 创建ContextReader
func (e *StreamingEngine) newExecutionContextReader(execution *WorkflowExecution) agent.ContextReader {
	return &executionContextReader{
		execution: execution,
		mu:        &e.mu,
		notifier:  e.getExecutionNotifier(execution),
	}
}

// GetStepOutput 获取步骤输出（返回副本，线程安全）
func (r *executionContextReader) GetStepOutput(stepID string) (map[string]interface{}, bool) {
	return r.execution.Context.GetStepOutput(stepID)
}

// GetVariable 获取全局变量
func (r *executionContextReader) GetVariable(key string) (interface{}, bool) {
	return r.execution.Context.GetVariable(key)
}

// GetField 获取嵌套字段，支持路径如 "result.summary"
func (r *executionContextReader) GetField(stepID string, fieldPath string) (interface{}, bool) {
	output, exists := r.execution.Context.GetStepOutput(stepID)
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
		output, _ := r.execution.Context.GetStepOutput(stepID)
		r.mu.RLock()
		stepExec := r.execution.StepExecutions[stepID]
		r.mu.RUnlock()

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
			return nil, apperr.NotFoundf("field %s.%s not found (step completed)", stepID, fieldPath).WithCode("workflow_field_not_found")
		}

		if stepExec != nil && stepExec.Status == WorkflowStatusFailed {
			// 步骤失败
			return nil, apperr.Conflictf("step %s failed, field unavailable", stepID).WithCode("workflow_field_unavailable")
		}

		// 检查超时
		if time.Now().After(deadline) {
			return nil, apperr.Conflictf("timeout waiting for field %s.%s", stepID, fieldPath).WithCode("workflow_field_wait_timeout")
		}

		waitTimeout := 100 * time.Millisecond
		if remaining := time.Until(deadline); remaining < waitTimeout {
			waitTimeout = remaining
		}
		if waitTimeout <= 0 {
			return nil, apperr.Conflictf("timeout waiting for field %s.%s", stepID, fieldPath).WithCode("workflow_field_wait_timeout")
		}

		select {
		case <-r.notifier.WaitChan():
			// 收到通知，重新检查
			continue
		case <-time.After(waitTimeout):
			// 超时保护，避免死锁
			continue
		}
	}
}

// IsStepCompleted 检查步骤是否已完成
func (r *executionContextReader) IsStepCompleted(stepID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stepExec, exists := r.execution.StepExecutions[stepID]
	if !exists {
		return false
	}

	return stepExec.Status == WorkflowStatusCompleted
}

// notifyContextChange 通知Context变更（在数据写入后调用）
func (r *executionContextReader) notifyContextChange() {
	if r.notifier != nil {
		r.notifier.Notify()
	}
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
