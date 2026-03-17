package tool

import (
	"context"
	"testing"
)

// MockTool is a mock tool for testing
type MockTool struct {
	name string
}

func (m *MockTool) GetDefinition() *Definition {
	return &Definition{
		Name:        m.name,
		Description: "Mock tool for testing",
		Parameters: &ParametersSchema{
			Type: "object",
			Properties: map[string]*PropertySchema{
				"test_param": {
					Type:        "string",
					Description: "Test parameter",
				},
			},
			Required: []string{"test_param"},
		},
	}
}

func (m *MockTool) Execute(ctx context.Context, params map[string]interface{}) (*Result, error) {
	return &Result{
		Success: true,
		Data: map[string]interface{}{
			"output": "test_output",
		},
	}, nil
}

func TestRegistryRegister(t *testing.T) {
	registry := NewRegistry()

	tool1 := &MockTool{name: "tool1"}
	err := registry.Register(tool1)
	if err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	if registry.Count() != 1 {
		t.Errorf("Expected 1 tool, got %d", registry.Count())
	}

	// Test duplicate registration
	err = registry.Register(tool1)
	if err == nil {
		t.Error("Expected error when registering duplicate tool")
	}
}

func TestRegistryGet(t *testing.T) {
	registry := NewRegistry()
	tool1 := &MockTool{name: "tool1"}
	registry.Register(tool1)

	retrieved, err := registry.Get("tool1")
	if err != nil {
		t.Fatalf("Failed to get tool: %v", err)
	}

	if retrieved.GetDefinition().Name != "tool1" {
		t.Errorf("Expected tool1, got %s", retrieved.GetDefinition().Name)
	}

	// Test getting non-existent tool
	_, err = registry.Get("non_existent")
	if err == nil {
		t.Error("Expected error when getting non-existent tool")
	}
}

func TestRegistryHas(t *testing.T) {
	registry := NewRegistry()
	tool1 := &MockTool{name: "tool1"}
	registry.Register(tool1)

	if !registry.Has("tool1") {
		t.Error("Expected registry to have tool1")
	}

	if registry.Has("non_existent") {
		t.Error("Expected registry not to have non_existent tool")
	}
}

func TestRegistryGetAll(t *testing.T) {
	registry := NewRegistry()
	tool1 := &MockTool{name: "tool1"}
	tool2 := &MockTool{name: "tool2"}

	registry.Register(tool1)
	registry.Register(tool2)

	tools := registry.GetAll()
	if len(tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(tools))
	}
}

func TestRegistryGetDefinitions(t *testing.T) {
	registry := NewRegistry()
	tool1 := &MockTool{name: "tool1"}
	tool2 := &MockTool{name: "tool2"}

	registry.Register(tool1)
	registry.Register(tool2)

	defs := registry.GetDefinitions()
	if len(defs) != 2 {
		t.Errorf("Expected 2 definitions, got %d", len(defs))
	}

	// Verify definitions are correct
	for _, def := range defs {
		if def.Name != "tool1" && def.Name != "tool2" {
			t.Errorf("Unexpected tool name: %s", def.Name)
		}
	}
}

func TestRegistryUnregister(t *testing.T) {
	registry := NewRegistry()
	tool1 := &MockTool{name: "tool1"}
	registry.Register(tool1)

	err := registry.Unregister("tool1")
	if err != nil {
		t.Fatalf("Failed to unregister tool: %v", err)
	}

	if registry.Count() != 0 {
		t.Errorf("Expected 0 tools after unregister, got %d", registry.Count())
	}

	// Test unregistering non-existent tool
	err = registry.Unregister("non_existent")
	if err == nil {
		t.Error("Expected error when unregistering non-existent tool")
	}
}

func TestRegistryCount(t *testing.T) {
	registry := NewRegistry()

	if registry.Count() != 0 {
		t.Errorf("Expected 0 tools in new registry, got %d", registry.Count())
	}

	tool1 := &MockTool{name: "tool1"}
	tool2 := &MockTool{name: "tool2"}

	registry.Register(tool1)
	if registry.Count() != 1 {
		t.Errorf("Expected 1 tool, got %d", registry.Count())
	}

	registry.Register(tool2)
	if registry.Count() != 2 {
		t.Errorf("Expected 2 tools, got %d", registry.Count())
	}

	registry.Unregister("tool1")
	if registry.Count() != 1 {
		t.Errorf("Expected 1 tool after unregister, got %d", registry.Count())
	}
}
