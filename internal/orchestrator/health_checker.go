package orchestrator

import (
	"context"
	"sync"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// HealthChecker monitors agent health and triggers task reassignment
type HealthChecker struct {
	registry    *agent.Registry
	taskManager *TaskManager
	interval    time.Duration
	timeout     time.Duration
	stopChan    chan struct{}
	wg          sync.WaitGroup
	mu          sync.RWMutex
	agentStatus map[string]AgentHealthStatus
}

// AgentHealthStatus represents the health status of an agent
type AgentHealthStatus struct {
	Name            string    `json:"name"`
	Healthy         bool      `json:"healthy"`
	LastCheckTime   time.Time `json:"last_check_time"`
	LastHealthyTime time.Time `json:"last_healthy_time"`
	FailureCount    int       `json:"failure_count"`
	ErrorMessage    string    `json:"error_message,omitempty"`
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(registry *agent.Registry, taskManager *TaskManager, interval, timeout time.Duration) *HealthChecker {
	return &HealthChecker{
		registry:    registry,
		taskManager: taskManager,
		interval:    interval,
		timeout:     timeout,
		stopChan:    make(chan struct{}),
		agentStatus: make(map[string]AgentHealthStatus),
	}
}

// Start starts the health checker
func (hc *HealthChecker) Start(ctx context.Context) {
	hc.wg.Add(1)
	go hc.run(ctx)
	logger.Info("Health checker started", zap.Duration("interval", hc.interval))
}

// Stop stops the health checker
func (hc *HealthChecker) Stop() {
	close(hc.stopChan)
	hc.wg.Wait()
	logger.Info("Health checker stopped")
}

// run is the main health check loop
func (hc *HealthChecker) run(ctx context.Context) {
	defer hc.wg.Done()

	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-hc.stopChan:
			return
		case <-ticker.C:
			hc.checkAllAgents(ctx)
		}
	}
}

// checkAllAgents checks the health of all registered agents
func (hc *HealthChecker) checkAllAgents(ctx context.Context) {
	agents := hc.registry.GetAll()

	for name, ag := range agents {
		go hc.checkAgent(ctx, name, ag)
	}
}

// checkAgent checks the health of a single agent
func (hc *HealthChecker) checkAgent(ctx context.Context, name string, ag agent.Agent) {
	checkCtx, cancel := context.WithTimeout(ctx, hc.timeout)
	defer cancel()

	// 只有显式声明 health-check 能力的 Agent 才执行主动探活。
	// 像 OpenAI / DeepSeek 这类 LLM Agent 不支持 ping，并且通常要求 prompt，
	// 这里如果硬发 ping 只会产生误判，进而触发错误的任务迁移。
	if !supportsHealthCheck(ag) {
		hc.markAgentHealthy(name)
		return
	}

	// 执行健康检查（发送一个简单的测试任务）
	input := &agent.TaskInput{
		TaskID: "health-check-" + name,
		Action: "ping", // 假设所有 Agent 都支持 ping
		Parameters: map[string]interface{}{
			"health_check": true,
		},
	}

	startTime := time.Now()
	output, err := ag.Execute(checkCtx, input)
	elapsed := time.Since(startTime)

	hc.mu.Lock()
	defer hc.mu.Unlock()

	status := hc.agentStatus[name]
	status.Name = name
	status.LastCheckTime = time.Now()

	if err != nil || (output != nil && !output.Success) {
		// 健康检查失败
		status.Healthy = false
		status.FailureCount++
		if err != nil {
			status.ErrorMessage = err.Error()
		} else {
			status.ErrorMessage = output.Error
		}

		logger.Warn("Agent health check failed",
			zap.String("agent", name),
			zap.Int("failure_count", status.FailureCount),
			zap.Duration("elapsed", elapsed),
			zap.String("error", status.ErrorMessage),
		)

		// 如果连续失败次数超过阈值，触发任务重新分配
		if status.FailureCount >= 3 {
			hc.handleAgentFailure(name)
		}
	} else {
		// 健康检查成功
		if !status.Healthy {
			logger.Info("Agent recovered",
				zap.String("agent", name),
				zap.Int("previous_failures", status.FailureCount),
			)
		}

		status.Healthy = true
		status.LastHealthyTime = time.Now()
		status.FailureCount = 0
		status.ErrorMessage = ""
	}

	hc.agentStatus[name] = status
}

func supportsHealthCheck(ag agent.Agent) bool {
	for _, capability := range ag.GetCapabilities() {
		if capability == "health-check" {
			return true
		}
	}
	return false
}

func (hc *HealthChecker) markAgentHealthy(name string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	status := hc.agentStatus[name]
	status.Name = name
	status.Healthy = true
	status.LastCheckTime = time.Now()
	status.LastHealthyTime = status.LastCheckTime
	status.FailureCount = 0
	status.ErrorMessage = ""
	hc.agentStatus[name] = status
}

// handleAgentFailure handles agent failure by reassigning its tasks
func (hc *HealthChecker) handleAgentFailure(agentName string) {
	logger.Error("Agent failed, reassigning tasks",
		zap.String("agent", agentName),
	)

	// 重新分配该 Agent 的所有任务
	reassignedTasks := hc.taskManager.ReassignAgentTasks(agentName)

	logger.Info("Tasks reassigned",
		zap.String("failed_agent", agentName),
		zap.Int("reassigned_count", len(reassignedTasks)),
		zap.Strings("task_ids", reassignedTasks),
	)

	// 可选：从 Registry 中注销该 Agent
	// hc.registry.Unregister(agentName)
}

// GetAgentStatus returns the health status of all agents
func (hc *HealthChecker) GetAgentStatus() map[string]AgentHealthStatus {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	result := make(map[string]AgentHealthStatus, len(hc.agentStatus))
	for k, v := range hc.agentStatus {
		result[k] = v
	}

	return result
}

// GetAgentStatusByName returns the health status of a specific agent
func (hc *HealthChecker) GetAgentStatusByName(name string) (AgentHealthStatus, bool) {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	status, exists := hc.agentStatus[name]
	return status, exists
}

// CheckStaleTasks checks for tasks that have been running too long
func (hc *HealthChecker) CheckStaleTasks(timeout time.Duration) []*Task {
	staleTasks := hc.taskManager.GetStaleTasks(timeout)

	if len(staleTasks) > 0 {
		logger.Warn("Found stale tasks",
			zap.Int("count", len(staleTasks)),
		)

		// 可以选择自动取消或重试这些任务
		for _, task := range staleTasks {
			logger.Warn("Stale task detected",
				zap.String("task_id", task.ID),
				zap.String("agent", task.AgentName),
				zap.Duration("running_time", time.Since(*task.StartedAt)),
			)
		}
	}

	return staleTasks
}
