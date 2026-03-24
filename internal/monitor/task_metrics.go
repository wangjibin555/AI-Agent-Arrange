package monitor

import (
	"strconv"
	"time"
)

type TaskMetrics struct {
	registry *Registry
}

func NewTaskMetrics(registry *Registry) *TaskMetrics {
	return &TaskMetrics{registry: registry}
}

func (m *TaskMetrics) ObserveExecutionRequest(execType, status string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("execution_requests_total", map[string]string{
		"type":   execType,
		"status": status,
	})
	m.registry.ObserveSummary("execution_duration_seconds", map[string]string{
		"type":   execType,
		"status": status,
	}, latency.Seconds())
}

func (m *TaskMetrics) IncExecutionInflight(execType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncGauge("execution_inflight", map[string]string{"type": execType})
}

func (m *TaskMetrics) DecExecutionInflight(execType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.DecGauge("execution_inflight", map[string]string{"type": execType})
}

func (m *TaskMetrics) ObserveTaskCreated(agentName, capability, action string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("task_created_total", map[string]string{
		"agent":      agentName,
		"capability": capability,
		"action":     action,
	})
}

func (m *TaskMetrics) ObserveExecutionCancel(execType, status string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("execution_cancel_total", map[string]string{
		"type":   execType,
		"status": status,
	})
}

func (m *TaskMetrics) ObserveQueueLength(length int) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.SetGauge("task_queue_length", nil, float64(length))
}

func (m *TaskMetrics) ObserveTaskStarted(agentName, action string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("task_started_total", map[string]string{
		"agent":  agentName,
		"action": action,
	})
}

func (m *TaskMetrics) ObserveTaskFinished(agentName, action, status string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	labels := map[string]string{
		"agent":  agentName,
		"action": action,
		"status": status,
	}
	m.registry.IncCounter("task_execution_total", labels)
	m.registry.ObserveSummary("task_execution_duration_seconds", labels, latency.Seconds())
}

func (m *TaskMetrics) ObserveTaskRetry(agentName, reason string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("task_retries_total", map[string]string{
		"agent":  agentName,
		"reason": reason,
	})
}

func (m *TaskMetrics) ObserveTaskTimeout(agentName, action string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("task_timeout_total", map[string]string{
		"agent":  agentName,
		"action": action,
	})
}

func (m *TaskMetrics) ObserveWorkerActive(workerID int, active bool) {
	if m == nil || m.registry == nil {
		return
	}
	labels := map[string]string{
		"worker_id": strconv.Itoa(workerID),
	}
	if active {
		m.registry.SetGauge("worker_active", labels, 1)
		m.registry.IncGauge("worker_active_total", nil)
		return
	}
	m.registry.SetGauge("worker_active", labels, 0)
	m.registry.DecGauge("worker_active_total", nil)
}

func (m *TaskMetrics) ObserveAgentLoad(agentName string, load int) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.SetGauge("agent_load", map[string]string{
		"agent": agentName,
	}, float64(load))
}
