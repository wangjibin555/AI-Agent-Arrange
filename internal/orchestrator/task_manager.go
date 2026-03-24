package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// TaskRepository defines the interface for task persistence
type TaskRepository interface {
	Create(ctx context.Context, task *Task) error
	Update(ctx context.Context, task *Task) error
	GetByID(ctx context.Context, id string) (*Task, error)
	GetPendingTasks(ctx context.Context) ([]*Task, error)
	GetRunningTasks(ctx context.Context) ([]*Task, error)
}

// TaskEventPublisher defines the interface for publishing task events
type TaskEventPublisher interface {
	PublishTaskEvent(taskID string, eventType string, status string, message string, result map[string]interface{}, errorMsg string)
}

// TaskManager manages task lifecycle and persistence
type TaskManager struct {
	tasks            map[string]*Task    // taskID -> Task (内存缓存)
	agentTasks       map[string][]string // agentName -> []taskID (正在执行的任务列表)
	pendingTasks     []string            // 待执行任务队列
	registry         *agent.Registry     // Agent 注册中心
	repository       TaskRepository      // 持久化存储（可选）
	eventPublisher   TaskEventPublisher  // 事件发布器（可选）
	mu               sync.RWMutex
	maxRetries       int
	retryInterval    time.Duration
	notifyChan       chan struct{} // 通知 Worker 有新任务
	maxPendingTasks  int           // 最大待执行任务数（0 = 无限制）
	maxTasksPerAgent int           // 单个 Agent 最大并发任务数（0 = 无限制）
	metrics          *monitor.TaskMetrics
}

// TaskManagerConfig contains configuration for TaskManager
type TaskManagerConfig struct {
	MaxRetries       int                // 最大重试次数
	RetryInterval    time.Duration      // 重试间隔
	MaxPendingTasks  int                // 最大待执行任务数（0 = 无限制）
	MaxTasksPerAgent int                // 单个 Agent 最大并发任务数（0 = 无限制）
	Repository       TaskRepository     // 可选的持久化存储
	EventPublisher   TaskEventPublisher // 可选的事件发布器
}

// NewTaskManager creates a new task manager
func NewTaskManager(registry *agent.Registry, config TaskManagerConfig) *TaskManager {
	tm := &TaskManager{
		tasks:            make(map[string]*Task),
		agentTasks:       make(map[string][]string),
		pendingTasks:     make([]string, 0),
		registry:         registry,
		repository:       config.Repository,     // 可以为 nil（不持久化）
		eventPublisher:   config.EventPublisher, // 可以为 nil（不发布事件）
		maxRetries:       config.MaxRetries,
		retryInterval:    config.RetryInterval,
		maxPendingTasks:  config.MaxPendingTasks,
		maxTasksPerAgent: config.MaxTasksPerAgent,
		notifyChan:       make(chan struct{}, 100), // 缓冲通道
	}

	// 如果配置了持久化，启动时恢复未完成的任务
	if tm.repository != nil {
		if err := tm.RecoverTasks(context.Background()); err != nil {
			// 记录错误但不中断启动
			fmt.Printf("Warning: failed to recover tasks from database: %v\n", err)
		}
	}

	return tm
}

func (tm *TaskManager) SetMetrics(metrics *monitor.TaskMetrics) {
	tm.metrics = metrics
	tm.observeQueueLengthLocked()
	tm.observeAgentLoadsLocked()
}

