package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// SlowTool simulates a tool that takes time to execute
type SlowTool struct {
	name     string
	duration time.Duration
	mu       sync.Mutex
	callLog  []time.Time
}

func NewSlowTool(name string, duration time.Duration) *SlowTool {
	return &SlowTool{
		name:     name,
		duration: duration,
		callLog:  make([]time.Time, 0),
	}
}

func (t *SlowTool) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        t.name,
		Description: "A slow tool for testing concurrency",
		Parameters: &tool.ParametersSchema{
			Type:       "object",
			Properties: map[string]*tool.PropertySchema{},
			Required:   []string{},
		},
	}
}

func (t *SlowTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	t.mu.Lock()
	t.callLog = append(t.callLog, time.Now())
	t.mu.Unlock()

	// Simulate work
	select {
	case <-time.After(t.duration):
		return &tool.Result{
			Success: true,
			Data: map[string]interface{}{
				"tool":     t.name,
				"duration": t.duration.String(),
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestConcurrentToolExecution tests that tools execute concurrently
func TestConcurrentToolExecution(t *testing.T) {
	// Create registry with slow tools
	registry := tool.NewRegistry()

	tool1 := NewSlowTool("slow_tool_1", 200*time.Millisecond)
	tool2 := NewSlowTool("slow_tool_2", 200*time.Millisecond)
	tool3 := NewSlowTool("slow_tool_3", 200*time.Millisecond)

	registry.Register(tool1)
	registry.Register(tool2)
	registry.Register(tool3)

	// Create OpenAI agent
	agent := NewOpenAIAgent("test-agent", "dummy-key")
	agent.SetToolRegistry(registry)

	// Create mock tool calls
	toolCalls := []openai.ToolCall{
		{
			ID:   "call_1",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "slow_tool_1",
				Arguments: "{}",
			},
		},
		{
			ID:   "call_2",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "slow_tool_2",
				Arguments: "{}",
			},
		},
		{
			ID:   "call_3",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "slow_tool_3",
				Arguments: "{}",
			},
		},
	}

	// Execute concurrently
	startTime := time.Now()

	input := &TaskInput{
		TaskID:     "test-task",
		Parameters: make(map[string]interface{}),
	}

	results := agent.executeToolCallsConcurrently(context.Background(), toolCalls, input)

	elapsed := time.Since(startTime)

	// Verify results
	if len(results) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(results))
	}

	// Check that execution was concurrent (should be ~200ms, not 600ms)
	if elapsed > 400*time.Millisecond {
		t.Errorf("Execution took too long: %v (expected ~200ms for concurrent execution)", elapsed)
	}

	t.Logf("✅ Concurrent execution completed in %v (sequential would take ~600ms)", elapsed)

	// Verify all results succeeded
	for i, result := range results {
		if result.Error != nil {
			t.Errorf("Result %d failed: %v", i, result.Error)
		}
		if result.Result == nil {
			t.Errorf("Result %d has nil result", i)
		}
	}

	// Verify order is maintained
	for i, result := range results {
		if result.Index != i {
			t.Errorf("Result order not maintained: expected index %d, got %d", i, result.Index)
		}
		expectedToolCallID := toolCalls[i].ID
		if result.ToolCall.ID != expectedToolCallID {
			t.Errorf("Result %d has wrong ToolCall ID: expected %s, got %s",
				i, expectedToolCallID, result.ToolCall.ID)
		}
	}

	// Verify all tools started roughly at the same time (within 50ms)
	if len(tool1.callLog) > 0 && len(tool2.callLog) > 0 && len(tool3.callLog) > 0 {
		startTime1 := tool1.callLog[0]
		startTime2 := tool2.callLog[0]
		startTime3 := tool3.callLog[0]

		maxDiff := max(
			startTime2.Sub(startTime1).Abs(),
			startTime3.Sub(startTime1).Abs(),
			startTime3.Sub(startTime2).Abs(),
		)

		if maxDiff > 50*time.Millisecond {
			t.Logf("⚠️  Tools didn't start simultaneously (max diff: %v)", maxDiff)
		} else {
			t.Logf("✅ All tools started within %v of each other", maxDiff)
		}
	}
}

// TestPanicRecovery tests that panicking tools don't crash the agent
func TestPanicRecovery(t *testing.T) {
	// Create a tool that panics
	panicTool := &PanicTool{}

	registry := tool.NewRegistry()
	registry.Register(panicTool)

	agent := NewOpenAIAgent("test-agent", "dummy-key")
	agent.SetToolRegistry(registry)

	toolCalls := []openai.ToolCall{
		{
			ID:   "call_panic",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "panic_tool",
				Arguments: "{}",
			},
		},
	}

	input := &TaskInput{
		TaskID:     "test-task",
		Parameters: make(map[string]interface{}),
	}

	// Should not panic
	results := agent.executeToolCallsConcurrently(context.Background(), toolCalls, input)

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]
	if result.Error == nil {
		t.Error("Expected error from panicking tool")
	}

	if result.ResultContent == "" {
		t.Error("Expected error message in ResultContent")
	}

	t.Logf("✅ Panic successfully recovered: %s", result.ResultContent)
}

// PanicTool is a tool that always panics
type PanicTool struct{}

func (t *PanicTool) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "panic_tool",
		Description: "A tool that panics",
		Parameters: &tool.ParametersSchema{
			Type:       "object",
			Properties: map[string]*tool.PropertySchema{},
			Required:   []string{},
		},
	}
}

func (t *PanicTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	panic("intentional panic for testing")
}

// Helper function for Go 1.21+ (or implement manually for older versions)
func max(a, b, c time.Duration) time.Duration {
	result := a
	if b > result {
		result = b
	}
	if c > result {
		result = c
	}
	return result
}
