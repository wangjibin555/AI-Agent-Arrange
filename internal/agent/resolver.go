package agent

import "fmt"

// CandidateSelector picks one agent from a resolved candidate set.
type CandidateSelector func([]Agent) (Agent, error)

// ResolveRequest describes one agent resolution attempt.
type ResolveRequest struct {
	AgentName          string
	Capability         string
	FallbackCapability string
}

// ResolveHooks customizes resolver behavior for different callers.
type ResolveHooks struct {
	OnNamedAgentNotFound func(agentName string, cause error) error
	OnCapabilityNotFound func(capability string) error
	OnRegistryEmpty      func() error
	SelectCandidate      CandidateSelector
}

// Resolver centralizes agent/capability based selection.
type Resolver struct {
	registry *Registry
}

// NewResolver creates a resolver over one registry.
func NewResolver(registry *Registry) *Resolver {
	return &Resolver{registry: registry}
}

// Resolve returns one concrete agent using direct name, capability, or fallback capability.
func (r *Resolver) Resolve(req ResolveRequest, hooks ResolveHooks) (Agent, error) {
	if r == nil || r.registry == nil {
		return nil, fmt.Errorf("agent registry is not configured")
	}

	if req.AgentName != "" {
		ag, err := r.registry.Get(req.AgentName)
		if err != nil {
			if hooks.OnNamedAgentNotFound != nil {
				return nil, hooks.OnNamedAgentNotFound(req.AgentName, err)
			}
			return nil, err
		}
		return ag, nil
	}

	capability := req.Capability
	if capability == "" {
		capability = req.FallbackCapability
	}

	var candidates []Agent
	if capability != "" {
		candidates = r.registry.FindByCapability(capability)
		if len(candidates) == 0 {
			if hooks.OnCapabilityNotFound != nil {
				return nil, hooks.OnCapabilityNotFound(capability)
			}
			return nil, fmt.Errorf("no agent with capability %q found", capability)
		}
	} else {
		allAgents := r.registry.GetAll()
		if len(allAgents) == 0 {
			if hooks.OnRegistryEmpty != nil {
				return nil, hooks.OnRegistryEmpty()
			}
			return nil, fmt.Errorf("no agents registered")
		}
		candidates = make([]Agent, 0, len(allAgents))
		for _, ag := range allAgents {
			candidates = append(candidates, ag)
		}
	}

	if hooks.SelectCandidate != nil {
		return hooks.SelectCandidate(candidates)
	}
	return candidates[0], nil
}