// RecoverTasks recovers pending and running tasks from database on startup
func (tm *TaskManager) RecoverTasks(ctx context.Context) error {
	if tm.repository == nil {
		return nil
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// 1. 恢复 pending 任务
	pendingTasks, err := tm.repository.GetPendingTasks(ctx)
	if err != nil {
		return fmt.Errorf("failed to recover pending tasks: %w", err)
	}

	for _, task := range pendingTasks {
		tm.tasks[task.ID] = task
		tm.pendingTasks = append(tm.pendingTasks, task.ID)
	}

	// 2. 恢复 running 任务（服务重启时需要重新执行）
	runningTasks, err := tm.repository.GetRunningTasks(ctx)
	if err != nil {
		return fmt.Errorf("failed to recover running tasks: %w", err)
	}

	for _, task := range runningTasks {
		// 将运行中的任务重置为 pending 状态（因为服务重启了）
		task.Status = TaskStatusPending
		task.StartedAt = nil
		// 增加重试计数
		*task.RetryCount++

		// 检查是否超过最大重试次数
		if *task.RetryCount >= tm.maxRetries {
			task.Status = TaskStatusFailed
			task.Error = "task interrupted by server restart, max retries exceeded"
			now := time.Now()
			task.CompletedAt = &now
			// 更新数据库
			if err := tm.repository.Update(ctx, task); err != nil {
				return fmt.Errorf("failed to mark interrupted task as failed: %w", err)
			}
		} else {
			// 更新数据库为 pending 状态
			if err := tm.repository.Update(ctx, task); err != nil {
				return fmt.Errorf("failed to reset running task to pending: %w", err)
			}
			// 加入待执行队列
			tm.tasks[task.ID] = task
			tm.pendingTasks = append(tm.pendingTasks, task.ID)
		}
	}

	recoveredCount := len(pendingTasks) + len(runningTasks)
	if recoveredCount > 0 {
		fmt.Printf("Recovered %d tasks from database (%d pending, %d running)\n",
			recoveredCount, len(pendingTasks), len(runningTasks))
	}

	return nil
}

// CreateTask creates and stores a new task
func (tm *TaskManager) CreateTask(ctx context.Context, task *Task) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, exists := tm.tasks[task.ID]; exists {
		return fmt.Errorf("task %s already exists", task.ID)
	}

	// 检查队列大小限制
	if tm.maxPendingTasks > 0 && len(tm.pendingTasks) >= tm.maxPendingTasks {
		return fmt.Errorf("task queue full: %d/%d tasks pending", len(tm.pendingTasks), tm.maxPendingTasks)
	}

	// 初始化重试计数
	if task.RetryCount == nil {
		retryCount := 0
		task.RetryCount = &retryCount
	}

	task.Status = TaskStatusPending
	task.CreatedAt = time.Now()

	// 1. 持久化到数据库（如果配置了 repository）
	if tm.repository != nil {
		if err := tm.repository.Create(ctx, task); err != nil {
			return fmt.Errorf("failed to persist task: %w", err)
		}
	}

	// 2. 存入内存（快速访问）
	tm.tasks[task.ID] = task
	tm.pendingTasks = append(tm.pendingTasks, task.ID)
	tm.observeTaskCreated(task)
	tm.observeQueueLengthLocked()

	// 通知 Worker 有新任务
	select {
	case tm.notifyChan <- struct{}{}:
	default:
		// 通道满了，Worker 会定期检查
	}

	return nil
}

// 这是 Worker 调用的核心方法：自动选择任务和 Agent
func (tm *TaskManager) PullNextTask(ctx context.Context) (*Task, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// 没有待执行任务
	if len(tm.pendingTasks) == 0 {
		return nil, fmt.Errorf("no pending tasks")
	}

	// 获取第一个待执行任务（可以扩展为优先级队列）
	taskID := tm.pendingTasks[0]
	task, exists := tm.tasks[taskID]
	if !exists {
		// 任务不存在，从队列中移除
		tm.pendingTasks = tm.pendingTasks[1:]
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// 根据任务需要的能力，选择负载最低的 Agent
	selectedAgent, err := tm.selectAgentForTask(task)
	if err != nil {
		// 没有可用的 Agent，任务留在队列中
		return nil, err
	}

	// 分配 Agent
	previousAgentName := task.AgentName
	task.AgentName = selectedAgent.GetName()

	// 标记为运行中
	previousStatus := task.Status
	previousStartedAt := task.StartedAt
	task.Status = TaskStatusRunning
	now := time.Now()
	task.StartedAt = &now

	if tm.repository != nil {
		if err := tm.repository.Update(ctx, cloneTask(task)); err != nil {
			task.AgentName = previousAgentName
			task.Status = previousStatus
			task.StartedAt = previousStartedAt
			return nil, fmt.Errorf("failed to persist running task: %w", err)
		}
	}

	tm.observeTaskStarted(task)

	// 发布任务开始事件
	if tm.eventPublisher != nil {
		tm.eventPublisher.PublishTaskEvent(
			task.ID,
			"status_changed",
			string(TaskStatusRunning),
			fmt.Sprintf("Task started on agent %s", task.AgentName),
			nil,
			"",
		)
	}

	// 记录到 Agent 任务列表
	tm.agentTasks[task.AgentName] = append(tm.agentTasks[task.AgentName], task.ID)

	// 从待执行队列中移除
	tm.pendingTasks = tm.pendingTasks[1:]
	tm.observeQueueLengthLocked()
	tm.observeAgentLoadLocked(task.AgentName)

	return task, nil
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}

	cloned := *task
	if task.Parameters != nil {
		cloned.Parameters = make(map[string]interface{}, len(task.Parameters))
		for key, value := range task.Parameters {
			cloned.Parameters[key] = value
		}
	}
	if task.RequestMetadata != nil {
		cloned.RequestMetadata = make(map[string]string, len(task.RequestMetadata))
		for key, value := range task.RequestMetadata {
			cloned.RequestMetadata[key] = value
		}
	}
	if task.Dependencies != nil {
		cloned.Dependencies = append([]string(nil), task.Dependencies...)
	}
	if task.Result != nil {
		cloned.Result = make(map[string]interface{}, len(task.Result))
		for key, value := range task.Result {
			cloned.Result[key] = value
		}
	}
	if task.RetryCount != nil {
		retryCount := *task.RetryCount
		cloned.RetryCount = &retryCount
	}

	return &cloned
}

