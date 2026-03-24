package workflow

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// TemplateEngine handles variable substitution in workflow parameters
type TemplateEngine struct {
	context *ExecutionContext
}

var templateExprRE = regexp.MustCompile(`\{\{([^}]+)\}\}`)
var conditionOperatorRE = regexp.MustCompile(`^(.*?)\s*(==|!=|>=|<=|>|<)\s*(.*?)$`)

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
	result := template
	matches := templateExprRE.FindAllStringSubmatch(template, -1)

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

// ResolveValue resolves a template expression to its raw value without converting it to string.
// Supports either a wrapped expression like "{{step.result.items}}" or a plain expression string.
func (te *TemplateEngine) ResolveValue(expression string) (interface{}, error) {
	expr := strings.TrimSpace(expression)
	if strings.HasPrefix(expr, "{{") && strings.HasSuffix(expr, "}}") {
		expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "{{"), "}}"))
	}
	return te.evaluateExpression(expr)
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
		if match := templateExprRE.FindStringSubmatch(strings.TrimSpace(v)); len(match) == 2 && strings.TrimSpace(v) == match[0] {
			return te.ResolveValue(v)
		}
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

	// Case 1.1: Nested global variable access (e.g. "user.id")
	if value, exists := te.context.GetVariable(parts[0]); exists {
		return navigateValue(value, parts[1:], expr)
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

		return navigateValue(output, parts[startIndex:], expr)
	}

	return "", fmt.Errorf("invalid expression: %s", expr)
}

func navigateValue(current interface{}, parts []string, expr string) (interface{}, error) {
	if len(parts) == 0 {
		return current, nil
	}

	for _, part := range parts {
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

// valueToString converts a value to its string representation
func (te *TemplateEngine) valueToString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
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
	expr := strings.TrimSpace(expression)
	if expr == "" {
		return false, fmt.Errorf("empty condition expression")
	}

	if matches := conditionOperatorRE.FindStringSubmatch(expr); len(matches) == 4 {
		left, err := te.resolveConditionOperand(matches[1])
		if err != nil {
			return false, err
		}
		right, err := te.resolveConditionOperand(matches[3])
		if err != nil {
			return false, err
		}
		return compareConditionValues(left, matches[2], right)
	}

	value, err := te.resolveConditionOperand(expr)
	if err != nil {
		return false, err
	}
	return te.toBoolean(value), nil
}

func (te *TemplateEngine) resolveConditionOperand(raw string) (interface{}, error) {
	operand := strings.TrimSpace(raw)
	if operand == "" {
		return "", nil
	}

	if value, ok, err := parseConditionLiteral(operand); ok || err != nil {
		return value, err
	}

	if strings.HasPrefix(operand, "{{") && strings.HasSuffix(operand, "}}") {
		return te.ResolveValue(operand)
	}

	return te.evaluateExpression(operand)
}

func parseConditionLiteral(raw string) (interface{}, bool, error) {
	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			return raw[1 : len(raw)-1], true, nil
		}
	}

	switch strings.ToLower(raw) {
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	case "null", "nil":
		return nil, true, nil
	}

	if num, err := strconv.ParseFloat(raw, 64); err == nil {
		return num, true, nil
	}

	return nil, false, nil
}

func compareConditionValues(left interface{}, operator string, right interface{}) (bool, error) {
	if leftNum, ok := toComparableNumber(left); ok {
		if rightNum, ok := toComparableNumber(right); ok {
			return compareNumeric(leftNum, operator, rightNum)
		}
	}

	if leftBool, ok := left.(bool); ok {
		if rightBool, ok := right.(bool); ok {
			switch operator {
			case "==":
				return leftBool == rightBool, nil
			case "!=":
				return leftBool != rightBool, nil
			default:
				return false, fmt.Errorf("operator %s not supported for booleans", operator)
			}
		}
	}

	if left == nil || right == nil {
		switch operator {
		case "==":
			return left == right, nil
		case "!=":
			return left != right, nil
		default:
			return false, fmt.Errorf("operator %s not supported for nil values", operator)
		}
	}

	leftStr := stringifyComparableValue(left)
	rightStr := stringifyComparableValue(right)
	switch operator {
	case "==":
		return leftStr == rightStr, nil
	case "!=":
		return leftStr != rightStr, nil
	case ">":
		return leftStr > rightStr, nil
	case ">=":
		return leftStr >= rightStr, nil
	case "<":
		return leftStr < rightStr, nil
	case "<=":
		return leftStr <= rightStr, nil
	default:
		return false, fmt.Errorf("unsupported operator: %s", operator)
	}
}

func compareNumeric(left float64, operator string, right float64) (bool, error) {
	switch operator {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case ">":
		return left > right, nil
	case ">=":
		return left >= right, nil
	case "<":
		return left < right, nil
	case "<=":
		return left <= right, nil
	default:
		return false, fmt.Errorf("unsupported operator: %s", operator)
	}
}

func toComparableNumber(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func stringifyComparableValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	case map[string]interface{}:
		return formatMapValue(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatMapValue(value map[string]interface{}) string {
	if len(value) == 0 {
		return "{}"
	}

	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%v", key, value[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// toBoolean converts a value to boolean semantics for conditions.
func (te *TemplateEngine) toBoolean(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		return s == "true" || s == "1" || s == "yes" || s == "on"
	case nil:
		return false
	default:
		if num, ok := toComparableNumber(v); ok {
			return num != 0
		}
		return fmt.Sprintf("%v", v) != ""
	}
}
