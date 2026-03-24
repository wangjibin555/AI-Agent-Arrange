package monitor

import "time"

type HTTPMetrics struct {
	registry *Registry
}

func NewHTTPMetrics(registry *Registry) *HTTPMetrics {
	return &HTTPMetrics{registry: registry}
}

func (m *HTTPMetrics) IncInflight(route, method string) {
	m.registry.IncGauge("http_requests_inflight", map[string]string{
		"route":  route,
		"method": method,
	})
}

func (m *HTTPMetrics) DecInflight(route, method string) {
	m.registry.DecGauge("http_requests_inflight", map[string]string{
		"route":  route,
		"method": method,
	})
}

func (m *HTTPMetrics) Observe(route, method, status string, latency time.Duration) {
	labels := map[string]string{
		"route":  route,
		"method": method,
		"status": status,
	}
	m.registry.IncCounter("http_requests_total", labels)
	m.registry.ObserveSummary("http_request_duration_seconds", labels, latency.Seconds())
}