// WaitForTask blocks until a task is available or context is cancelled
func (tm *TaskManager) WaitForTask(ctx context.Context) error {
	// 先检查是否有任务
	tm.mu.RLock()
	hasTasks := len(tm.pendingTasks) > 0
	tm.mu.RUnlock()

	if hasTasks {
		return nil
	}

	// 等待新任务通知或超时
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.notifyChan:
		return nil
	case <-time.After(1 * time.Second):
		// 定期超时，让 Worker 可以检查其他条件
		return nil
	}
}

// GetRegistry returns the underlying agent registry.
func (tm *TaskManager) GetRegistry() *agent.Registry {
	return tm.registry
}

// selectAgentForTask selects the best agent for a task based on capability and load balancing
func (tm *TaskManager) selectAgentForTask(task *Task) (agent.Agent, error) {
	resolver := agent.NewResolver(tm.registry)
	return resolver.Resolve(agent.ResolveRequest{
		AgentName:          task.AgentName,
		Capability:         task.RequiredCapability,
		FallbackCapability: inferCapabilityFromAction(task.Action),
	}, agent.ResolveHooks{
		OnCapabilityNotFound: func(capability string) error {
			return apperr.NotFoundf("no agent with capability '%s' found", capability).WithCode("agent_capability_not_found")
		},
		OnRegistryEmpty: func() error {
			return apperr.NotFound("no agents registered").WithCode("agent_registry_empty")
		},
		SelectCandidate: tm.selectLeastLoadedCandidate,
	})
}

func (tm *TaskManager) selectLeastLoadedCandidate(candidateAgents []agent.Agent) (agent.Agent, error) {
	var selectedAgent agent.Agent
	minLoad := int(^uint(0) >> 1)

	for _, ag := range candidateAgents {
		agentName := ag.GetName()
		currentLoad := len(tm.agentTasks[agentName])
		if tm.maxTasksPerAgent > 0 && currentLoad >= tm.maxTasksPerAgent {
			continue
		}
		if currentLoad < minLoad {
			minLoad = currentLoad
			selectedAgent = ag
		}
	}

	if selectedAgent == nil {
		if tm.maxTasksPerAgent > 0 {
			return nil, apperr.Conflict(fmt.Sprintf("all agents are at max capacity (%d tasks each)", tm.maxTasksPerAgent)).WithCode("agent_capacity_exhausted")
		}
		return nil, apperr.NotFound("no suitable agent found").WithCode("agent_not_available")
	}

	return selectedAgent, nil
}

// inferCapabilityFromAction infers the required capability from the action
// 从 Action 推断需要的能力
func inferCapabilityFromAction(action string) string {
	// Action → Capability 映射表
	actionCapabilityMap := map[string]string{
		// 文本处理
		"translate": "translation",
		"summarize": "summarization",
		"generate":  "text-generation",
		"analyze":   "text-analysis",
		"chat":      "conversation",
		"qa":        "question-answering",
		"reason":    "complex-reasoning",
		"plan":      "complex-reasoning",
		"research":  "web-search",

		// 数据操作
		"query":  "database",
		"sql":    "sql-query",
		"insert": "database",
		"update": "database",
		"delete": "database",

		// 工具调用
		"search":  "web-search",
		"fetch":   "http-request",
		"webhook": "webhook",

		// 测试
		"echo":    "text-processing",
		"ping":    "health-check",
		"process": "text-processing",
	}

	capability, exists := actionCapabilityMap[action]
	if exists {
		return capability
	}

	// 找不到映射，返回空字符串（使用所有 Agent）
	return ""
}

