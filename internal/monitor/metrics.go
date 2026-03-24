package monitor

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type Config struct {
	Namespace string
}

type Metrics struct {
	registry   *Registry
	HTTP       *HTTPMetrics
	Dependency *DependencyMetrics
	Task       *TaskMetrics
	Workflow   *WorkflowMetrics
	Tool       *ToolMetrics
	Agent      *AgentMetrics
	SSE        *SSEMetrics
}

func New(cfg Config) *Metrics {
	if cfg.Namespace == "" {
		cfg.Namespace = "ai_agent_arrange"
	}

	registry := NewRegistry(cfg.Namespace)
	return &Metrics{
		registry:   registry,
		HTTP:       NewHTTPMetrics(registry),
		Dependency: NewDependencyMetrics(registry),
		Task:       NewTaskMetrics(registry),
		Workflow:   NewWorkflowMetrics(registry),
		Tool:       NewToolMetrics(registry),
		Agent:      NewAgentMetrics(registry),
		SSE:        NewSSEMetrics(registry),
	}
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(m.registry.Render()))
	})
}

type Registry struct {
	namespace string
	mu        sync.RWMutex
	counters  map[string]float64
	gauges    map[string]float64
	summaries map[string]*summaryMetric
}

type summaryMetric struct {
	Count float64
	Sum   float64
}

func NewRegistry(namespace string) *Registry {
	return &Registry{
		namespace: namespace,
		counters:  make(map[string]float64),
		gauges:    make(map[string]float64),
		summaries: make(map[string]*summaryMetric),
	}
}

func (r *Registry) IncCounter(name string, labels map[string]string) {
	r.AddCounter(name, labels, 1)
}

func (r *Registry) AddCounter(name string, labels map[string]string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[r.metricKey(name, labels)] += delta
}

func (r *Registry) IncGauge(name string, labels map[string]string) {
	r.AddGauge(name, labels, 1)
}

func (r *Registry) DecGauge(name string, labels map[string]string) {
	r.AddGauge(name, labels, -1)
}

func (r *Registry) AddGauge(name string, labels map[string]string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[r.metricKey(name, labels)] += delta
}

func (r *Registry) SetGauge(name string, labels map[string]string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[r.metricKey(name, labels)] = value
}

func (r *Registry) ObserveSummary(name string, labels map[string]string, value float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := r.metricKey(name, labels)
	current := r.summaries[key]
	if current == nil {
		current = &summaryMetric{}
		r.summaries[key] = current
	}
	current.Count++
	current.Sum += value
}

func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lines := make([]string, 0, len(r.counters)+len(r.gauges)+len(r.summaries)*2)

	counterKeys := sortedKeys(r.counters)
	for _, key := range counterKeys {
		lines = append(lines, fmt.Sprintf("%s %v", key, r.counters[key]))
	}

	gaugeKeys := sortedKeys(r.gauges)
	for _, key := range gaugeKeys {
		lines = append(lines, fmt.Sprintf("%s %v", key, r.gauges[key]))
	}

	summaryKeys := sortedSummaryKeys(r.summaries)
	for _, key := range summaryKeys {
		lines = append(lines, fmt.Sprintf("%s_count %v", key, r.summaries[key].Count))
		lines = append(lines, fmt.Sprintf("%s_sum %v", key, r.summaries[key].Sum))
	}

	if len(lines) == 0 {
		return "# no metrics collected yet\n"
	}
	return strings.Join(lines, "\n") + "\n"
}

func (r *Registry) metricKey(name string, labels map[string]string) string {
	fullName := r.namespace + "_" + name
	if len(labels) == 0 {
		return fullName
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapeLabelValue(labels[key])))
	}
	return fmt.Sprintf("%s{%s}", fullName, strings.Join(parts, ","))
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSummaryKeys(m map[string]*summaryMetric) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
