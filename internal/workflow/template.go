package workflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TemplateEngine handles variable substitution in workflow parameters
type TemplateEngine struct {
	context *ExecutionContext
}

// NewTemplateEngine creates a new template engine
func NewTemplateEngine(ctx *ExecutionContext) *TemplateEngine {
	return &TemplateEngine{
		context: ctx,
	}
}

// Render renders a template string with context variables
// Supports patterns like:
//   - {{variable_name}} - global variables
//   - {{step_id.result.field}} - step output fields
//   - {{step_id.result}} - entire step result
func (te *TemplateEngine) Render(template string) (string, error) {
	// 正则表达式匹配所有 {{...}}
	re := regexp.MustCompile(`\{\{([^}]+)\}\}`)

	result := template
	matches := re.FindAllStringSubmatch(template, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		placeholder := match[0] // Full match: {{...}}
		expression := strings.TrimSpace(match[1])

		value, err := te.evaluateExpression(expression)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate expression '%s': %w", expression, err)
		}

		// Convert value to string
		valueStr := te.valueToString(value)

		// Replace placeholder with value
		result = strings.ReplaceAll(result, placeholder, valueStr)
	}

	return result, nil
}

// RenderParameters renders all parameters in a map
func (te *TemplateEngine) RenderParameters(params map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for key, value := range params {
		rendered, err := te.renderValue(value)
		if err != nil {
			return nil, fmt.Errorf("failed to render parameter '%s': %w", key, err)
		}
		result[key] = rendered
	}

	return result, nil
}

// renderValue recursively renders a value (handles nested maps and arrays)
func (te *TemplateEngine) renderValue(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return te.Render(v)

	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, val := range v {
			rendered, err := te.renderValue(val)
			if err != nil {
				return nil, err
			}
			result[key] = rendered
		}
		return result, nil

	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			rendered, err := te.renderValue(val)
			if err != nil {
				return nil, err
			}
			result[i] = rendered
		}
		return result, nil

	default:
		// Return as-is for non-template types
		return value, nil
	}
}

// evaluateExpression evaluates an expression against the execution context
func (te *TemplateEngine) evaluateExpression(expr string) (interface{}, error) {
	parts := strings.Split(expr, ".")

	// Case 1: Global variable (no dots or starts with 'variables.')
	if len(parts) == 1 || (len(parts) > 1 && parts[0] == "variables") {
		varName := expr
		if parts[0] == "variables" && len(parts) > 1 {
			varName = strings.Join(parts[1:], ".")
		}

		if value, exists := te.context.GetVariable(varName); exists {
			return value, nil
		}
		return "", fmt.Errorf("variable not found: %s", varName)
	}

	// Case 2: Step output reference (e.g., "step1.result.field")
	if len(parts) >= 2 {
		stepID := parts[0]
		output, exists := te.context.GetStepOutput(stepID)
		if !exists {
			return "", fmt.Errorf("step output not found: %s", stepID)
		}

		// Skip "result" if it's the second part (for compatibility with {{step1.result.field}} syntax)
		startIndex := 1
		if len(parts) > 1 && parts[1] == "result" {
			startIndex = 2
		}

		// If we only have "step1.result", return the entire output
		if startIndex >= len(parts) {
			return output, nil
		}

		// Navigate nested structure
		current := interface{}(output)
		for _, part := range parts[startIndex:] {
			switch v := current.(type) {
			case map[string]interface{}:
				if val, ok := v[part]; ok {
					current = val
				} else {
					return "", fmt.Errorf("field not found: %s in %s", part, expr)
				}
			default:
				return "", fmt.Errorf("cannot access field %s: not a map", part)
			}
		}

		return current, nil
	}

	return "", fmt.Errorf("invalid expression: %s", expr)
}

// valueToString converts a value to its string representation
func (te *TemplateEngine) valueToString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case int, int32, int64:
		return fmt.Sprintf("%d", v)
	case float32, float64:
		return strconv.FormatFloat(v.(float64), 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		// For complex types, return JSON representation
		return fmt.Sprintf("%v", v)
	}
}

// EvaluateCondition evaluates a condition expression
// Supports simple comparisons:
//   - {{step1.result.value}} > 10
//   - {{variable}} == "success"
//   - {{step1.result.enabled}} == true
func (te *TemplateEngine) EvaluateCondition(expression string) (bool, error) {
	// Simple implementation: render template and evaluate
	// In production, you'd want a proper expression evaluator

	rendered, err := te.Render(expression)
	if err != nil {
		return false, err
	}

	// Check for comparison operators
	operators := []string{"==", "!=", ">=", "<=", ">", "<"}
	for _, op := range operators {
		if strings.Contains(rendered, op) {
			parts := strings.SplitN(rendered, op, 2)
			if len(parts) != 2 {
				continue
			}

			left := strings.TrimSpace(parts[0])
			right := strings.TrimSpace(parts[1])

			return te.compare(left, op, right)
		}
	}

	// If no operator, treat as boolean
	return te.toBoolean(rendered), nil
}

// compare compares two values based on operator
func (te *TemplateEngine) compare(left, operator, right string) (bool, error) {
	// Try numeric comparison first
	leftNum, leftErr := strconv.ParseFloat(left, 64)
	rightNum, rightErr := strconv.ParseFloat(right, 64)

	if leftErr == nil && rightErr == nil {
		switch operator {
		case "==":
			return leftNum == rightNum, nil
		case "!=":
			return leftNum != rightNum, nil
		case ">":
			return leftNum > rightNum, nil
		case ">=":
			return leftNum >= rightNum, nil
		case "<":
			return leftNum < rightNum, nil
		case "<=":
			return leftNum <= rightNum, nil
		}
	}

	// String comparison
	switch operator {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	default:
		return false, fmt.Errorf("operator %s not supported for strings", operator)
	}
}

// toBoolean converts a string to boolean
func (te *TemplateEngine) toBoolean(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}
