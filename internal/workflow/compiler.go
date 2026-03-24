package workflow

import (
	"fmt"
	"sort"
	"strings"
)

type BindingSourceKind string

const (
	BindingSourceVariable BindingSourceKind = "variable"
	BindingSourceStep     BindingSourceKind = "step"
	BindingSourceLiteral  BindingSourceKind = "literal"
)

type DataBinding struct {
	TargetPath   string            `json:"target_path"`
	Expression   string            `json:"expression"`
	SourceKind   BindingSourceKind `json:"source_kind"`
	SourceStepID string            `json:"source_step_id,omitempty"`
	SourcePath   string            `json:"source_path,omitempty"`
	Template     bool              `json:"template"`
}

type CompiledWorkflow struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	Source    *Workflow                 `json:"-"`
	OnFailure *FailurePolicy            `json:"-"`
	Steps     []*CompiledStep           `json:"steps"`
	StepByID  map[string]*CompiledStep  `json:"-"`
	Edges     []CompiledEdge            `json:"edges,omitempty"`
	Incoming  map[string][]CompiledEdge `json:"-"`
	Outgoing  map[string][]CompiledEdge `json:"-"`
	DAG       *DAG                      `json:"-"`
}

type CompiledStep struct {
	ID                string        `json:"id"`
	Runtime           *Step         `json:"runtime"`
	Bindings          []DataBinding `json:"bindings,omitempty"`
	DefaultedFields   []string      `json:"defaulted_fields,omitempty"`
	ReferencedStepIDs []string      `json:"referenced_step_ids,omitempty"`
}

type CompiledEdgeKind string

const (
	CompiledEdgeDependency CompiledEdgeKind = "dependency"
	CompiledEdgeRoute      CompiledEdgeKind = "route"
	CompiledEdgeForeach    CompiledEdgeKind = "foreach"
)

type CompiledEdge struct {
	FromStepID    string           `json:"from_step_id"`
	ToStepID      string           `json:"to_step_id"`
	Kind          CompiledEdgeKind `json:"kind"`
	RouteKey      string           `json:"route_key,omitempty"`
	DefaultRoute  bool             `json:"default_route,omitempty"`
	BindingTarget string           `json:"binding_target,omitempty"`
}

type Compiler struct{}

func NewCompiler() *Compiler {
	return &Compiler{}
}

func CompileWorkflow(workflow *Workflow) (*CompiledWorkflow, error) {
	return NewCompiler().Compile(workflow)
}

func (c *Compiler) Compile(workflow *Workflow) (*CompiledWorkflow, error) {
	if workflow != nil && workflow.Name == "" && workflow.ID != "" {
		workflow.Name = workflow.ID
	}
	if err := ValidateDefinition(workflow); err != nil {
		return nil, err
	}

	compiled := &CompiledWorkflow{
		ID:        workflow.ID,
		Name:      workflow.Name,
		Source:    workflow,
		OnFailure: workflow.OnFailure,
		Steps:     make([]*CompiledStep, 0, len(workflow.Steps)),
		StepByID:  make(map[string]*CompiledStep, len(workflow.Steps)),
		Incoming:  make(map[string][]CompiledEdge),
		Outgoing:  make(map[string][]CompiledEdge),
	}

	runtimeSteps := make([]*Step, 0, len(workflow.Steps))
	for _, step := range workflow.Steps {
		runtimeStep, defaulted := normalizeCompiledStep(step)
		bindings := collectStepBindings(runtimeStep)

		referencedSet := make(map[string]struct{})
		for _, binding := range bindings {
			if binding.SourceKind == BindingSourceStep {
				referencedSet[binding.SourceStepID] = struct{}{}
			}
		}

		referencedSteps := make([]string, 0, len(referencedSet))
		for stepID := range referencedSet {
			referencedSteps = append(referencedSteps, stepID)
		}
		sort.Strings(referencedSteps)

		compiledStep := &CompiledStep{
			ID:                runtimeStep.ID,
			Runtime:           runtimeStep,
			Bindings:          bindings,
			DefaultedFields:   defaulted,
			ReferencedStepIDs: referencedSteps,
		}
		compiled.Steps = append(compiled.Steps, compiledStep)
		compiled.StepByID[compiledStep.ID] = compiledStep
		runtimeSteps = append(runtimeSteps, runtimeStep)
	}

	compiled.buildEdges()

	for _, step := range compiled.Steps {
		if err := validateCompiledStepReferences(step); err != nil {
			return nil, err
		}
		for _, depID := range step.Runtime.DependsOn {
			if _, ok := compiled.StepByID[depID]; !ok {
				return nil, fmt.Errorf("compiled step %s depends on unknown step %s", step.ID, depID)
			}
		}
		for _, binding := range step.Bindings {
			if binding.SourceKind == BindingSourceStep {
				if _, ok := compiled.StepByID[binding.SourceStepID]; !ok {
					return nil, fmt.Errorf("compiled step %s references unknown step %s", step.ID, binding.SourceStepID)
				}
			}
		}
	}

	dag, err := NewDAG(runtimeSteps)
	if err != nil {
		return nil, err
	}
	compiled.DAG = dag

	return compiled, nil
}

