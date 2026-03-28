package tool

import (
	"fmt"
	"sync"
)

// Registry 管理当前系统中所有已注册的工具。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry 创建工具注册中心。
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register 注册一个新的工具实例。
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	def := tool.GetDefinition()
	if def == nil {
		return fmt.Errorf("tool definition is nil")
	}

	if def.Name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	if _, exists := r.tools[def.Name]; exists {
		return fmt.Errorf("tool %s already registered", def.Name)
	}

	r.tools[def.Name] = tool
	return nil
}

// Get 按名称获取工具实例。
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}

	return tool, nil
}

// GetAll 返回全部已注册工具。
func (r *Registry) GetAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}

	return tools
}

// GetDefinitions 返回全部工具定义，主要用于 LLM 函数调用能力暴露。
func (r *Registry) GetDefinitions() []*Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]*Definition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.GetDefinition())
	}

	return defs
}

// Has 判断工具是否已注册。
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.tools[name]
	return exists
}

// Unregister 从注册中心移除一个工具。
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; !exists {
		return fmt.Errorf("tool %s not found", name)
	}

	delete(r.tools, name)
	return nil
}

// Count 返回当前已注册工具数量。
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.tools)
}
