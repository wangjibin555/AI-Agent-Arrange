package workflow

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Parser handles parsing workflow definitions from various formats
type Parser struct{}

// NewParser creates a new workflow parser
func NewParser() *Parser {
	return &Parser{}
}

// ParseYAML parses a workflow from YAML content
func (p *Parser) ParseYAML(content []byte) (*Workflow, error) {
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
	for _, step := range wf.Steps {
		if step.ID == "" {
			return fmt.Errorf("step ID is required")
		}

		if stepIDs[step.ID] {
			return fmt.Errorf("duplicate step ID: %s", step.ID)
		}
		stepIDs[step.ID] = true

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

	// Validate DAG structure (no cycles)
	if _, err := NewDAG(wf.Steps); err != nil {
		return fmt.Errorf("invalid workflow structure: %w", err)
	}

	return nil
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
