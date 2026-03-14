package agent

import (
	"fmt"
	"sync"
)

// Registry manages all registered agents
type Registry struct {
	agents map[string]Agent
	mu     sync.RWMutex
}

// NewRegistry creates a new agent registry
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]Agent),
	}
}

// Register registers a new agent
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

// Unregister removes an agent from the registry
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[name]; !exists {
		return fmt.Errorf("agent %s not found", name)
	}

	delete(r.agents, name)
	return nil
}

// Get retrieves an agent by name
func (r *Registry) Get(name string) (Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[name]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", name)
	}

	return agent, nil
}

// GetAll returns all registered agents
func (r *Registry) GetAll() map[string]Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]Agent, len(r.agents))
	for k, v := range r.agents {
		result[k] = v
	}
	return result
}

// FindByCapability finds all agents with a specific capability
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
