package tool

import "context"

// Tool represents a callable function/tool that can be invoked by an agent
type Tool interface {
	// GetDefinition 返回 LLM 函数调用的工具元数据
	GetDefinition() *Definition

	// Execute executes the tool with given parameters
	Execute(ctx context.Context, params map[string]interface{}) (*Result, error)
}

// Definition represents the tool definition for LLM function calling
type Definition struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  *ParametersSchema `json:"parameters"`
}

// ParametersSchema represents the JSON Schema for tool parameters
type ParametersSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]*PropertySchema `json:"properties"`
	Required   []string                   `json:"required,omitempty"`
}

// PropertySchema represents a single parameter property
type PropertySchema struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Enum        []string        `json:"enum,omitempty"`
	Items       *PropertySchema `json:"items,omitempty"` // For array types
}

// Result represents the result of a tool execution
type Result struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}
