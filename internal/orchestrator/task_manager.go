package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
)

// TaskRepository defines the interface for task persistence
type TaskRepository interface {
	Create(task *Task) error
	Update(task *Task) error
	GetByID(id string) (*Task, error)
	GetPendingTasks() ([]*Task, error)
	GetRunningTasks() ([]*Task, error)
}

// TaskManager manages task lifecycle and persistence
type TaskManager struct {
	tasks            map[string]*Task    // taskID -> Task (内存缓存)
	agentTasks       map[string][]string // agentName -> []taskID (正在执行的任务列表)
	pendingTasks     []string            // 待执行任务队列
	registry         *agent.Registry     // Agent 注册中心
	repository       TaskRepository      // 持久化存储（可选）
	mu               sync.RWMutex
	maxRetries       int
	retryInterval    time.Duration
	notifyChan       chan struct{} // 通知 Worker 有新任务
	maxPendingTasks  int           // 最大待执行任务数（0 = 无限制）
	maxTasksPerAgent int           // 单个 Agent 最大并发任务数（0 = 无限制）
}

// TaskManagerConfig contains configuration for TaskManager
type TaskManagerConfig struct {
	MaxRetries       int            // 最大重试次数
	RetryInterval    time.Duration  // 重试间隔
	MaxPendingTasks  int            // 最大待执行任务数（0 = 无限制）
	MaxTasksPerAgent int            // 单个 Agent 最大并发任务数（0 = 无限制）
	Repository       TaskRepository // 可选的持久化存储
}

// NewTaskManager creates a new task manager
func NewTaskManager(registry *agent.Registry, config TaskManagerConfig) *TaskManager {
	tm := &TaskManager{
		tasks:            make(map[string]*Task),
		agentTasks:       make(map[string][]string),
		pendingTasks:     make([]string, 0),
		registry:         registry,
		repository:       config.Repository, // 可以为 nil（不持久化）
		maxRetries:       config.MaxRetries,
		retryInterval:    config.RetryInterval,
		maxPendingTasks:  config.MaxPendingTasks,
		maxTasksPerAgent: config.MaxTasksPerAgent,
		notifyChan:       make(chan struct{}, 100), // 缓冲通道
	}

	// 如果配置了持久化，启动时恢复未完成的任务
	if tm.repository != nil {
		if err := tm.RecoverTasks(); err != nil {
			// 记录错误但不中断启动
			fmt.Printf("Warning: failed to recover tasks from database: %v\n", err)
		}
	}

	return tm
}

