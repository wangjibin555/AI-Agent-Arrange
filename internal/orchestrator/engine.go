package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wepie/ai-agent-arrange/internal/agent"
	"github.com/wepie/ai-agent-arrange/pkg/logger"
	"go.uber.org/zap"
)

// Engine is the core orchestration engine
type Engine struct {
	agentRegistry *agent.Registry
	taskManager   *TaskManager
	healthChecker *HealthChecker
	workers       []*Worker
	workerWg      sync.WaitGroup // Wait for workers to finish
	workerCtx     context.Context
	workerCancel  context.CancelFunc
	mu            sync.RWMutex
	running       bool
}

// NewEngine creates a new orchestration engine
func NewEngine(registry *agent.Registry, maxWorkers int) *Engine {
	// Create task manager with default configuration
	taskManagerConfig := TaskManagerConfig{
		MaxRetries:       3, // 最大重试3次
		RetryInterval:    5 * time.Second,
		MaxPendingTasks:  10000, // 最多待执行10000个任务
		MaxTasksPerAgent: 100,   // 单个Agent最多100个并发任务
	}
	taskManager := NewTaskManager(registry, taskManagerConfig)

	// Create health checker that checks every 30 seconds with 5 second timeout
	healthChecker := NewHealthChecker(registry, taskManager, 30*time.Second, 5*time.Second)

	return &Engine{
		agentRegistry: registry,
		taskManager:   taskManager,
		healthChecker: healthChecker,
		workers:       make([]*Worker, maxWorkers),
	}
}

// Start starts the orchestration engine
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("engine already running")
	}
	e.running = true
	e.mu.Unlock()

	logger.Info("Starting orchestration engine",
		zap.Int("worker_count", len(e.workers)),
	)

	// Create worker context
	e.workerCtx, e.workerCancel = context.WithCancel(ctx)

	// Start health checker
	e.healthChecker.Start(e.workerCtx)

	// Start workers
	for i := 0; i < len(e.workers); i++ {
		worker := NewWorker(i, e.agentRegistry, e.taskManager)
		e.workers[i] = worker

		e.workerWg.Add(1)
		go func(w *Worker) {
			defer e.workerWg.Done()
			w.Start(e.workerCtx)
		}(worker)
	}

	logger.Info("Orchestration engine started successfully")
	return nil
}

// Stop stops the orchestration engine gracefully
func (e *Engine) Stop() error {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return fmt.Errorf("engine not running")
	}
	e.running = false
	e.mu.Unlock()

	logger.Info("Stopping orchestration engine gracefully...")

	// Step 1: Cancel worker context to signal shutdown
	if e.workerCancel != nil {
		logger.Info("Signaling workers to stop")
		e.workerCancel()
	}

	// Step 2: Wait for all workers to finish their current tasks
	logger.Info("Waiting for workers to finish current tasks...")
	workerDone := make(chan struct{})
	go func() {
		e.workerWg.Wait()
		close(workerDone)
	}()

	// Wait with timeout (30 seconds)
	select {
	case <-workerDone:
		logger.Info("All workers stopped gracefully")
	case <-time.After(30 * time.Second):
		logger.Warn("Workers did not stop within timeout, forcing shutdown")
	}

	// Step 3: Stop health checker
	logger.Info("Stopping health checker")
	e.healthChecker.Stop()

	logger.Info("Orchestration engine stopped successfully")
	return nil
}

// SubmitTask submits a new task for execution
func (e *Engine) SubmitTask(task *Task) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.running {
		return fmt.Errorf("engine not running")
	}

	// Validate task (如果指定了 AgentName，检查是否存在)
	if task.AgentName != "" {
		if _, err := e.agentRegistry.Get(task.AgentName); err != nil {
			return fmt.Errorf("agent not found: %w", err)
		}
	}

	// Save task to task manager (会自动通知 Worker)
	if err := e.taskManager.CreateTask(task); err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	return nil
}

// GetStatus returns the current engine status
func (e *Engine) GetStatus() *EngineStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := e.taskManager.GetTaskStats()

	return &EngineStatus{
		Running:     e.running,
		QueueLength: stats.StatusCounts[TaskStatusPending], // 使用 TaskManager 的待执行任务数
		WorkerCount: len(e.workers),
	}
}

// SelectLeastLoadedAgent selects the agent with the least load for a given capability
func (e *Engine) SelectLeastLoadedAgent(capability string) (agent.Agent, error) {
	// 获取所有具有该能力的 Agent
	agents := e.agentRegistry.FindByCapability(capability)
	if len(agents) == 0 {
		return nil, fmt.Errorf("no agent with capability %s found", capability)
	}

	// 选择负载最低的
	var selectedAgent agent.Agent
	minLoad := int(^uint(0) >> 1) // MaxInt

	for _, ag := range agents {
		load := e.taskManager.GetAgentLoad(ag.GetName())
		if load < minLoad {
			minLoad = load
			selectedAgent = ag
		}
	}

	return selectedAgent, nil
}

// GetAgentLoad returns the current load for a specific agent
func (e *Engine) GetAgentLoad(agentName string) int {
	return e.taskManager.GetAgentLoad(agentName)
}

// GetLoadStats returns load statistics from TaskManager
func (e *Engine) GetLoadStats() TaskStats {
	return e.taskManager.GetTaskStats()
}

// GetTaskLimits returns task capacity limits
func (e *Engine) GetTaskLimits() TaskLimits {
	return e.taskManager.GetTaskLimits()
}

// GetTaskUsage returns task system usage statistics
func (e *Engine) GetTaskUsage() TaskUsage {
	return e.taskManager.GetTaskUsage()
}

// GetTaskManager returns the task manager
func (e *Engine) GetTaskManager() *TaskManager {
	return e.taskManager
}

// GetHealthChecker returns the health checker
func (e *Engine) GetHealthChecker() *HealthChecker {
	return e.healthChecker
}

// GetTaskStats returns task statistics
func (e *Engine) GetTaskStats() TaskStats {
	return e.taskManager.GetTaskStats()
}

// GetTask retrieves a task by ID
func (e *Engine) GetTask(taskID string) (*Task, error) {
	return e.taskManager.GetTask(taskID)
}

// EngineStatus represents the current status of the engine
type EngineStatus struct {
	Running     bool `json:"running"`
	QueueLength int  `json:"queue_length"`
	WorkerCount int  `json:"worker_count"`
}
