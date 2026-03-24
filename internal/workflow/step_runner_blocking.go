package workflow

import (
	"context"

	"github.com/google/uuid"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

type blockingStepRunner struct {
	engine         *Engine
	renderedParams map[string]interface{}
}

func newBlockingStepRunner(engine *Engine) *blockingStepRunner {
	return &blockingStepRunner{engine: engine}
}

func (r *blockingStepRunner) StartOptions(_ *Step) StepStartOptions {
	return StepStartOptions{}
}

func (r *blockingStepRunner) Prepare(_ context.Context, step *Step, execution *WorkflowExecution, _ *StepExecution) error {
	params, err := r.engine.renderStepParameters(execution.Context, step)
	if err != nil {
		return err
	}
	r.renderedParams = params
	return nil
}

func (r *blockingStepRunner) RunAttempt(
	ctx context.Context,
	step *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	_ int,
) (*StepAttemptOutcome, error) {
	taskID := uuid.New().String()
	stepExec.TaskID = taskID

	taskInput := &agent.TaskInput{
		TaskID:        taskID,
		Action:        step.Action,
		Parameters:    r.renderedParams,
		Context:       make(map[string]interface{}),
		ContextReader: r.engine.newExecutionContextReader(execution),
	}

	output, err := r.engine.executeAgentTask(ctx, step, taskInput)
	if err != nil {
		return nil, err
	}
	if !output.Success {
		return nil, apperr.Internalf("step execution failed: %s", output.Error).WithCode("workflow_step_execution_failed")
	}

	return &StepAttemptOutcome{Result: output.Result}, nil
}

func (r *blockingStepRunner) HandlePrepareError(
	_ context.Context,
	_ *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	err error,
) error {
	return r.engine.failStep(stepExec, execution, err)
}

func (r *blockingStepRunner) HandleExhausted(
	_ context.Context,
	_ *Step,
	execution *WorkflowExecution,
	stepExec *StepExecution,
	lastErr error,
) error {
	return r.engine.failStep(stepExec, execution, lastErr)
}

func (e *Engine) renderStepParameters(ctxData *ExecutionContext, step *Step) (map[string]interface{}, error) {
	templateEngine := NewTemplateEngine(ctxData)
	renderedParams, err := templateEngine.RenderParameters(step.Parameters)
	if err != nil {
		return nil, apperr.InvalidArgument("failed to render parameters").WithCode("workflow_step_params_render_failed").WithCause(err)
	}

	renderedInputs, err := templateEngine.RenderParameters(step.Inputs)
	if err != nil {
		return nil, apperr.InvalidArgument("failed to resolve explicit inputs").WithCode("workflow_step_inputs_render_failed").WithCause(err)
	}

	merged := mergeParameterMaps(renderedParams, renderedInputs)
	if err := validateStepInputSchema(step, merged); err != nil {
		return nil, err
	}
	return merged, nil
}

func mergeParameterMaps(base, overlay map[string]interface{}) map[string]interface{} {
	switch {
	case base == nil && overlay == nil:
		return nil
	case base == nil:
		return copyMap(overlay)
	case overlay == nil:
		return copyMap(base)
	}

	out := copyMap(base)
	for key, value := range overlay {
		if baseMap, ok := out[key].(map[string]interface{}); ok {
			if overlayMap, ok := value.(map[string]interface{}); ok {
				out[key] = mergeParameterMaps(baseMap, overlayMap)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func validateStepInputSchema(step *Step, params map[string]interface{}) error {
	if step == nil || step.InputSchema == nil || len(step.InputSchema.Required) == 0 {
		return nil
	}
	for _, field := range step.InputSchema.Required {
		if _, ok := params[field]; !ok {
			return apperr.InvalidArgumentf("missing required input field: %s", field).WithCode("workflow_step_input_schema_invalid")
		}
	}
	return nil
}

func (e *Engine) executeAgentTask(ctx context.Context, step *Step, taskInput *agent.TaskInput) (*agent.TaskOutput, error) {
	ag, err := e.resolveStepAgent(step)
	if err != nil {
		return nil, err
	}
	if err := validateAgentInputContract(ag, step, taskInput.Parameters); err != nil {
		return nil, err
	}

	stepCtx := ctx
	if step.Timeout != nil {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
		defer cancel()
	}

	output, err := ag.Execute(stepCtx, taskInput)
	if err != nil {
		return nil, err
	}
	if output != nil && output.Success {
		if err := validateAgentOutputContract(ag, step, output.Result); err != nil {
			return nil, err
		}
	}
	return output, nil
}

func (e *Engine) resolveStepAgent(step *Step) (agent.Agent, error) {
	resolver := agent.NewResolver(e.agentRegistry)
	return resolver.Resolve(agent.ResolveRequest{
		AgentName:  step.AgentName,
		Capability: step.Capability,
	}, agent.ResolveHooks{
		OnNamedAgentNotFound: func(agentName string, _ error) error {
			return apperr.NotFoundf("agent not found: %s", agentName).WithCode("workflow_step_agent_not_found")
		},
		OnCapabilityNotFound: func(capability string) error {
			return apperr.NotFoundf("no agent with capability '%s' found", capability).WithCode("workflow_step_agent_not_found")
		},
		OnRegistryEmpty: func() error {
			return apperr.NotFound("no agents registered").WithCode("workflow_step_agent_not_found")
		},
	})
}
