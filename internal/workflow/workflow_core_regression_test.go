package workflow

import (
	"context"
	"strings"
	"testing"
)

func TestWorkflow_CoreAvailabilityRegression(t *testing.T) {
	t.Run("parser_report_detects_branch_pattern_and_keeps_parse_successful", func(t *testing.T) {
		parser := NewParser()

		wf, err := parser.ParseYAML([]byte(`
name: "branch-pattern"
steps:
  - id: classify
    agent: "test-agent"
    action: "analyze"

  - id: fast_path
    agent: "test-agent"
    action: "process_fast"
    depends_on: ["classify"]
    condition:
      type: "expression"
      expression: "{{classify.result.score}} > 0.8"

  - id: slow_path
    agent: "test-agent"
    action: "process_slow"
    depends_on: ["classify"]
    condition:
      type: "expression"
      expression: "{{classify.result.score}} <= 0.8"
`))
		if err != nil {
			t.Fatalf("parse yaml: %v", err)
		}
		if wf == nil {
			t.Fatalf("expected parsed workflow")
		}

		report := parser.LastReport()
		if len(report.Warnings) == 0 {
			t.Fatalf("expected warning report for branch-like conditional pattern")
		}
		if len(report.Suggestions) == 0 {
			t.Fatalf("expected suggestion report for branch-like conditional pattern")
		}
	})

	t.Run("non_streaming_route_default_branch_is_correct", func(t *testing.T) {
		publisher := &recordingWorkflowEventPublisher{}
		engine, _ := newTestEngine(t, publisher)

		workflowDef := &Workflow{
			ID:   "wf-core-route-default",
			Name: "Core Route Default",
			Steps: []*Step{
				{
					ID:        "classify",
					AgentName: "test-agent",
					Action:    "stream_text",
					Parameters: map[string]interface{}{
						"text": "unknown",
					},
					Route: &RouteConfig{
						Expression: "{{classify.result.joined}}",
						Cases: map[string][]string{
							"fast": []string{"fast_path"},
						},
						Default: []string{"manual_review"},
					},
				},
				{
					ID:        "fast_path",
					AgentName: "test-agent",
					Action:    "stream_text",
					DependsOn: []string{"classify"},
					Parameters: map[string]interface{}{
						"text": "fast-branch",
					},
				},
				{
					ID:        "manual_review",
					AgentName: "test-agent",
					Action:    "stream_text",
					DependsOn: []string{"classify"},
					Parameters: map[string]interface{}{
						"text": "manual-branch",
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
		if exec.RouteSelections["classify"] != "unknown" {
			t.Fatalf("expected route selection unknown, got %q", exec.RouteSelections["classify"])
		}
		if exec.StepExecutions["manual_review"].Status != WorkflowStatusCompleted {
			t.Fatalf("expected manual_review completed, got %s", exec.StepExecutions["manual_review"].Status)
		}
		if exec.StepExecutions["fast_path"].Status != WorkflowStatusSkipped {
			t.Fatalf("expected fast_path skipped, got %s", exec.StepExecutions["fast_path"].Status)
		}
		if !publisher.hasEvent("step_skipped", "fast_path") {
			t.Fatalf("expected step_skipped event for fast_path")
		}
	})

	t.Run("condition_remains_guard_not_primary_branch", func(t *testing.T) {
		engine, _ := newTestEngine(t, nil)

		workflowDef := &Workflow{
			ID:   "wf-core-condition-guard",
			Name: "Core Condition Guard",
			Steps: []*Step{
				{
					ID:        "prepare",
					AgentName: "test-agent",
					Action:    "stream_text",
					Parameters: map[string]interface{}{
						"text": "payload",
					},
				},
				{
					ID:        "optional_step",
					AgentName: "test-agent",
					Action:    "stream_text",
					DependsOn: []string{"prepare"},
					Condition: &Condition{
						Type:       ConditionTypeExpression,
						Expression: "{{run_optional}} == true",
					},
					Parameters: map[string]interface{}{
						"text": "should-not-run",
					},
				},
			},
		}

		execution, err := engine.Execute(context.Background(), workflowDef, map[string]interface{}{
			"run_optional": false,
		})
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
		if exec.StepExecutions["optional_step"].Status != WorkflowStatusSkipped {
			t.Fatalf("expected optional_step skipped, got %s", exec.StepExecutions["optional_step"].Status)
		}
	})

	t.Run("streaming_multi_dependency_route_and_output_flow_are_correct", func(t *testing.T) {
		engine, _ := newTestStreamingEngine(t, nil)

		workflowDef := &Workflow{
			ID: "wf-core-streaming-route",
			Steps: []*Step{
				{
					ID:        "producer_a",
					AgentName: "test-agent",
					Action:    "stream_text",
					Parameters: map[string]interface{}{
						"text":     "ab",
						"delay_ms": 5,
					},
					Streaming: &StreamingConfig{Enabled: true},
				},
				{
					ID:        "producer_b",
					AgentName: "test-agent",
					Action:    "stream_text",
					Parameters: map[string]interface{}{
						"text":     "cd",
						"delay_ms": 5,
					},
					Streaming: &StreamingConfig{Enabled: true},
				},
				{
					ID:        "merge",
					AgentName: "test-agent",
					Action:    "merge_outputs",
					DependsOn: []string{"producer_a", "producer_b"},
					Parameters: map[string]interface{}{
						"steps":      []string{"producer_a", "producer_b"},
						"timeout_ms": 2000,
					},
					Streaming: &StreamingConfig{
						Enabled:        true,
						WaitFor:        "partial",
						MinStartTokens: 1,
						StreamTimeout:  2,
					},
					Route: &RouteConfig{
						Expression: "{{merge.result.joined}}",
						Cases: map[string][]string{
							"ab+cd": []string{"publish"},
						},
						Default: []string{"review"},
					},
				},
				{
					ID:        "publish",
					AgentName: "test-agent",
					Action:    "wait_output",
					DependsOn: []string{"merge"},
					Parameters: map[string]interface{}{
						"step_id":    "merge",
						"timeout_ms": 2000,
					},
					Streaming: &StreamingConfig{
						Enabled: true,
						WaitFor: "full",
					},
				},
				{
					ID:        "review",
					AgentName: "test-agent",
					Action:    "stream_text",
					DependsOn: []string{"merge"},
					Parameters: map[string]interface{}{
						"text": "review",
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
		if got := exec.StepExecutions["merge"].Result["joined"]; got != "ab+cd" {
			t.Fatalf("unexpected merge result: %v", got)
		}
		if exec.StepExecutions["publish"].Status != WorkflowStatusCompleted {
			t.Fatalf("expected publish completed, got %s", exec.StepExecutions["publish"].Status)
		}
		if exec.StepExecutions["review"].Status != WorkflowStatusSkipped {
			t.Fatalf("expected review skipped, got %s", exec.StepExecutions["review"].Status)
		}
	})

	t.Run("mixed_workflow_non_streaming_step_receives_context_reader", func(t *testing.T) {
		engine, _ := newTestStreamingEngine(t, nil)

		workflowDef := &Workflow{
			ID: "wf-core-mixed-context-reader",
			Steps: []*Step{
				{
					ID:        "producer",
					AgentName: "test-agent",
					Action:    "stream_text",
					Parameters: map[string]interface{}{
						"text":     "mixed-ok",
						"delay_ms": 5,
					},
					Streaming: &StreamingConfig{Enabled: true},
				},
				{
					ID:        "consumer",
					AgentName: "test-agent",
					Action:    "wait_output",
					DependsOn: []string{"producer"},
					Parameters: map[string]interface{}{
						"step_id":    "producer",
						"timeout_ms": 2000,
					},
					// 故意不启用 streaming，验证普通 executeStep 也会注入 ContextReader
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
		if got := exec.StepExecutions["consumer"].Result["joined"]; got != "mixed-ok" {
			t.Fatalf("unexpected consumer result: %v", got)
		}
	})

	t.Run("rollback_cleans_outputs_aliases_and_buffers", func(t *testing.T) {
		engine, _ := newTestStreamingEngine(t, nil)

		workflowDef := &Workflow{
			ID: "wf-core-rollback-cleanup",
			OnFailure: &FailurePolicy{
				Rollback: true,
			},
			Steps: []*Step{
				{
					ID:          "prepare",
					AgentName:   "test-agent",
					Action:      "stream_text",
					OutputAlias: "prepared_output",
					Parameters: map[string]interface{}{
						"text":     "cleanup",
						"delay_ms": 5,
					},
					Streaming: &StreamingConfig{Enabled: true},
				},
				{
					ID:        "boom",
					AgentName: "test-agent",
					Action:    "fail",
					DependsOn: []string{"prepare"},
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
		if _, ok := exec.Context.GetStepOutput("prepare"); ok {
			t.Fatalf("expected prepare output removed after rollback")
		}
		if _, ok := exec.Context.GetVariable("prepared_output"); ok {
			t.Fatalf("expected prepared_output alias removed after rollback")
		}

		status := engine.GetStreamingStatus(execution.ID)
		if got := status["buffer_count"].(int); got != 0 {
			t.Fatalf("expected no remaining buffers after rollback cleanup, got %d", got)
		}
	})

	t.Run("foreach_step_fan_out_and_aggregate_results", func(t *testing.T) {
		engine, _ := newTestEngine(t, nil)

		workflowDef := &Workflow{
			ID:   "wf-core-foreach",
			Name: "Core Foreach",
			Steps: []*Step{
				{
					ID:        "extract_users",
					AgentName: "test-agent",
					Action:    "return_result",
					Parameters: map[string]interface{}{
						"result": map[string]interface{}{
							"users": []interface{}{
								map[string]interface{}{"id": "u1"},
								map[string]interface{}{"id": "u2"},
								map[string]interface{}{"id": "u3"},
							},
						},
					},
				},
				{
					ID:        "enrich_user",
					AgentName: "test-agent",
					Action:    "echo_value",
					DependsOn: []string{"extract_users"},
					Foreach: &ForeachConfig{
						From:        "{{extract_users.result.users}}",
						ItemAs:      "user",
						IndexAs:     "user_index",
						MaxParallel: 2,
					},
					Parameters: map[string]interface{}{
						"value": "{{user.id}}",
						"index": "{{user_index}}",
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

		enrichExec := exec.StepExecutions["enrich_user"]
		if enrichExec == nil {
			t.Fatalf("missing enrich_user execution")
		}
		if got := enrichExec.Result["count"]; got != 3 {
			t.Fatalf("expected foreach result count 3, got %v", got)
		}

		items, ok := enrichExec.Result["items"].([]map[string]interface{})
		if !ok {
			t.Fatalf("expected foreach items slice, got %#v", enrichExec.Result["items"])
		}
		if len(items) != 3 {
			t.Fatalf("expected 3 foreach items, got %d", len(items))
		}
		if items[0]["value"] != "u1" || items[1]["value"] != "u2" || items[2]["value"] != "u3" {
			t.Fatalf("unexpected foreach values: %+v", items)
		}
	})

	t.Run("fan_in_step_can_consume_foreach_items_array", func(t *testing.T) {
		engine, _ := newTestEngine(t, nil)

		workflowDef := &Workflow{
			ID:   "wf-core-fan-in",
			Name: "Core Fan In",
			Steps: []*Step{
				{
					ID:        "extract_users",
					AgentName: "test-agent",
					Action:    "return_result",
					Parameters: map[string]interface{}{
						"result": map[string]interface{}{
							"users": []interface{}{
								map[string]interface{}{"id": "u1"},
								map[string]interface{}{"id": "u2"},
								map[string]interface{}{"id": "u3"},
							},
						},
					},
				},
				{
					ID:        "enrich_user",
					AgentName: "test-agent",
					Action:    "echo_value",
					DependsOn: []string{"extract_users"},
					Foreach: &ForeachConfig{
						From:        "{{extract_users.result.users}}",
						ItemAs:      "user",
						IndexAs:     "user_index",
						MaxParallel: 2,
					},
					Parameters: map[string]interface{}{
						"value": "{{user.id}}",
						"index": "{{user_index}}",
					},
				},
				{
					ID:        "aggregate_profiles",
					AgentName: "test-agent",
					Action:    "collect_item_values",
					DependsOn: []string{"enrich_user"},
					Parameters: map[string]interface{}{
						"items": "{{enrich_user.result.items}}",
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

		aggregateExec := exec.StepExecutions["aggregate_profiles"]
		if aggregateExec == nil {
			t.Fatalf("missing aggregate_profiles execution")
		}
		if aggregateExec.Result["count"] != 3 {
			t.Fatalf("expected fan-in count 3, got %+v", aggregateExec.Result)
		}
		if aggregateExec.Result["joined"] != "u1+u2+u3" {
			t.Fatalf("unexpected fan-in joined result: %+v", aggregateExec.Result)
		}
	})

	t.Run("invalid_route_configuration_fails_fast", func(t *testing.T) {
		parser := NewParser()

		_, err := parser.ParseYAML([]byte(`
name: "invalid-fast-fail"
steps:
  - id: router
    agent: "test-agent"
    action: "stream_text"
    route:
      expression: "{{router.result.joined}}"
      cases:
        fast: ["missing_step"]
`))
		if err == nil {
			t.Fatalf("expected parser validation error for invalid route target")
		}
		if !strings.Contains(err.Error(), "route target does not exist") {
			t.Fatalf("unexpected parser error: %v", err)
		}
	})
}