// GetAgentLoad returns the current load of an agent
func (tm *TaskManager) GetAgentLoad(agentName string) int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	return len(tm.agentTasks[agentName])
}

// GetTask retrieves a task by ID (memory first, then database)
func (tm *TaskManager) GetTask(ctx context.Context, taskID string) (*Task, error) {
	tm.mu.RLock()
	task, exists := tm.tasks[taskID]
	tm.mu.RUnlock()

	// 1. 先从内存中查找（快速）
	if exists {
		return task, nil
	}

	// 2. 内存中没有，从数据库中查找（降级）
	if tm.repository != nil {
		task, err := tm.repository.GetByID(ctx, taskID)
		if err == nil {
			// 找到了，加载到内存中
			tm.mu.Lock()
			tm.tasks[taskID] = task
			tm.mu.Unlock()
			return task, nil
		}
	}

	return nil, apperr.NotFoundf("task %s not found", taskID).WithCode("task_not_found")
}

// UpdateTaskStatus updates the status of a task
func (tm *TaskManager) UpdateTaskStatus(taskID string, status TaskStatus, result map[string]interface{}, err string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Status = status
	task.Result = result
	task.Error = err

	now := time.Now()
	switch status {
	case TaskStatusRunning:
		task.StartedAt = &now
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
		task.CompletedAt = &now
	}

	return nil
}

// 标记任务正在运行
func (tm *TaskManager) MarkTaskAsRunning(taskID, agentName string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Status = TaskStatusRunning
	now := time.Now()
	task.StartedAt = &now

	// 记录 Agent 正在执行的任务
	tm.agentTasks[agentName] = append(tm.agentTasks[agentName], taskID)

	// 从待执行队列中移除
	tm.removePendingTask(taskID)

	return nil
}

// MarkTaskAsCompleted marks a task as completed
func (tm *TaskManager) MarkTaskAsCompleted(ctx context.Context, taskID, agentName string, result map[string]interface{}) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Status = TaskStatusCompleted
	task.Result = result
	now := time.Now()
	task.CompletedAt = &now

	// 1. 更新数据库
	if tm.repository != nil {
		if err := tm.repository.Update(ctx, task); err != nil {
			return fmt.Errorf("failed to update task in database: %w", err)
		}
	}

	// 2. 发布完成事件
	if tm.eventPublisher != nil {
		tm.eventPublisher.PublishTaskEvent(
			taskID,
			"completed",
			string(TaskStatusCompleted),
			"Task completed successfully",
			result,
			"",
		)
	}

	// 3. 从 Agent 的任务列表中移除
	tm.removeAgentTask(agentName, taskID)
	tm.observeAgentLoadLocked(agentName)

	return nil
}

// MarkTaskAsFailed marks a task as failed and optionally retries it
func (tm *TaskManager) MarkTaskAsFailed(ctx context.Context, taskID, agentName string, errMsg string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Error = errMsg
	*task.RetryCount++

	// 从 Agent 的任务列表中移除
	tm.removeAgentTask(agentName, taskID)

	// 判断是否需要重试
	if *task.RetryCount < tm.maxRetries {
		// 重新加入待执行队列
		task.Status = TaskStatusPending
		task.StartedAt = nil
		tm.pendingTasks = append(tm.pendingTasks, taskID)
		tm.observeTaskRetry(task, "execution_failed")
		tm.observeQueueLengthLocked()

		// 更新数据库（重试状态）
		if tm.repository != nil {
			if err := tm.repository.Update(ctx, task); err != nil {
				return fmt.Errorf("failed to update task for retry: %w", err)
			}
		}
		return nil
	}

	// 超过最大重试次数，标记为失败
	task.Status = TaskStatusFailed
	now := time.Now()
	task.CompletedAt = &now

	// 更新数据库（最终失败状态）
	if tm.repository != nil {
		if err := tm.repository.Update(ctx, task); err != nil {
			return fmt.Errorf("failed to update failed task: %w", err)
		}
	}

	// 发布失败事件
	if tm.eventPublisher != nil {
		tm.eventPublisher.PublishTaskEvent(
			taskID,
			"failed",
			string(TaskStatusFailed),
			fmt.Sprintf("Task failed after %d retries", *task.RetryCount),
			nil,
			errMsg,
		)
	}
	tm.observeQueueLengthLocked()
	tm.observeAgentLoadLocked(agentName)

	return nil
}

