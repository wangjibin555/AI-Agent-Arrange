package tool_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// CalculatorTool for testing purposes
type CalculatorTool struct{}

func NewCalculatorTool() *CalculatorTool {
	return &CalculatorTool{}
}

func (t *CalculatorTool) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "calculator",
		Description: "Performs basic arithmetic operations",
		Parameters: &tool.ParametersSchema{
			Type: "object",
			Properties: map[string]*tool.PropertySchema{
				"operation": {
					Type:        "string",
					Description: "The operation to perform",
					Enum:        []string{"add", "subtract", "multiply", "divide"},
				},
				"a": {
					Type:        "number",
					Description: "First operand",
				},
				"b": {
					Type:        "number",
					Description: "Second operand",
				},
			},
			Required: []string{"operation", "a", "b"},
		},
	}
}

func (t *CalculatorTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok {
		return nil, tool.ErrInvalidParams("calculator", "operation must be a string")
	}

	a, err := getFloat64(params["a"])
	if err != nil {
		return nil, tool.ErrInvalidParams("calculator", "parameter 'a' must be a number")
	}

	b, err := getFloat64(params["b"])
	if err != nil {
		return nil, tool.ErrInvalidParams("calculator", "parameter 'b' must be a number")
	}

	var result float64
	switch operation {
	case "add":
		result = a + b
	case "subtract":
		result = a - b
	case "multiply":
		result = a * b
	case "divide":
		if b == 0 {
			return nil, tool.ErrExecutionFailed(
				"calculator",
				"division by zero",
				fmt.Errorf("cannot divide by zero"),
			).WithSuggestedFix("Ensure the divisor (b) is not zero")
		}
		result = a / b
	default:
		return nil, tool.ErrInvalidParams(
			"calculator",
			fmt.Sprintf("unknown operation: %s", operation),
		)
	}

	return &tool.Result{
		Success: true,
		Data: map[string]interface{}{
			"result": result,
		},
	}, nil
}

func getFloat64(v interface{}) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func TestExecutor_Success(t *testing.T) {
	registry := tool.NewRegistry()
	calculator := NewCalculatorTool()
	registry.Register(calculator)

	executor := tool.NewExecutor(registry, tool.DefaultExecutionConfig())

	result, err := executor.Execute(context.Background(), "calculator", map[string]interface{}{
		"operation": "add",
		"a":         float64(10),
		"b":         float64(5),
	})

	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	if !result.Success {
		t.Fatalf("Expected success=true, got false")
	}

	if result.Data["result"] != float64(15) {
		t.Errorf("Expected result=15, got %v", result.Data["result"])
	}
}

func TestExecutor_ToolNotFound(t *testing.T) {
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry, tool.DefaultExecutionConfig())

	_, err := executor.Execute(context.Background(), "nonexistent", map[string]interface{}{})

	if err == nil {
		t.Fatal("Expected error for nonexistent tool")
	}

	toolErr, ok := err.(*tool.ToolError)
	if !ok {
		t.Fatalf("Expected *tool.ToolError, got %T", err)
	}

	if toolErr.Type != tool.ErrorTypeNotFound {
		t.Errorf("Expected error type TOOL_NOT_FOUND, got %s", toolErr.Type)
	}

	if toolErr.Retryable {
		t.Error("Tool not found error should not be retryable")
	}
}

func TestExecutor_InvalidParams_Missing(t *testing.T) {
	registry := tool.NewRegistry()
	calculator := NewCalculatorTool()
	registry.Register(calculator)

	executor := tool.NewExecutor(registry, tool.DefaultExecutionConfig())

	_, err := executor.Execute(context.Background(), "calculator", map[string]interface{}{
		"operation": "add",
		// Missing "a" and "b"
	})

	if err == nil {
		t.Fatal("Expected error for missing parameters")
	}

	toolErr, ok := err.(*tool.ToolError)
	if !ok {
		t.Fatalf("Expected *tool.ToolError, got %T", err)
	}

	if toolErr.Type != tool.ErrorTypeInvalidParams {
		t.Errorf("Expected error type INVALID_PARAMETERS, got %s", toolErr.Type)
	}

	if toolErr.SuggestedFix == "" {
		t.Error("Expected suggested fix to be present")
	}

	llmMsg := toolErr.ToLLMMessage()
	if llmMsg == "" {
		t.Error("Expected non-empty LLM message")
	}
	t.Logf("LLM Message:\n%s", llmMsg)
}

