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
		exec, err := engine.GetExecution(context.Background(), executionID)
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

func TestTemplateEngine_RenderFloat32WithoutPanic(t *testing.T) {
	engine := NewTemplateEngine(NewExecutionContext(map[string]interface{}{
		"score": float32(1.25),
	}))

	rendered, err := engine.Render("{{score}}")
	if err != nil {
		t.Fatalf("render float32: %v", err)
	}
	if rendered != "1.25" {
		t.Fatalf("expected rendered float32 to be 1.25, got %q", rendered)
	}
}

func TestTemplateEngine_EvaluateConditionUsesTypedOperands(t *testing.T) {
	ctx := NewExecutionContext(map[string]interface{}{
		"run_optional": true,
		"score":        float32(0.85),
		"user": map[string]interface{}{
			"role": "admin",
		},
	})
	ctx.SetStepOutput("classify", map[string]interface{}{
		"score":   0.91,
		"enabled": true,
		"count":   3,
	})
	engine := NewTemplateEngine(ctx)

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{name: "float32 numeric compare", expr: "{{score}} > 0.8", want: true},
		{name: "step output numeric compare", expr: "{{classify.score}} >= 0.9", want: true},
		{name: "bool compare", expr: "{{run_optional}} == true", want: true},
		{name: "nested global compare", expr: "{{user.role}} == \"admin\"", want: true},
		{name: "direct context expression compare", expr: "user.role == \"admin\"", want: true},
		{name: "step field compare", expr: "{{classify.result.count}} == 3", want: true},
		{name: "truthy operand", expr: "{{classify.enabled}}", want: true},
		{name: "false compare", expr: "{{classify.score}} < 0.5", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := engine.EvaluateCondition(tc.expr)
			if err != nil {
				t.Fatalf("evaluate condition %q: %v", tc.expr, err)
			}
			if got != tc.want {
				t.Fatalf("condition %q = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEngine_RuntimeFailureStoresStructuredErrorCode(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestEngine(t, publisher)

	workflowDef := &Workflow{
		ID:   "runtime-error-code",
		Name: "Runtime Error Code",
		Steps: []*Step{
			{
				ID:        "missing_agent_step",
				AgentName: "missing-agent",
				Action:    "echo",
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
	if exec.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed workflow, got %s (%s)", exec.Status, exec.Error)
	}

	stepExec := exec.StepExecutions["missing_agent_step"]
	if stepExec == nil {
		t.Fatal("expected step execution record")
	}
	if stepExec.Metadata["error_code"] != "workflow_step_agent_not_found" {
		t.Fatalf("unexpected error code metadata: %#v", stepExec.Metadata)
	}
}

func TestStreamingEngine_RuntimeFailureStoresStructuredErrorCode(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestStreamingEngine(t, publisher)

	workflowDef := &Workflow{
		ID:   "runtime-streaming-error-code",
		Name: "Runtime Streaming Error Code",
		Steps: []*Step{
			{
				ID:        "missing_agent_stream_step",
				AgentName: "missing-agent",
				Action:    "stream_text",
				Streaming: &StreamingConfig{
					Enabled: true,
					WaitFor: "full",
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
	if exec.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed workflow, got %s (%s)", exec.Status, exec.Error)
	}

	stepExec := exec.StepExecutions["missing_agent_stream_step"]
	if stepExec == nil {
		t.Fatal("expected step execution record")
	}
	if stepExec.Metadata["error_code"] != "workflow_step_agent_not_found" {
		t.Fatalf("unexpected error code metadata: %#v", stepExec.Metadata)
	}
}