func (w *CompiledWorkflow) buildEdges() {
	if w == nil {
		return
	}

	w.Edges = w.Edges[:0]
	for key := range w.Incoming {
		delete(w.Incoming, key)
	}
	for key := range w.Outgoing {
		delete(w.Outgoing, key)
	}

	for _, step := range w.Steps {
		if step == nil || step.Runtime == nil {
			continue
		}
		for _, depID := range step.Runtime.DependsOn {
			w.addEdge(CompiledEdge{
				FromStepID: depID,
				ToStepID:   step.ID,
				Kind:       CompiledEdgeDependency,
			})
		}
		if step.Runtime.Route != nil {
			for routeKey, targets := range step.Runtime.Route.Cases {
				for _, targetID := range targets {
					w.addEdge(CompiledEdge{
						FromStepID: step.ID,
						ToStepID:   targetID,
						Kind:       CompiledEdgeRoute,
						RouteKey:   routeKey,
					})
				}
			}
			for _, targetID := range step.Runtime.Route.Default {
				w.addEdge(CompiledEdge{
					FromStepID:   step.ID,
					ToStepID:     targetID,
					Kind:         CompiledEdgeRoute,
					DefaultRoute: true,
				})
			}
		}
		if step.Runtime.Foreach != nil {
			for _, binding := range step.Bindings {
				if binding.TargetPath == "foreach.from" && binding.SourceKind == BindingSourceStep {
					w.addEdge(CompiledEdge{
						FromStepID:    binding.SourceStepID,
						ToStepID:      step.ID,
						Kind:          CompiledEdgeForeach,
						BindingTarget: binding.TargetPath,
					})
				}
			}
		}
	}
}

func (w *CompiledWorkflow) addEdge(edge CompiledEdge) {
	w.Edges = append(w.Edges, edge)
	w.Incoming[edge.ToStepID] = append(w.Incoming[edge.ToStepID], edge)
	w.Outgoing[edge.FromStepID] = append(w.Outgoing[edge.FromStepID], edge)
}

func validateCompiledStepReferences(step *CompiledStep) error {
	if step == nil || step.Runtime == nil {
		return nil
	}

	allowed := make(map[string]struct{}, len(step.Runtime.DependsOn)+1)
	allowed[step.ID] = struct{}{}
	for _, depID := range step.Runtime.DependsOn {
		allowed[depID] = struct{}{}
	}

	for _, binding := range step.Bindings {
		if binding.SourceKind != BindingSourceStep || binding.SourceStepID == "" {
			continue
		}
		if _, ok := allowed[binding.SourceStepID]; ok {
			continue
		}
		return fmt.Errorf("compiled step %s references step %s without depends_on", step.ID, binding.SourceStepID)
	}

	if step.Runtime.InputSchema != nil && len(step.Runtime.InputSchema.Required) > 0 {
		required := make(map[string]struct{}, len(step.Runtime.InputSchema.Required))
		for _, field := range step.Runtime.InputSchema.Required {
			required[field] = struct{}{}
		}
		for key := range step.Runtime.Inputs {
			if _, ok := required[key]; !ok {
				return fmt.Errorf("compiled step %s input %s is not declared in input_schema", step.ID, key)
			}
		}
	}

	return nil
}

