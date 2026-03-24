package monitor

import "time"

type SSEMetrics struct {
	registry *Registry
}

func NewSSEMetrics(registry *Registry) *SSEMetrics {
	return &SSEMetrics{registry: registry}
}

func (m *SSEMetrics) ObserveConnection(executionType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("sse_connections_total", map[string]string{
		"execution_type": executionType,
	})
	m.registry.IncGauge("sse_connections_inflight", map[string]string{
		"execution_type": executionType,
	})
}

func (m *SSEMetrics) ObserveDisconnect(executionType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.DecGauge("sse_connections_inflight", map[string]string{
		"execution_type": executionType,
	})
}

func (m *SSEMetrics) ObserveEventDelivered(executionType, eventType string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	labels := map[string]string{
		"execution_type": executionType,
		"event_type":     eventType,
	}
	m.registry.IncCounter("sse_events_total", labels)
	m.registry.ObserveSummary("sse_event_delivery_duration_seconds", labels, latency.Seconds())
}

func (m *SSEMetrics) ObserveEventDropped(executionType, eventType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("sse_event_dropped_total", map[string]string{
		"execution_type": executionType,
		"event_type":     eventType,
	})
}

func (m *SSEMetrics) ObserveHeartbeat(executionType string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("sse_heartbeat_total", map[string]string{
		"execution_type": executionType,
	})
}
