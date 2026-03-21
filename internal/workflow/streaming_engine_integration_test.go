package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
)

type recordingWorkflowEventPublisher struct {
	mu     sync.Mutex
	events []recordedWorkflowEvent
}

type recordedWorkflowEvent struct {
	EventType string
	StepID    string
	At        time.Time
}

func (p *recordingWorkflowEventPublisher) PublishWorkflowEvent(
	executionID, eventType, status, message string,
	data map[string]interface{},
) {
	p.mu.Lock()
	defer p.mu.Unlock()

	stepID, _ := data["step_id"].(string)
	p.events = append(p.events, recordedWorkflowEvent{
		EventType: eventType,
		StepID:    stepID,
		At:        time.Now(),
	})
}

func (p *recordingWorkflowEventPublisher) lastEventTime(eventType, stepID string) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := len(p.events) - 1; i >= 0; i-- {
		if p.events[i].EventType == eventType && p.events[i].StepID == stepID {
			return p.events[i].At
		}
	}
	return time.Time{}
}

func (p *recordingWorkflowEventPublisher) hasEvent(eventType, stepID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, event := range p.events {
		if event.EventType == eventType && event.StepID == stepID {
			return true
		}
	}
	return false
}

type integrationTestAgent struct {
	name string

	mu                sync.Mutex
	compensationCalls []map[string]interface{}
	actionCalls       []string
}

func newIntegrationTestAgent(name string) *integrationTestAgent {
	return &integrationTestAgent{name: name}
}

func (a *integrationTestAgent) GetName() string { return a.name }

func (a *integrationTestAgent) GetDescription() string { return "integration test agent" }

func (a *integrationTestAgent) GetCapabilities() []string { return []string{"test"} }

func (a *integrationTestAgent) Init(config *agent.Config) error { return nil }

func (a *integrationTestAgent) Shutdown() error { return nil }

func (a *integrationTestAgent) Execute(ctx context.Context, input *agent.TaskInput) (*agent.TaskOutput, error) {
	a.recordActionCall(input)

	switch input.Action {
	case "stream_text":
		return a.executeStreamText(ctx, input)
	case "delayed_stream_text":
		return a.executeDelayedStreamText(ctx, input)
	case "stream_then_fail":
		return a.executeStreamThenFail(ctx, input)
	case "merge_outputs":
		return a.executeMergeOutputs(ctx, input)
	case "wait_output":
		return a.executeWaitOutput(ctx, input)
	case "fail":
		return nil, fmt.Errorf("simulated failure")
	case "compensate":
		return a.executeCompensate(input)
	default:
		return nil, fmt.Errorf("unsupported action: %s", input.Action)
	}
}

func (a *integrationTestAgent) recordActionCall(input *agent.TaskInput) {
	text, _ := input.Parameters["text"].(string)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.actionCalls = append(a.actionCalls, fmt.Sprintf("%s:%s", input.Action, text))
}