func TestExecutor_InvalidParams_WrongType(t *testing.T) {
	registry := tool.NewRegistry()
	calculator := NewCalculatorTool()
	registry.Register(calculator)

	executor := tool.NewExecutor(registry, tool.DefaultExecutionConfig())

	_, err := executor.Execute(context.Background(), "calculator", map[string]interface{}{
		"operation": 123, // Should be string
		"a":         float64(10),
		"b":         float64(5),
	})

	if err == nil {
		t.Fatal("Expected error for wrong parameter type")
	}

	toolErr, ok := err.(*tool.ToolError)
	if !ok {
		t.Fatalf("Expected *tool.ToolError, got %T", err)
	}

	if toolErr.Type != tool.ErrorTypeInvalidParams {
		t.Errorf("Expected error type INVALID_PARAMETERS, got %s", toolErr.Type)
	}
}

func TestExecutor_InvalidParams_InvalidEnum(t *testing.T) {
	registry := tool.NewRegistry()
	calculator := NewCalculatorTool()
	registry.Register(calculator)

	executor := tool.NewExecutor(registry, tool.DefaultExecutionConfig())

	_, err := executor.Execute(context.Background(), "calculator", map[string]interface{}{
		"operation": "modulo", // Invalid operation
		"a":         float64(10),
		"b":         float64(5),
	})

	if err == nil {
		t.Fatal("Expected error for invalid enum value")
	}

	toolErr, ok := err.(*tool.ToolError)
	if !ok {
		t.Fatalf("Expected *tool.ToolError, got %T", err)
	}

	if toolErr.Type != tool.ErrorTypeInvalidParams {
		t.Errorf("Expected error type INVALID_PARAMETERS, got %s", toolErr.Type)
	}

	// Check that allowed values are included in details
	if toolErr.Details["allowed_values"] == nil {
		t.Error("Expected allowed_values in error details")
	}
}

func TestExecutor_ExecutionFailed_DivisionByZero(t *testing.T) {
	registry := tool.NewRegistry()
	calculator := NewCalculatorTool()
	registry.Register(calculator)

	executor := tool.NewExecutor(registry, tool.DefaultExecutionConfig())

	_, err := executor.Execute(context.Background(), "calculator", map[string]interface{}{
		"operation": "divide",
		"a":         float64(10),
		"b":         float64(0), // Division by zero
	})

	if err == nil {
		t.Fatal("Expected error for division by zero")
	}

	toolErr, ok := err.(*tool.ToolError)
	if !ok {
		t.Fatalf("Expected *tool.ToolError, got %T", err)
	}

	if toolErr.Type != tool.ErrorTypeExecutionFailed {
		t.Errorf("Expected error type EXECUTION_FAILED, got %s", toolErr.Type)
	}

	if toolErr.SuggestedFix == "" {
		t.Error("Expected suggested fix to be present")
	}

	t.Logf("LLM Message:\n%s", toolErr.ToLLMMessage())
}

func TestExecutor_Timeout(t *testing.T) {
	registry := tool.NewRegistry()

	// Create a slow tool that takes 2 seconds
	slowTool := &SlowTool{delay: 2 * time.Second}
	registry.Register(slowTool)

	// Configure executor with 500ms timeout
	config := &tool.ExecutionConfig{
		Timeout:      500 * time.Millisecond,
		MaxRetries:   0, // No retries for this test
		RetryDelay:   0,
		RetryBackoff: 1.0,
	}
	executor := tool.NewExecutor(registry, config)

	_, err := executor.Execute(context.Background(), "slow_tool", map[string]interface{}{})

	if err == nil {
		t.Fatal("Expected timeout error")
	}

	toolErr, ok := err.(*tool.ToolError)
	if !ok {
		t.Fatalf("Expected *tool.ToolError, got %T", err)
	}

	if toolErr.Type != tool.ErrorTypeTimeout {
		t.Errorf("Expected error type TIMEOUT, got %s", toolErr.Type)
	}

	if !toolErr.Retryable {
		t.Error("Timeout errors should be retryable")
	}
}

// SlowTool simulates a tool that takes time to execute
type SlowTool struct {
	delay time.Duration
}

func (t *SlowTool) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "slow_tool",
		Description: "A tool that simulates slow execution",
		Parameters: &tool.ParametersSchema{
			Type:       "object",
			Properties: map[string]*tool.PropertySchema{},
			Required:   []string{},
		},
	}
}

func (t *SlowTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	select {
	case <-time.After(t.delay):
		return &tool.Result{Success: true, Data: map[string]interface{}{}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
