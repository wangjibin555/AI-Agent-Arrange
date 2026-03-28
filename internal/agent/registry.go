package agent

import (
	"fmt"
	"sync"
)

// Registry 管理系统中所有已注册的 Agent。
type Registry struct {
	agents map[string]Agent
	mu     sync.RWMutex
}

// NewRegistry 创建 Agent 注册中心。
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]Agent),
	}
}

// Register 注册一个新的 Agent。
func (r *Registry) Register(agent Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := agent.GetName()
	if _, exists := r.agents[name]; exists {
		return fmt.Errorf("agent %s already registered", name)
	}

	r.agents[name] = agent

	return nil
}

// Unregister 从注册中心移除一个 Agent。
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[name]; !exists {
		return fmt.Errorf("agent %s not found", name)
	}

	delete(r.agents, name)
	return nil
}

// Get 按名称获取 Agent。
func (r *Registry) Get(name string) (Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[name]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", name)
	}

	return agent, nil
}

// GetAll 返回全部已注册 Agent。
func (r *Registry) GetAll() map[string]Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]Agent, len(r.agents))
	for k, v := range r.agents {
		result[k] = v
	}
	return result
}

// FindByCapability 查找具备指定能力的全部 Agent。
func (r *Registry) FindByCapability(capability string) []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Agent
	for _, agent := range r.agents {
		for _, cap := range agent.GetCapabilities() {
			if cap == capability {
				result = append(result, agent)
				break
			}
		}
	}
	return result
}

// 负载均衡逻辑已移至 TaskManager