func (a *integrationTestAgent) executeStreamText(ctx context.Context, input *agent.TaskInput) (*agent.TaskOutput, error) {
	text, _ := input.Parameters["text"].(string)
	delay := getIntParam(input.Parameters, "delay_ms", 10)

	var built strings.Builder
	for _, ch := range text {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		built.WriteRune(ch)
		if input.StreamCallback != nil {
			input.StreamCallback(map[string]interface{}{
				"token":  string(ch),
				"joined": built.String(),
			})
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}

	return &agent.TaskOutput{
		TaskID:  input.TaskID,
		Success: true,
		Result: map[string]interface{}{
			"joined": built.String(),
			"length": len(text),
		},
	}, nil
}

func (a *integrationTestAgent) executeDelayedStreamText(ctx context.Context, input *agent.TaskInput) (*agent.TaskOutput, error) {
	initialDelay := getIntParam(input.Parameters, "initial_delay_ms", 0)
	if initialDelay > 0 {
		select {
		case <-time.After(time.Duration(initialDelay) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return a.executeStreamText(ctx, input)
}

func (a *integrationTestAgent) executeStreamThenFail(ctx context.Context, input *agent.TaskInput) (*agent.TaskOutput, error) {
	text, _ := input.Parameters["text"].(string)
	delay := getIntParam(input.Parameters, "delay_ms", 10)
	failAfter := getIntParam(input.Parameters, "fail_after", 1)

	var built strings.Builder
	for i, ch := range text {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		built.WriteRune(ch)
		if input.StreamCallback != nil {
			input.StreamCallback(map[string]interface{}{
				"token":  string(ch),
				"joined": built.String(),
			})
		}
		if i+1 >= failAfter {
			return nil, fmt.Errorf("simulated failure after %d chunks", failAfter)
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}

	return nil, fmt.Errorf("expected failure did not happen")
}

func (a *integrationTestAgent) executeMergeOutputs(ctx context.Context, input *agent.TaskInput) (*agent.TaskOutput, error) {
	steps := getStringSliceParam(input.Parameters, "steps")
	timeout := time.Duration(getIntParam(input.Parameters, "timeout_ms", 2000)) * time.Millisecond
	deadline := time.Now().Add(timeout)

	for {
		allDone := true
		for _, stepID := range steps {
			if !input.ContextReader.IsStepCompleted(stepID) {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for dependencies completion")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	values := make([]string, 0, len(steps))
	for _, stepID := range steps {
		value, err := input.ContextReader.WaitForField(stepID, "joined", timeout)
		if err != nil {
			return nil, err
		}
		values = append(values, fmt.Sprint(value))
	}

	return &agent.TaskOutput{
		TaskID:  input.TaskID,
		Success: true,
		Result: map[string]interface{}{
			"joined": strings.Join(values, "+"),
			"parts":  values,
		},
	}, nil
}

func (a *integrationTestAgent) executeWaitOutput(ctx context.Context, input *agent.TaskInput) (*agent.TaskOutput, error) {
	stepID, _ := input.Parameters["step_id"].(string)
	timeout := time.Duration(getIntParam(input.Parameters, "timeout_ms", 2000)) * time.Millisecond
	deadline := time.Now().Add(timeout)

	for {
		if input.ContextReader.IsStepCompleted(stepID) {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for step %s completion", stepID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}

	value, err := input.ContextReader.WaitForField(stepID, "joined", timeout)
	if err != nil {
		return nil, err
	}

	return &agent.TaskOutput{
		TaskID:  input.TaskID,
		Success: true,
		Result: map[string]interface{}{
			"joined": fmt.Sprint(value),
		},
	}, nil
}

func (a *integrationTestAgent) executeCompensate(input *agent.TaskInput) (*agent.TaskOutput, error) {
	params := copyMap(input.Parameters)

	a.mu.Lock()
	a.compensationCalls = append(a.compensationCalls, params)
	a.mu.Unlock()

	return &agent.TaskOutput{
		TaskID:  input.TaskID,
		Success: true,
		Result: map[string]interface{}{
			"compensated": true,
		},
	}, nil
}

func (a *integrationTestAgent) compensationSnapshot() []map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]map[string]interface{}, len(a.compensationCalls))
	copy(out, a.compensationCalls)
	return out
}

func (a *integrationTestAgent) countActionCalls(prefix string) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	count := 0
	for _, call := range a.actionCalls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

func getIntParam(params map[string]interface{}, key string, defaultValue int) int {
	if raw, ok := params[key]; ok {
		switch v := raw.(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return defaultValue
}

func getStringSliceParam(params map[string]interface{}, key string) []string {
	raw, ok := params[key]
	if !ok {
		return nil
	}

	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		out := make([]string, 0, len(values))
		for _, item := range values {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

func newTestStreamingEngine(t *testing.T, publisher EventPublisher) (*StreamingEngine, *integrationTestAgent) {
	t.Helper()

	registry := agent.NewRegistry()
	testAgent := newIntegrationTestAgent("test-agent")
	if err := registry.Register(testAgent); err != nil {
		t.Fatalf("register test agent: %v", err)
	}

	engine := NewStreamingEngine(EngineConfig{
		AgentRegistry:  registry,
		Repository:     NewMemoryRepository(),
		EventPublisher: publisher,
		DefaultTimeout: 5 * time.Second,
		MaxConcurrency: 8,
	})

	return engine, testAgent
}

func waitForExecution(engine *StreamingEngine, executionID string) (*WorkflowExecution, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		exec, err := engine.GetExecution(executionID)
		if err != nil {
			return nil, fmt.Errorf("get execution %s: %w", executionID, err)
		}
		if exec.Status != WorkflowStatusRunning {
			return exec, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting execution %s", executionID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStreamingWorkflow_HappyPathMultiDependencyPartial(t *testing.T) {
	engine, _ := newTestStreamingEngine(t, nil)

	workflowDef := &Workflow{
		ID: "wf-partial-happy",
		Steps: []*Step{
			{
				ID:        "producer_a",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "abc",
					"delay_ms": 10,
				},
				Streaming: &StreamingConfig{Enabled: true},
			},
			{
				ID:        "producer_b",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "xyz",
					"delay_ms": 10,
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
					MinStartTokens: 2,
					StreamTimeout:  2,
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow, got %s (%s)", exec.Status, exec.Error)
	}

	mergeExec := exec.StepExecutions["merge"]
	if mergeExec == nil || mergeExec.Result["joined"] != "abc+xyz" {
		t.Fatalf("unexpected merge result: %+v", mergeExec)
	}
}

func TestStreamingWorkflow_FullWaitStartsAfterProducerCompleted(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestStreamingEngine(t, publisher)

	workflowDef := &Workflow{
		ID: "wf-full-boundary",
		Steps: []*Step{
			{
				ID:        "producer",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "abcd",
					"delay_ms": 20,
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow, got %s (%s)", exec.Status, exec.Error)
	}

	producerCompletedAt := publisher.lastEventTime("step_completed", "producer")
	consumerStartedAt := publisher.lastEventTime("step_started", "consumer")
	if producerCompletedAt.IsZero() || consumerStartedAt.IsZero() {
		t.Fatalf("missing recorded events: producer=%v consumer=%v", producerCompletedAt, consumerStartedAt)
	}
	if consumerStartedAt.Before(producerCompletedAt) {
		t.Fatalf("consumer started before producer completed: consumer=%v producer=%v", consumerStartedAt, producerCompletedAt)
	}
}

func TestStreamingWorkflow_FailureRollbackCompensation(t *testing.T) {
	engine, testAgent := newTestStreamingEngine(t, nil)

	workflowDef := &Workflow{
		ID: "wf-rollback",
		OnFailure: &FailurePolicy{
			Rollback: true,
		},
		Steps: []*Step{
			{
				ID:                 "prepare",
				AgentName:          "test-agent",
				Action:             "stream_text",
				CompensationAction: "compensate",
				Parameters: map[string]interface{}{
					"text":     "rollback-me",
					"delay_ms": 5,
				},
				CompensationParams: map[string]interface{}{
					"reason": "rollback",
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed workflow, got %s", exec.Status)
	}

	if _, ok := exec.Context.GetStepOutput("prepare"); ok {
		t.Fatalf("prepare output should be removed after rollback")
	}

	calls := testAgent.compensationSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 compensation call, got %d", len(calls))
	}
	originalOutput, ok := calls[0]["original_output"].(map[string]interface{})
	if !ok || originalOutput["joined"] != "rollback-me" {
		t.Fatalf("unexpected compensation payload: %+v", calls[0])
	}

	status := engine.GetStreamingStatus(execution.ID)
	if got := status["buffer_count"].(int); got != 0 {
		t.Fatalf("expected no remaining buffers after cleanup, got %d", got)
	}
}

func TestStreamingWorkflow_ConcurrentExecutionsAreIsolated(t *testing.T) {
	engine, _ := newTestStreamingEngine(t, nil)

	workflowDef := &Workflow{
		ID: "wf-concurrent-isolation",
		Steps: []*Step{
			{
				ID:        "producer",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "{{input}}",
					"delay_ms": 15,
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
				Streaming: &StreamingConfig{
					Enabled:        true,
					WaitFor:        "partial",
					MinStartTokens: 2,
					StreamTimeout:  2,
				},
			},
		},
	}

	type runResult struct {
		expected string
		exec     *WorkflowExecution
		err      error
	}

	results := make(chan runResult, 2)
	run := func(input string) {
		execution, err := engine.Execute(context.Background(), workflowDef, map[string]interface{}{
			"input": input,
		})
		if err != nil {
			results <- runResult{expected: input, err: err}
			return
		}
		exec, waitErr := waitForExecution(engine, execution.ID)
		results <- runResult{expected: input, exec: exec, err: waitErr}
	}

	go run("alpha")
	go run("beta")

	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("execute concurrent workflow: %v", result.err)
		}
		if result.exec.Status != WorkflowStatusCompleted {
			t.Fatalf("expected completed workflow, got %s (%s)", result.exec.Status, result.exec.Error)
		}

		consumerExec := result.exec.StepExecutions["consumer"]
		if consumerExec == nil {
			t.Fatalf("missing consumer execution for input %s", result.expected)
		}
		if got := consumerExec.Result["joined"]; got != result.expected {
			t.Fatalf("concurrent execution isolation failed: expected %q, got %v", result.expected, got)
		}
	}
}

func TestStreamingWorkflow_OnErrorContinueReturnsPartialData(t *testing.T) {
	engine, _ := newTestStreamingEngine(t, nil)

	workflowDef := &Workflow{
		ID: "wf-onerror-continue",
		Steps: []*Step{
			{
				ID:        "producer",
				AgentName: "test-agent",
				Action:    "stream_then_fail",
				Parameters: map[string]interface{}{
					"text":       "abcd",
					"delay_ms":   5,
					"fail_after": 2,
				},
				Streaming: &StreamingConfig{
					Enabled: true,
					OnError: "continue",
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow with partial data, got %s (%s)", exec.Status, exec.Error)
	}

	producerExec := exec.StepExecutions["producer"]
	if producerExec == nil {
		t.Fatalf("missing producer execution")
	}
	if producerExec.Result["_partial"] != true {
		t.Fatalf("expected partial flag in result, got %+v", producerExec.Result)
	}
	if producerExec.Result["joined"] != "ab" {
		t.Fatalf("expected partial joined output 'ab', got %+v", producerExec.Result)
	}
}

func TestStreamingWorkflow_OnErrorFallbackSucceeds(t *testing.T) {
	engine, _ := newTestStreamingEngine(t, nil)

	workflowDef := &Workflow{
		ID: "wf-onerror-fallback",
		Steps: []*Step{
			{
				ID:        "producer",
				AgentName: "test-agent",
				Action:    "delayed_stream_text",
				Parameters: map[string]interface{}{
					"text":             "fallback-ok",
					"initial_delay_ms": 1200,
					"delay_ms":         5,
				},
				Streaming: &StreamingConfig{
					Enabled:       true,
					OnError:       "fallback",
					StreamTimeout: 1,
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow after fallback, got %s (%s)", exec.Status, exec.Error)
	}

	producerExec := exec.StepExecutions["producer"]
	if producerExec == nil || producerExec.Result["joined"] != "fallback-ok" {
		t.Fatalf("unexpected fallback result: %+v", producerExec)
	}
}

func TestStreamingWorkflow_ResumeExecutionFromCheckpoint(t *testing.T) {
	engine, testAgent := newTestStreamingEngine(t, nil)

	sourceWorkflow := &Workflow{
		ID: "wf-resume-source",
		Steps: []*Step{
			{
				ID:        "prepare",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "checkpoint",
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

	sourceExecution, err := engine.Execute(context.Background(), sourceWorkflow, nil)
	if err != nil {
		t.Fatalf("execute source workflow: %v", err)
	}

	failedExec, err := waitForExecution(engine, sourceExecution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedExec.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed source workflow, got %s", failedExec.Status)
	}

	resumeWorkflow := &Workflow{
		ID: "wf-resume-source",
		Steps: []*Step{
			{
				ID:        "prepare",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "checkpoint",
					"delay_ms": 5,
				},
				Streaming: &StreamingConfig{Enabled: true},
			},
			{
				ID:        "recover",
				AgentName: "test-agent",
				Action:    "wait_output",
				DependsOn: []string{"prepare"},
				Parameters: map[string]interface{}{
					"step_id":    "prepare",
					"timeout_ms": 2000,
				},
				Streaming: &StreamingConfig{
					Enabled: true,
					WaitFor: "full",
				},
			},
		},
	}

	resumedExecution, err := engine.ResumeExecution(context.Background(), resumeWorkflow, failedExec.ID, nil)
	if err != nil {
		t.Fatalf("resume execution: %v", err)
	}

	resumedExec, err := waitForExecution(engine, resumedExecution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumedExec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected resumed workflow completed, got %s (%s)", resumedExec.Status, resumedExec.Error)
	}

	if resumedExec.StepExecutions["prepare"] == nil || resumedExec.StepExecutions["prepare"].Status != WorkflowStatusCompleted {
		t.Fatalf("expected restored prepare step execution, got %+v", resumedExec.StepExecutions["prepare"])
	}

	recoverExec := resumedExec.StepExecutions["recover"]
	if recoverExec == nil || recoverExec.Result["joined"] != "checkpoint" {
		t.Fatalf("unexpected recover result: %+v", recoverExec)
	}

	if got := testAgent.countActionCalls("stream_text:checkpoint"); got != 1 {
		t.Fatalf("expected prepare step to run once before resume, got %d", got)
	}
}

func TestStreamingWorkflow_BufferOverflowFailsAndEmitsEvent(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestStreamingEngine(t, publisher)

	workflowDef := &Workflow{
		ID: "wf-buffer-overflow",
		Steps: []*Step{
			{
				ID:        "producer",
				AgentName: "test-agent",
				Action:    "stream_text",
				Parameters: map[string]interface{}{
					"text":     "abcdef",
					"delay_ms": 300,
				},
				Streaming: &StreamingConfig{
					Enabled:    true,
					BufferSize: 512,
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed workflow on buffer overflow, got %s (%s)", exec.Status, exec.Error)
	}
	if !publisher.hasEvent("step_failed", "producer") {
		t.Fatalf("expected step_failed event for producer")
	}
}

func TestStreamingWorkflow_FallbackEmitsErrorEvent(t *testing.T) {
	publisher := &recordingWorkflowEventPublisher{}
	engine, _ := newTestStreamingEngine(t, publisher)

	workflowDef := &Workflow{
		ID: "wf-fallback-event",
		Steps: []*Step{
			{
				ID:        "producer",
				AgentName: "test-agent",
				Action:    "delayed_stream_text",
				Parameters: map[string]interface{}{
					"text":             "fallback-event",
					"initial_delay_ms": 1200,
					"delay_ms":         5,
				},
				Streaming: &StreamingConfig{
					Enabled:       true,
					OnError:       "fallback",
					StreamTimeout: 1,
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed workflow after fallback, got %s (%s)", exec.Status, exec.Error)
	}
	if !publisher.hasEvent("step_stream_error_fallback", "producer") {
		t.Fatalf("expected step_stream_error_fallback event for producer")
	}
}

func TestStreamingWorkflow_RollbackRemovesOutputAliasVariable(t *testing.T) {
	engine, _ := newTestStreamingEngine(t, nil)

	workflowDef := &Workflow{
		ID: "wf-rollback-alias-cleanup",
		OnFailure: &FailurePolicy{
			Rollback: true,
		},
		Steps: []*Step{
			{
				ID:          "prepare",
				AgentName:   "test-agent",
				Action:      "stream_text",
				OutputAlias: "prepare_alias",
				Parameters: map[string]interface{}{
					"text":     "alias-data",
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
		t.Fatal(err)
	}
	if exec.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed workflow, got %s (%s)", exec.Status, exec.Error)
	}

	if _, ok := exec.Context.GetVariable("prepare_alias"); ok {
		t.Fatalf("expected output alias variable to be removed after rollback")
	}
}
