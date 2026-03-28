package tool

import "context"

// Tool 定义 Agent 可调用的工具接口。
type Tool interface {
	// GetDefinition 返回供 LLM 函数调用使用的工具元数据。
	GetDefinition() *Definition

	// Execute 使用给定参数执行工具。
	Execute(ctx context.Context, params map[string]interface{}) (*Result, error)
}

// Definition 描述工具在函数调用场景下的定义信息。
type Definition struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  *ParametersSchema `json:"parameters"`
}

// ParametersSchema 表示工具参数的 JSON Schema。
type ParametersSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]*PropertySchema `json:"properties"`
	Required   []string                   `json:"required,omitempty"`
}

// PropertySchema 表示单个参数字段的 Schema 定义。
type PropertySchema struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Enum        []string        `json:"enum,omitempty"`
	Items       *PropertySchema `json:"items,omitempty"` // 数组类型时的元素定义
}

// Result 表示一次工具调用的返回结果。
type Result struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}