// RecoverTasks recovers pending and running tasks from database on startup
func (tm *TaskManager) RecoverTasks() error {
	if tm.repository == nil {
		return nil
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// 1. 恢复 pending 任务
	pendingTasks, err := tm.repository.GetPendingTasks()
	if err != nil {
		return fmt.Errorf("failed to recover pending tasks: %w", err)
	}

	for _, task := range pendingTasks {
		tm.tasks[task.ID] = task
		tm.pendingTasks = append(tm.pendingTasks, task.ID)
	}

	// 2. 恢复 running 任务（服务重启时需要重新执行）
	runningTasks, err := tm.repository.GetRunningTasks()
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
			if err := tm.repository.Update(task); err != nil {
				return fmt.Errorf("failed to mark interrupted task as failed: %w", err)
			}
		} else {
			// 更新数据库为 pending 状态
			if err := tm.repository.Update(task); err != nil {
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
func (tm *TaskManager) CreateTask(task *Task) error {
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
		if err := tm.repository.Create(task); err != nil {
			return fmt.Errorf("failed to persist task: %w", err)
		}
	}

	// 2. 存入内存（快速访问）
	tm.tasks[task.ID] = task
	tm.pendingTasks = append(tm.pendingTasks, task.ID)

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
	task.AgentName = selectedAgent.GetName()

	// 标记为运行中
	task.Status = TaskStatusRunning
	now := time.Now()
	task.StartedAt = &now

	// 记录到 Agent 任务列表
	tm.agentTasks[task.AgentName] = append(tm.agentTasks[task.AgentName], task.ID)

	// 从待执行队列中移除
	tm.pendingTasks = tm.pendingTasks[1:]

	return task, nil
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

// selectAgentForTask selects the best agent for a task based on capability and load balancing
func (tm *TaskManager) selectAgentForTask(task *Task) (agent.Agent, error) {
	// 情况1: 任务已经指定了 Agent，直接返回
	if task.AgentName != "" {
		return tm.registry.Get(task.AgentName)
	}

	// 情况2: 根据能力筛选候选 Agent
	var candidateAgents []agent.Agent

	if task.RequiredCapability != "" {
		// 2.1 有明确的能力要求，筛选支持该能力的 Agent
		candidateAgents = tm.registry.FindByCapability(task.RequiredCapability)
		if len(candidateAgents) == 0 {
			return nil, fmt.Errorf("no agent with capability '%s' found", task.RequiredCapability)
		}
	} else {
		// 2.2 没有指定能力，尝试从 Action 推断
		inferredCapability := inferCapabilityFromAction(task.Action)
		if inferredCapability != "" {
			candidateAgents = tm.registry.FindByCapability(inferredCapability)
		}

		// 如果推断失败或没找到，使用所有 Agent
		if len(candidateAgents) == 0 {
			allAgents := tm.registry.GetAll()
			if len(allAgents) == 0 {
				return nil, fmt.Errorf("no agents registered")
			}
			// 转换 map 为 slice
			for _, ag := range allAgents {
				candidateAgents = append(candidateAgents, ag)
			}
		}
	}

	// 情况3: 在候选 Agent 中选择负载最低的（考虑负载上限）
	var selectedAgent agent.Agent
	minLoad := int(^uint(0) >> 1) // MaxInt

	for _, ag := range candidateAgents {
		agentName := ag.GetName()
		currentLoad := len(tm.agentTasks[agentName])

		// 检查是否超过单个 Agent 的负载上限
		if tm.maxTasksPerAgent > 0 && currentLoad >= tm.maxTasksPerAgent {
			// 该 Agent 已满载，跳过
			continue
		}

		if currentLoad < minLoad {
			minLoad = currentLoad
			selectedAgent = ag
		}
	}

	if selectedAgent == nil {
		// 如果有负载限制，可能所有 Agent 都满载了
		if tm.maxTasksPerAgent > 0 {
			return nil, fmt.Errorf("all agents are at max capacity (%d tasks each)", tm.maxTasksPerAgent)
		}
		return nil, fmt.Errorf("no suitable agent found")
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
		"summarize": "text-generation",
		"generate":  "text-generation",
		"analyze":   "text-analysis",
		"chat":      "conversation",
		"qa":        "question-answering",

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
func (tm *TaskManager) GetTask(taskID string) (*Task, error) {
	tm.mu.RLock()
	task, exists := tm.tasks[taskID]
	tm.mu.RUnlock()

	// 1. 先从内存中查找（快速）
	if exists {
		return task, nil
	}

	// 2. 内存中没有，从数据库中查找（降级）
	if tm.repository != nil {
		task, err := tm.repository.GetByID(taskID)
		if err == nil {
			// 找到了，加载到内存中
			tm.mu.Lock()
			tm.tasks[taskID] = task
			tm.mu.Unlock()
			return task, nil
		}
	}

	return nil, fmt.Errorf("task %s not found", taskID)
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
func (tm *TaskManager) MarkTaskAsCompleted(taskID, agentName string, result map[string]interface{}) error {
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
		if err := tm.repository.Update(task); err != nil {
			return fmt.Errorf("failed to update task in database: %w", err)
		}
	}

	// 2. 从 Agent 的任务列表中移除
	tm.removeAgentTask(agentName, taskID)

	return nil
}

// MarkTaskAsFailed marks a task as failed and optionally retries it
func (tm *TaskManager) MarkTaskAsFailed(taskID, agentName string, errMsg string) error {
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

		// 更新数据库（重试状态）
		if tm.repository != nil {
			if err := tm.repository.Update(task); err != nil {
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
		if err := tm.repository.Update(task); err != nil {
			return fmt.Errorf("failed to update failed task: %w", err)
		}
	}

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

// 标记任务为取消状态
func (tm *TaskManager) CancelTask(taskID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	if task.Status == TaskStatusCompleted || task.Status == TaskStatusFailed {
		return fmt.Errorf("task %s already finished", taskID)
	}

	task.Status = TaskStatusCancelled
	now := time.Now()
	task.CompletedAt = &now

	// 从待执行队列中移除
	tm.removePendingTask(taskID)

	// 从 Agent 任务列表中移除
	for agentName := range tm.agentTasks {
		tm.removeAgentTask(agentName, taskID)
	}

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