// GetAgentTasks returns all tasks currently being executed by an agent
func (tm *TaskManager) GetAgentTasks(agentName string) []*Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	taskIDs := tm.agentTasks[agentName]
	tasks := make([]*Task, 0, len(taskIDs))

	for _, taskID := range taskIDs {
		if task, exists := tm.tasks[taskID]; exists {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// ReassignAgentTasks reassigns all tasks from a failed agent to other agents
// 这是你问题的核心：Agent 挂了，重新分配任务
func (tm *TaskManager) ReassignAgentTasks(failedAgentName string) []string {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	taskIDs := tm.agentTasks[failedAgentName]
	if len(taskIDs) == 0 {
		return nil
	}

	reassignedTasks := make([]string, 0, len(taskIDs))

	for _, taskID := range taskIDs {
		task, exists := tm.tasks[taskID]
		if !exists {
			continue
		}

		// 重置任务状态，重新加入待执行队列
		task.Status = TaskStatusPending
		task.StartedAt = nil
		*task.RetryCount++

		// 检查是否超过最大重试次数
		if *task.RetryCount >= tm.maxRetries {
			task.Status = TaskStatusFailed
			task.Error = fmt.Sprintf("max retries exceeded after agent %s failure", failedAgentName)
			now := time.Now()
			task.CompletedAt = &now
			continue
		}

		tm.pendingTasks = append(tm.pendingTasks, taskID)
		reassignedTasks = append(reassignedTasks, taskID)
	}

	// 清空该 Agent 的任务列表
	delete(tm.agentTasks, failedAgentName)

	return reassignedTasks
}

// GetPendingTasks returns all pending tasks
func (tm *TaskManager) GetPendingTasks() []*Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tasks := make([]*Task, 0, len(tm.pendingTasks))
	for _, taskID := range tm.pendingTasks {
		if task, exists := tm.tasks[taskID]; exists && task.Status == TaskStatusPending {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// GetTasksByStatus returns all tasks with a specific status
func (tm *TaskManager) GetTasksByStatus(status TaskStatus) []*Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tasks := make([]*Task, 0)
	for _, task := range tm.tasks {
		if task.Status == status {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// GetTaskStats 获取任务统计信息
func (tm *TaskManager) GetTaskStats() TaskStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats := TaskStats{
		Total:           len(tm.tasks),
		StatusCounts:    make(map[TaskStatus]int),
		AgentTaskCounts: make(map[string]int),
	}

	// 统计各状态的任务数量（自动包含所有状态）
	for _, task := range tm.tasks {
		stats.StatusCounts[task.Status]++
	}

	// 统计各Agent的任务数量
	for agentName, taskIDs := range tm.agentTasks {
		stats.AgentTaskCounts[agentName] = len(taskIDs)
	}

	return stats
}

// GetTaskLimits 获取任务管理器的容量限制配置
func (tm *TaskManager) GetTaskLimits() TaskLimits {
	return TaskLimits{
		MaxPendingTasks:  tm.maxPendingTasks,
		MaxTasksPerAgent: tm.maxTasksPerAgent,
	}
}

// GetTaskUsage 获取任务系统使用率
func (tm *TaskManager) GetTaskUsage() TaskUsage {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	usage := TaskUsage{
		PendingTasks: len(tm.pendingTasks),
	}

	// 计算队列使用率
	if tm.maxPendingTasks > 0 {
		usage.QueueUsage = float64(len(tm.pendingTasks)) / float64(tm.maxPendingTasks)
	} else {
		usage.QueueUsage = 0 // 无限制时返回0
	}

	return usage
}

// 用于检测"卡住"的任务
func (tm *TaskManager) GetStaleTasks(timeout time.Duration) []*Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	staleTasks := make([]*Task, 0)
	now := time.Now()

	for _, task := range tm.tasks {
		if task.Status == TaskStatusRunning && task.StartedAt != nil {
			elapsed := now.Sub(*task.StartedAt)
			if elapsed > timeout {
				staleTasks = append(staleTasks, task)
			}
		}
	}

	return staleTasks
}

// CancelTask marks a task as cancelled
func (tm *TaskManager) CancelTask(ctx context.Context, taskID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return apperr.NotFoundf("task %s not found", taskID).WithCode("task_not_found")
	}

	if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		return apperr.Conflictf("Cannot cancel task in '%s' status", task.Status).WithCode("task_cancel_conflict")
	}

	task.Status = TaskStatusCancelled
	now := time.Now()
	task.CompletedAt = &now

	// 更新数据库
	if tm.repository != nil {
		if err := tm.repository.Update(ctx, task); err != nil {
			return fmt.Errorf("failed to update cancelled task: %w", err)
		}
	}

	// 发布取消事件
	if tm.eventPublisher != nil {
		tm.eventPublisher.PublishTaskEvent(
			taskID,
			"cancelled",
			string(TaskStatusCancelled),
			"Task cancelled by user",
			nil,
			"",
		)
	}

	// 从待执行队列中移除
	tm.removePendingTask(taskID)

	// 从 Agent 任务列表中移除
	for agentName := range tm.agentTasks {
		tm.removeAgentTask(agentName, taskID)
	}
	tm.observeQueueLengthLocked()
	tm.observeAgentLoadsLocked()

	return nil
}

// 移除待执行任务
func (tm *TaskManager) removePendingTask(taskID string) {
	for i, id := range tm.pendingTasks {
		if id == taskID {
			tm.pendingTasks = append(tm.pendingTasks[:i], tm.pendingTasks[i+1:]...)
			break
		}
	}
}

// 删除对应agent中的任务
func (tm *TaskManager) removeAgentTask(agentName, taskID string) {
	tasks := tm.agentTasks[agentName]
	for i, id := range tasks {
		if id == taskID {
			tm.agentTasks[agentName] = append(tasks[:i], tasks[i+1:]...)
			break
		}
	}

	// 如果该 Agent 没有任务了，删除这个 key
	if len(tm.agentTasks[agentName]) == 0 {
		delete(tm.agentTasks, agentName)
	}
}

func (tm *TaskManager) observeTaskCreated(task *Task) {
	if tm.metrics == nil || task == nil {
		return
	}
	tm.metrics.ObserveTaskCreated(task.AgentName, task.RequiredCapability, task.Action)
}

func (tm *TaskManager) observeTaskStarted(task *Task) {
	if tm.metrics == nil || task == nil {
		return
	}
	tm.metrics.ObserveTaskStarted(task.AgentName, task.Action)
}

func (tm *TaskManager) observeTaskRetry(task *Task, reason string) {
	if tm.metrics == nil || task == nil {
		return
	}
	tm.metrics.ObserveTaskRetry(task.AgentName, reason)
}

func (tm *TaskManager) observeQueueLengthLocked() {
	if tm.metrics == nil {
		return
	}
	tm.metrics.ObserveQueueLength(len(tm.pendingTasks))
}

func (tm *TaskManager) observeAgentLoadLocked(agentName string) {
	if tm.metrics == nil || agentName == "" {
		return
	}
	tm.metrics.ObserveAgentLoad(agentName, len(tm.agentTasks[agentName]))
}

func (tm *TaskManager) observeAgentLoadsLocked() {
	if tm.metrics == nil {
		return
	}
	for agentName, taskIDs := range tm.agentTasks {
		tm.metrics.ObserveAgentLoad(agentName, len(taskIDs))
	}
}

// GetEventPublisher returns the event publisher for streaming events
func (tm *TaskManager) GetEventPublisher() TaskEventPublisher {
	return tm.eventPublisher
}

// TaskStats 当前任务统计信息（只包含统计数据）
type TaskStats struct {
	Total           int                `json:"total"`
	StatusCounts    map[TaskStatus]int `json:"status_counts"`     // 各状态的任务数量
	AgentTaskCounts map[string]int     `json:"agent_task_counts"` // agent -> 当前执行任务数
}

// TaskLimits 任务管理器的容量限制配置
type TaskLimits struct {
	MaxPendingTasks  int `json:"max_pending_tasks"`   // 最大待执行任务数（0=无限制）
	MaxTasksPerAgent int `json:"max_tasks_per_agent"` // 单个Agent最大任务数（0=无限制）
}

// TaskUsage 任务系统使用率统计
type TaskUsage struct {
	PendingTasks int     `json:"pending_tasks"` // 当前待执行任务数
	QueueUsage   float64 `json:"queue_usage"`   // 队列使用率（0-1，0表示无限制）
}