func normalizeCompiledStep(step *Step) (*Step, []string) {
	runtime := &Step{
		ID:                 step.ID,
		Name:               step.Name,
		AgentName:          step.AgentName,
		Capability:         step.Capability,
		Action:             step.Action,
		Inputs:             copyMap(step.Inputs),
		Parameters:         copyMap(step.Parameters),
		DependsOn:          append([]string(nil), step.DependsOn...),
		Condition:          cloneCondition(step.Condition),
		Route:              cloneRouteConfig(step.Route),
		Foreach:            cloneForeachConfig(step.Foreach),
		Timeout:            step.Timeout,
		Retries:            step.Retries,
		OnFailure:          cloneStepFailurePolicy(step.OnFailure),
		ContinueOn:         cloneContinuePolicy(step.ContinueOn),
		OutputAlias:        step.OutputAlias,
		InputSchema:        cloneStepSchema(step.InputSchema),
		OutputSchema:       cloneStepSchema(step.OutputSchema),
		Streaming:          cloneStreamingConfig(step.Streaming),
		CompensationAction: step.CompensationAction,
		CompensationParams: copyMap(step.CompensationParams),
	}

	defaulted := make([]string, 0, 8)
	if runtime.Name == "" {
		runtime.Name = runtime.ID
		defaulted = append(defaulted, "name")
	}
	if runtime.Retries == 0 {
		runtime.Retries = 1
		defaulted = append(defaulted, "retries")
	}
	if runtime.Foreach != nil {
		if runtime.Foreach.ItemAs == "" {
			runtime.Foreach.ItemAs = "item"
			defaulted = append(defaulted, "foreach.item_as")
		}
		if runtime.Foreach.IndexAs == "" {
			runtime.Foreach.IndexAs = "index"
			defaulted = append(defaulted, "foreach.index_as")
		}
	}
	if runtime.Streaming != nil && runtime.Streaming.Enabled {
		if runtime.Streaming.WaitFor == "" {
			runtime.Streaming.WaitFor = DefaultWaitFor
			defaulted = append(defaulted, "streaming.wait_for")
		}
		if runtime.Streaming.BufferStrategy == "" {
			runtime.Streaming.BufferStrategy = DefaultBufferStrategy
			defaulted = append(defaulted, "streaming.buffer_strategy")
		}
		if runtime.Streaming.BufferSize == 0 {
			runtime.Streaming.BufferSize = DefaultBufferSize
			defaulted = append(defaulted, "streaming.buffer_size")
		}
		if runtime.Streaming.StreamTimeout == 0 {
			runtime.Streaming.StreamTimeout = DefaultStreamTimeout
			defaulted = append(defaulted, "streaming.stream_timeout")
		}
		if runtime.Streaming.OnError == "" {
			runtime.Streaming.OnError = DefaultOnError
			defaulted = append(defaulted, "streaming.on_error")
		}
	}

	return runtime, defaulted
}

func collectStepBindings(step *Step) []DataBinding {
	if step == nil {
		return nil
	}

	bindings := make([]DataBinding, 0)
	bindings = append(bindings, collectBindingsFromValue("inputs", step.Inputs)...)
	bindings = append(bindings, collectBindingsFromValue("params", step.Parameters)...)
	bindings = append(bindings, collectBindingsFromValue("compensation_params", step.CompensationParams)...)

	if step.Foreach != nil && step.Foreach.From != "" {
		bindings = append(bindings, parseBindingExpressions("foreach.from", step.Foreach.From)...)
	}
	if step.Condition != nil && step.Condition.Expression != "" {
		bindings = append(bindings, parseBindingExpressions("condition.expression", step.Condition.Expression)...)
	}
	if step.Route != nil && step.Route.Expression != "" {
		bindings = append(bindings, parseBindingExpressions("route.expression", step.Route.Expression)...)
	}

	return bindings
}

