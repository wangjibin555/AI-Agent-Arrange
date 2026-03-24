package monitor

import (
	"strconv"
	"time"
)

type DependencyMetrics struct {
	registry *Registry
}

type DependencyOperation struct {
	metrics        *DependencyMetrics
	dependencyType string
	dependencyName string
	operation      string
	start          time.Time
	slowThreshold  time.Duration
}

func NewDependencyMetrics(registry *Registry) *DependencyMetrics {
	return &DependencyMetrics{registry: registry}
}

func (m *DependencyMetrics) Observe(dependencyType, dependencyName, operation, status string, latency time.Duration, slowThreshold time.Duration) {
	labels := map[string]string{
		"dependency_type": dependencyType,
		"dependency_name": dependencyName,
		"operation":       operation,
		"status":          status,
	}
	m.registry.IncCounter("dependency_calls_total", labels)
	m.registry.ObserveSummary("dependency_call_duration_seconds", labels, latency.Seconds())

	if slowThreshold > 0 && latency > slowThreshold {
		m.registry.IncCounter("dependency_slow_total", map[string]string{
			"dependency_type": dependencyType,
			"dependency_name": dependencyName,
			"operation":       operation,
			"threshold_ms":    strconv.FormatInt(slowThreshold.Milliseconds(), 10),
		})
	}
}

func (m *DependencyMetrics) Start(dependencyType, dependencyName, operation string, slowThreshold time.Duration) *DependencyOperation {
	labels := map[string]string{
		"dependency_type": dependencyType,
		"dependency_name": dependencyName,
		"operation":       operation,
	}
	m.registry.IncGauge("dependency_calls_inflight", labels)

	return &DependencyOperation{
		metrics:        m,
		dependencyType: dependencyType,
		dependencyName: dependencyName,
		operation:      operation,
		start:          time.Now(),
		slowThreshold:  slowThreshold,
	}
}

func (o *DependencyOperation) Retry(reason string) {
	if o == nil || o.metrics == nil {
		return
	}
	o.metrics.registry.IncCounter("dependency_retries_total", map[string]string{
		"dependency_type": o.dependencyType,
		"dependency_name": o.dependencyName,
		"operation":       o.operation,
		"reason":          reason,
	})
}

func (o *DependencyOperation) Finish(status string) {
	if o == nil || o.metrics == nil {
		return
	}

	labels := map[string]string{
		"dependency_type": o.dependencyType,
		"dependency_name": o.dependencyName,
		"operation":       o.operation,
	}
	elapsed := time.Since(o.start)

	o.metrics.registry.DecGauge("dependency_calls_inflight", labels)
	o.metrics.registry.IncCounter("dependency_calls_total", mergeLabels(labels, map[string]string{
		"status": status,
	}))
	o.metrics.registry.ObserveSummary("dependency_call_duration_seconds", mergeLabels(labels, map[string]string{
		"status": status,
	}), elapsed.Seconds())

	if o.slowThreshold > 0 && elapsed > o.slowThreshold {
		o.metrics.registry.IncCounter("dependency_slow_total", mergeLabels(labels, map[string]string{
			"threshold_ms": strconv.FormatInt(o.slowThreshold.Milliseconds(), 10),
		}))
	}
}

func mergeLabels(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}
