package agent

import "fmt"

// CandidateSelector 用于从候选 Agent 集合中挑选最终执行者。
type CandidateSelector func([]Agent) (Agent, error)

// ResolveRequest 描述一次 Agent 解析请求。
type ResolveRequest struct {
	AgentName          string
	Capability         string
	FallbackCapability string
}

// ResolveHooks 允许不同调用方自定义解析失败和候选选择策略。
type ResolveHooks struct {
	OnNamedAgentNotFound func(agentName string, cause error) error
	OnCapabilityNotFound func(capability string) error
	OnRegistryEmpty      func() error
	SelectCandidate      CandidateSelector
}

// Resolver 封装基于 Agent 名称或能力的统一选择逻辑。
type Resolver struct {
	registry *Registry
}

// NewResolver 基于一个注册中心创建 Resolver。
func NewResolver(registry *Registry) *Resolver {
	return &Resolver{registry: registry}
}

// Resolve 按“显式名称 -> 指定能力 -> 兜底能力”的顺序解析出具体 Agent。
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
