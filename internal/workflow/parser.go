package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parser handles parsing workflow definitions from various formats
type Parser struct {
	report ValidationReport
}

// ValidationReport contains non-fatal parser findings that help improve DSL usage.
type ValidationReport struct {
	Warnings    []string `json:"warnings,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// NewParser creates a new workflow parser
func NewParser() *Parser {
	return &Parser{}
}

// Warnings returns non-fatal validation warnings from the last parse.
func (p *Parser) Warnings() []string {
	return append([]string(nil), p.report.Warnings...)
}

// LastReport returns the validation report from the last parse.
func (p *Parser) LastReport() ValidationReport {
	return ValidationReport{
		Warnings:    append([]string(nil), p.report.Warnings...),
		Suggestions: append([]string(nil), p.report.Suggestions...),
	}
}

// ParseYAML parses a workflow from YAML content
func (p *Parser) ParseYAML(content []byte) (*Workflow, error) {
	p.resetWarnings()

	var wf Workflow
	if err := yaml.Unmarshal(content, &wf); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := p.validate(&wf); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return &wf, nil
}

// ParseJSON parses a workflow from JSON content
func (p *Parser) ParseJSON(content []byte) (*Workflow, error) {
	p.resetWarnings()

	var wf Workflow
	if err := json.Unmarshal(content, &wf); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	if err := p.validate(&wf); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return &wf, nil
}

// ParseYAMLFile parses a workflow from a YAML file
func (p *Parser) ParseYAMLFile(filepath string) (*Workflow, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.ParseYAML(content)
}

// ParseJSONFile parses a workflow from a JSON file
func (p *Parser) ParseJSONFile(filepath string) (*Workflow, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.ParseJSON(content)
}

// validate validates a workflow definition
func (p *Parser) validate(wf *Workflow) error {
	if wf.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if len(wf.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}

	// Validate steps
	stepIDs := make(map[string]bool)
	stepMap := make(map[string]*Step)
	for _, step := range wf.Steps {
		if step.ID == "" {
			return fmt.Errorf("step ID is required")
		}

		if stepIDs[step.ID] {
			return fmt.Errorf("duplicate step ID: %s", step.ID)
		}
		stepIDs[step.ID] = true
		stepMap[step.ID] = step

		if step.AgentName == "" {
			return fmt.Errorf("step %s: agent name is required", step.ID)
		}

		if step.Action == "" {
			return fmt.Errorf("step %s: action is required", step.ID)
		}

		// Validate dependencies exist
		for _, depID := range step.DependsOn {
			if !stepIDs[depID] && !p.willExist(depID, wf.Steps) {
				return fmt.Errorf("step %s depends on non-existent step: %s", step.ID, depID)
			}
		}
	}

	for _, step := range wf.Steps {
		if err := p.validateRoute(step, stepMap); err != nil {
			return err
		}
		if err := p.validateForeach(step); err != nil {
			return err
		}
	}

	p.collectConditionalBranchWarnings(wf)

	// Validate DAG structure (no cycles)
	if _, err := NewDAG(wf.Steps); err != nil {
		return fmt.Errorf("invalid workflow structure: %w", err)
	}

	return nil
}

func (p *Parser) resetWarnings() {
	p.report = ValidationReport{}
}

func (p *Parser) addWarning(format string, args ...interface{}) {
	p.report.Warnings = append(p.report.Warnings, fmt.Sprintf(format, args...))
}

func (p *Parser) addSuggestion(format string, args ...interface{}) {
	p.report.Suggestions = append(p.report.Suggestions, fmt.Sprintf(format, args...))
}

func (p *Parser) validateRoute(step *Step, stepMap map[string]*Step) error {
	if step.Route == nil {
		return nil
	}
	if step.Route.Expression == "" {
		return fmt.Errorf("step %s: route expression is required", step.ID)
	}
	if len(step.Route.Cases) == 0 && len(step.Route.Default) == 0 {
		return fmt.Errorf("step %s: route must define at least one case or default target", step.ID)
	}

	seenTargets := make(map[string]string)
	validateTargets := func(routeKey string, targets []string) error {
		for _, targetID := range targets {
			target, ok := stepMap[targetID]
			if !ok {
				return fmt.Errorf("step %s: route target does not exist: %s", step.ID, targetID)
			}
			if !slices.Contains(target.DependsOn, step.ID) {
				return fmt.Errorf("step %s: route target %s must depend on router step", step.ID, targetID)
			}
			if previousKey, exists := seenTargets[targetID]; exists {
				return fmt.Errorf("step %s: route target %s is duplicated in %s and %s", step.ID, targetID, previousKey, routeKey)
			}
			seenTargets[targetID] = routeKey
		}
		return nil
	}

	for routeKey, targets := range step.Route.Cases {
		if err := validateTargets("case:"+routeKey, targets); err != nil {
			return err
		}
	}
	if err := validateTargets("default", step.Route.Default); err != nil {
		return err
	}

	return nil
}

func (p *Parser) validateForeach(step *Step) error {
	if step.Foreach == nil {
		return nil
	}
	if step.Foreach.From == "" {
		return fmt.Errorf("step %s: foreach.from is required", step.ID)
	}
	if step.Foreach.ItemAs != "" && step.Foreach.IndexAs != "" && step.Foreach.ItemAs == step.Foreach.IndexAs {
		return fmt.Errorf("step %s: foreach.item_as and foreach.index_as must be different", step.ID)
	}
	if step.Foreach.MaxParallel < 0 {
		return fmt.Errorf("step %s: foreach.max_parallel must be >= 0", step.ID)
	}
	if step.Streaming != nil && step.Streaming.Enabled {
		return fmt.Errorf("step %s: foreach does not support streaming.enabled yet", step.ID)
	}
	return nil
}

func (p *Parser) collectConditionalBranchWarnings(wf *Workflow) {
	groups := make(map[string][]*Step)
	for _, step := range wf.Steps {
		if step.Condition == nil || step.Condition.Type != ConditionTypeExpression || len(step.DependsOn) == 0 {
			continue
		}
		key := dependencyGroupKey(step.DependsOn)
		groups[key] = append(groups[key], step)
	}

	for depKey, steps := range groups {
		if len(steps) < 2 {
			continue
		}

		stepIDs := make([]string, 0, len(steps))
		for _, step := range steps {
			stepIDs = append(stepIDs, step.ID)
		}
		sort.Strings(stepIDs)

		p.addWarning(
			"steps [%s] share the same dependencies [%s] and each uses expression condition; this looks like a branch split and is usually better modeled with route",
			strings.Join(stepIDs, ", "),
			depKey,
		)
		p.addSuggestion(
			"replace the conditional branch pattern on [%s] with a router step that sets route.expression and route.cases",
			strings.Join(stepIDs, ", "),
		)
	}
}

func dependencyGroupKey(dependsOn []string) string {
	deps := append([]string(nil), dependsOn...)
	sort.Strings(deps)
	return strings.Join(deps, ", ")
}

// willExist checks if a step ID will exist later in the steps array
func (p *Parser) willExist(stepID string, steps []*Step) bool {
	for _, step := range steps {
		if step.ID == stepID {
			return true
		}
	}
	return false
}

// ToYAML converts a workflow to YAML format
func (p *Parser) ToYAML(wf *Workflow) ([]byte, error) {
	data, err := yaml.Marshal(wf)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to YAML: %w", err)
	}
	return data, nil
}

// ToJSON converts a workflow to JSON format
func (p *Parser) ToJSON(wf *Workflow) ([]byte, error) {
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to JSON: %w", err)
	}
	return data, nil
}
