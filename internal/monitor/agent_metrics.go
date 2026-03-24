package monitor

import "time"

type AgentMetrics struct {
	registry *Registry
}

func NewAgentMetrics(registry *Registry) *AgentMetrics {
	return &AgentMetrics{registry: registry}
}

func (m *AgentMetrics) ObserveAgentStarted(agent string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("agent_started_total", map[string]string{"agent": agent})
}

func (m *AgentMetrics) ObserveAgentFinished(agent, status string, latency time.Duration) {
	if m == nil || m.registry == nil {
		return
	}
	labels := map[string]string{
		"agent":  agent,
		"status": status,
	}
	m.registry.IncCounter("agent_execution_total", labels)
	m.registry.ObserveSummary("agent_execution_duration_seconds", labels, latency.Seconds())
}

func (m *AgentMetrics) ObserveAgentStreamChunk(agent string) {
	if m == nil || m.registry == nil {
		return
	}
	m.registry.IncCounter("agent_stream_chunks_total", map[string]string{"agent": agent})
}
