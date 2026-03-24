package workflow

import (
	"fmt"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/agent"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

func validateAgentInputContract(ag agent.Agent, step *Step, params map[string]interface{}) error {
	contractProvider, ok := ag.(agent.ActionContractProvider)
	if !ok {
		return nil
	}
	contract, ok := contractProvider.GetActionContract(step.Action)
	if !ok || contract == nil {
		return nil
	}
	for _, field := range contract.InputRequired {
		if _, exists := params[field]; !exists {
			return apperr.InvalidArgumentf("agent action %s missing required input: %s", step.Action, field).
				WithCode("workflow_agent_input_contract_invalid")
		}
	}
	return nil
}

func validateAgentOutputContract(ag agent.Agent, step *Step, result map[string]interface{}) error {
	contractProvider, ok := ag.(agent.ActionContractProvider)
	if !ok {
		return nil
	}
	contract, ok := contractProvider.GetActionContract(step.Action)
	if !ok || contract == nil {
		return nil
	}
	for _, field := range contract.OutputRequired {
		if _, exists := result[field]; !exists {
			return apperr.InvalidArgumentf("agent action %s missing required output: %s", step.Action, field).
				WithCode("workflow_agent_output_contract_invalid")
		}
	}
	return nil
}

func validateToolDefinitionContract(def interface {
	GetDefinition() interface{}
}) error {
	if def == nil {
		return nil
	}
	return nil
}

func mergeContractRequirements(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	out := make([]string, 0, len(left)+len(right))
	for _, field := range left {
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	for _, field := range right {
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func formatRequiredFields(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", fields)
}
