package monitor

import "time"

type WorkflowMetrics struct {
	registry *Registry
}

func NewWorkflowMetrics(registry *Registry) *WorkflowMetrics {
	return &WorkflowMetrics{registry: registry}
}

func (m *WorkflowMetrics) ObserveWorkflowStarted(workflowID string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("workflow_runs_total", map[string]string{
		"workflow": workflowID,
		"status":   "started",
	})
	m.registry.IncGauge("workflow_inflight", map[string]string{"workflow": workflowID})
}

func (m *WorkflowMetrics) ObserveWorkflowFinished(workflowID, status string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.DecGauge("workflow_inflight", map[string]string{"workflow": workflowID})
	m.registry.IncCounter("workflow_runs_total", map[string]string{
		"workflow": workflowID,
		"status":   status,
	})
	m.registry.ObserveSummary("workflow_duration_seconds", map[string]string{
		"workflow": workflowID,
		"status":   status,
	}, latency.Seconds())
}

func (m *WorkflowMetrics) ObserveStepStarted(workflowID, stepID, stepType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("workflow_step_runs_total", map[string]string{
		"workflow": workflowID,
		"step":     stepID,
		"type":     stepType,
		"status":   "started",
	})
}

func (m *WorkflowMetrics) ObserveStepFinished(workflowID, stepID, stepType, status string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	labels := map[string]string{
		"workflow": workflowID,
		"step":     stepID,
		"type":     stepType,
		"status":   status,
	}
	m.registry.IncCounter("workflow_step_runs_total", labels)
	m.registry.ObserveSummary("workflow_step_duration_seconds", labels, latency.Seconds())
}

func (m *WorkflowMetrics) ObserveStepSkipped(workflowID, stepID, reason string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("workflow_step_skipped_total", map[string]string{
		"workflow": workflowID,
		"step":     stepID,
		"reason":   reason,
	})
}

func (m *WorkflowMetrics) ObserveStepFailed(workflowID, stepID string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("workflow_step_failed_total", map[string]string{
		"workflow": workflowID,
		"step":     stepID,
	})
}

func (m *WorkflowMetrics) ObserveRecovery(workflowID, status string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("workflow_recovery_total", map[string]string{
		"workflow": workflowID,
		"status":   status,
	})
}

func (m *WorkflowMetrics) ObserveCancel(workflowID string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("workflow_cancel_total", map[string]string{
		"workflow": workflowID,
	})
}