func collectBindingsFromValue(prefix string, value interface{}) []DataBinding {
	switch current := value.(type) {
	case map[string]interface{}:
		bindings := make([]DataBinding, 0)
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			bindings = append(bindings, collectBindingsFromValue(path, current[key])...)
		}
		return bindings
	case []interface{}:
		bindings := make([]DataBinding, 0)
		for idx, item := range current {
			bindings = append(bindings, collectBindingsFromValue(fmt.Sprintf("%s[%d]", prefix, idx), item)...)
		}
		return bindings
	case string:
		return parseBindingExpressions(prefix, current)
	default:
		return nil
	}
}

func parseBindingExpressions(targetPath string, raw string) []DataBinding {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	matches := templateExprRE.FindAllStringSubmatch(raw, -1)
	if len(matches) > 0 {
		bindings := make([]DataBinding, 0, len(matches))
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			binding := classifyBinding(targetPath, strings.TrimSpace(match[1]))
			binding.Template = true
			bindings = append(bindings, binding)
		}
		return bindings
	}

	if looksLikeBindingReference(raw) {
		return []DataBinding{classifyBinding(targetPath, raw)}
	}
	return nil
}

func classifyBinding(targetPath, expression string) DataBinding {
	binding := DataBinding{
		TargetPath: targetPath,
		Expression: expression,
	}

	parts := strings.Split(expression, ".")
	if len(parts) == 0 {
		binding.SourceKind = BindingSourceLiteral
		return binding
	}

	if parts[0] == "variables" {
		binding.SourceKind = BindingSourceVariable
		binding.SourcePath = strings.Join(parts[1:], ".")
		return binding
	}

	if len(parts) == 1 {
		binding.SourceKind = BindingSourceVariable
		binding.SourcePath = parts[0]
		return binding
	}

	if len(parts) > 1 && parts[1] == "result" {
		binding.SourceKind = BindingSourceStep
		binding.SourceStepID = parts[0]
		binding.SourcePath = strings.Join(parts[2:], ".")
		return binding
	}

	binding.SourceKind = BindingSourceVariable
	binding.SourcePath = expression
	return binding
}

func looksLikeBindingReference(raw string) bool {
	if strings.ContainsAny(raw, " <>!=()+-*/\"'") {
		return false
	}
	return strings.Contains(raw, ".")
}

func cloneCondition(cond *Condition) *Condition {
	if cond == nil {
		return nil
	}
	copy := *cond
	return &copy
}

func cloneRouteConfig(route *RouteConfig) *RouteConfig {
	if route == nil {
		return nil
	}
	copy := &RouteConfig{
		Expression: route.Expression,
		Default:    append([]string(nil), route.Default...),
	}
	if route.Cases != nil {
		copy.Cases = make(map[string][]string, len(route.Cases))
		for key, targets := range route.Cases {
			copy.Cases[key] = append([]string(nil), targets...)
		}
	}
	return copy
}

func cloneForeachConfig(foreach *ForeachConfig) *ForeachConfig {
	if foreach == nil {
		return nil
	}
	copy := *foreach
	return &copy
}

func cloneStepFailurePolicy(policy *StepFailurePolicy) *StepFailurePolicy {
	if policy == nil {
		return nil
	}
	copy := *policy
	return &copy
}

func cloneContinuePolicy(policy *ContinuePolicy) *ContinuePolicy {
	if policy == nil {
		return nil
	}
	copy := *policy
	return &copy
}

func cloneStreamingConfig(streaming *StreamingConfig) *StreamingConfig {
	if streaming == nil {
		return nil
	}
	copy := *streaming
	if streaming.CheckpointEnabled != nil {
		value := *streaming.CheckpointEnabled
		copy.CheckpointEnabled = &value
	}
	return &copy
}

func cloneStepSchema(schema *StepSchema) *StepSchema {
	if schema == nil {
		return nil
	}
	return &StepSchema{
		Required: append([]string(nil), schema.Required...),
	}
}
