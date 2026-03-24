package monitor

import "time"

type ToolMetrics struct {
	registry *Registry
}

func NewToolMetrics(registry *Registry) *ToolMetrics {
	return &ToolMetrics{registry: registry}
}

func (m *ToolMetrics) ObserveToolStarted(toolName string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("tool_started_total", map[string]string{"tool": toolName})
}

func (m *ToolMetrics) ObserveToolFinished(toolName, status string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	labels := map[string]string{
		"tool":   toolName,
		"status": status,
	}
	m.registry.IncCounter("tool_execution_total", labels)
	m.registry.ObserveSummary("tool_execution_duration_seconds", labels, latency.Seconds())
}

func (m *ToolMetrics) ObserveToolRetry(toolName, reason string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("tool_retries_total", map[string]string{
		"tool":   toolName,
		"reason": reason,
	})
}

func (m *ToolMetrics) ObserveToolTimeout(toolName string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("tool_timeout_total", map[string]string{"tool": toolName})
}

func (m *ToolMetrics) ObserveToolValidationFailed(toolName string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("tool_validation_failed_total", map[string]string{"tool": toolName})
}

func (m *ToolMetrics) ObserveToolPanic(toolName string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("tool_panic_total", map[string]string{"tool": toolName})
}
