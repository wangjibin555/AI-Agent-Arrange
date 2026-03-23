package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
)

func newTestEngine(t *testing.T, publisher EventPublisher) (*Engine, *integrationTestAgent) {
	t.Helper()

	registry := agent.NewRegistry()
	testAgent := newIntegrationTestAgent("test-agent")
	if err := registry.Register(testAgent); err != nil {
		t.Fatalf("register test agent: %v", err)
	}

	engine := NewEngine(EngineConfig{
		AgentRegistry:  registry,
		Repository:     NewMemoryRepository(),
		EventPublisher: publisher,
		DefaultTimeout: 5 * time.Second,
		MaxConcurrency: 8,
	})

	return engine, testAgent
}

func waitForBaseExecution(engine *Engine, executionID string) (*WorkflowExecution, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		exec, err := engine.GetExecution(executionID)
		if err != nil {
			return nil, err
		}
		if exec.CompletedAt != nil {
			return exec, nil
		}
		if time.Now().After(deadline) {
			return nil, context.DeadlineExceeded
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestParser_ValidateRouteTargetMustDependOnRouter(t *testing.T) {
	parser := NewParser()

	_, err := parser.ParseYAML([]byte(`
name: "invalid-route"
steps:
  - id: classify
    agent: "test-agent"
    action: "stream_text"
    route:
      expression: "{{classify.result.joined}}"
      cases:
        fast: ["fast_path"]

  - id: fast_path
    agent: "test-agent"
    action: "stream_text"
`))
	if err == nil || !strings.Contains(err.Error(), "must depend on router step") {
		t.Fatalf("expected route dependency validation error, got %v", err)
	}
}

func TestEngine_DynamicRouteExecutesSelectedBranch(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestEngine(t, publisher)

	workflowDef := &Workflow{
		ID:   "route-engine",
		Name: "Route Engine",
		Steps: []*Step{
			{
				ID:        "classify",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text": "fast",
				},
				Route: &RouteConfig{
					Expression: "{{classify.result.joined}}",
					Cases: map[string][]string{
						"fast": []string{"fast_path"},
						"slow": []string{"slow_path"},
					},
				},
			},
			{
				ID:        "fast_path",
				AgentName: "test-agent",
				Action:    "stream_text",
				DependsOn: []string{"classify"},
				Parameters: map[string]interface{}{
					"text": "branch-fast",
				},
			},
			{
				ID:        "slow_path",
				AgentName: "test-agent",
				Action:    "stream_text",
				DependsOn: []string{"classify"},
				Parameters: map[string]interface{}{
					"text": "branch-slow",
				},
			},
		},
	}

	execution, err := engine.Execute(context.Background(), workflowDef, nil)
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}

	exec, err := waitForBaseExecution(engine, execution.ID)
	if err != nil {
		t.Fatalf("wait for execution: %v", err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow, got %s (%s)", exec.Status, exec.Error)
	}
	if exec.RouteSelections["classify"] != "fast" {
		t.Fatalf("expected route selection fast, got %q", exec.RouteSelections["classify"])
	}
	if exec.StepExecutions["fast_path"].Status != WorkflowStatusCompleted {
		t.Fatalf("expected fast_path completed, got %s", exec.StepExecutions["fast_path"].Status)
	}
	if exec.StepExecutions["slow_path"].Status != WorkflowStatusSkipped {
		t.Fatalf("expected slow_path skipped, got %s", exec.StepExecutions["slow_path"].Status)
	}
	if !publisher.hasEvent("step_routed", "classify") {
		t.Fatalf("expected step_routed event for classify")
	}
	if !publisher.hasEvent("step_skipped", "slow_path") {
		t.Fatalf("expected step_skipped event for slow_path")
	}
}

func TestStreamingEngine_DynamicRouteExecutesSelectedBranch(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestStreamingEngine(t, publisher)

	workflowDef := &Workflow{
		ID:   "route-streaming",
		Name: "Route Streaming",
		Steps: []*Step{
			{
				ID:        "classify",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "fast",
					"delay_ms": 5,
				},
				Streaming: &StreamingConfig{
					Enabled:        true,
					WaitFor:        "full",
					MinStartTokens: 1,
				},
				Route: &RouteConfig{
					Expression: "{{classify.result.joined}}",
					Cases: map[string][]string{
						"fast": []string{"fast_path"},
						"slow": []string{"slow_path"},
					},
				},
			},
			{
				ID:        "fast_path",
				AgentName: "test-agent",
				Action:    "stream_text",
				DependsOn: []string{"classify"},
				Parameters: map[string]interface{}{
					"text": "branch-fast",
				},
			},
			{
				ID:        "slow_path",
				AgentName: "test-agent",
				Action:    "stream_text",
				DependsOn: []string{"classify"},
				Parameters: map[string]interface{}{
					"text": "branch-slow",
				},
			},
		},
	}

	execution, err := engine.Execute(context.Background(), workflowDef, nil)
	if err != nil {
		t.Fatalf("execute workflow: %v", err)
	}

	exec, err := waitForExecution(engine, execution.ID)
	if err != nil {
		t.Fatalf("wait for execution: %v", err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow, got %s (%s)", exec.Status, exec.Error)
	}
	if exec.RouteSelections["classify"] != "fast" {
		t.Fatalf("expected route selection fast, got %q", exec.RouteSelections["classify"])
	}
	if exec.StepExecutions["fast_path"].Status != WorkflowStatusCompleted {
		t.Fatalf("expected fast_path completed, got %s", exec.StepExecutions["fast_path"].Status)
	}
	if exec.StepExecutions["slow_path"].Status != WorkflowStatusSkipped {
		t.Fatalf("expected slow_path skipped, got %s", exec.StepExecutions["slow_path"].Status)
	}
	if !publisher.hasEvent("step_routed", "classify") {
		t.Fatalf("expected step_routed event for classify")
	}
	if !publisher.hasEvent("step_skipped", "slow_path") {
		t.Fatalf("expected step_skipped event for slow_path")
	}
}
