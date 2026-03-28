package workflow

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// TemplateEngine 负责工作流参数中的模板解析。
// 它既支持把表达式渲染成字符串，也支持直接取原始值用于 foreach、condition 等场景。
type TemplateEngine struct {
	context *ExecutionContext
}

var templateExprRE = regexp.MustCompile(`\{\{([^}]+)\}\}`)
var conditionOperatorRE = regexp.MustCompile(`^(.*?)\s*(==|!=|>=|<=|>|<)\s*(.*?)$`)

// NewTemplateEngine 创建模板引擎。
func NewTemplateEngine(ctx *ExecutionContext) *TemplateEngine {
	return &TemplateEngine{
		context: ctx,
	}
}

// Render 渲染字符串模板。
// 支持：
// - `{{variable_name}}` 访问全局变量
// - `{{step_id.result.field}}` 访问步骤输出字段
// - `{{step_id.result}}` 访问整个步骤输出
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

// ResolveValue 解析表达式并返回原始值，而不是强制转成字符串。
// 这通常用于 foreach、condition 或需要保留数组/对象结构的参数渲染场景。
func (te *TemplateEngine) ResolveValue(expression string) (interface{}, error) {
	expr := strings.TrimSpace(expression)
	if strings.HasPrefix(expr, "{{") && strings.HasSuffix(expr, "}}") {
		expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "{{"), "}}"))
	}
	return te.evaluateExpression(expr)
}

// RenderParameters 递归渲染整张参数表。
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

// renderValue 递归渲染单个值，支持 map / slice / string 嵌套结构。
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

// evaluateExpression 根据当前执行上下文解析一个表达式。
// 解析顺序大致是：全局变量 -> 嵌套全局变量 -> 步骤输出。
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

// navigateValue 沿字段路径逐层取值，目前主要支持 map 结构。
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

// valueToString 把任意值转换成字符串形式，供模板替换使用。
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

// EvaluateCondition 评估条件表达式。
// 支持简单比较，例如：
// - `{{step1.result.value}} > 10`
// - `{{variable}} == "success"`
// - `{{step1.result.enabled}} == true`
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

// parseConditionLiteral 尝试把条件操作数解析为字面量值。
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

// compareConditionValues 根据运算符比较两个条件操作数。
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

// compareNumeric 处理数值类型的比较。
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

// toComparableNumber 尝试把常见数字类型统一转换成 float64。
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

// stringifyComparableValue 把任意值转换成可比较的稳定字符串形式。
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

// toBoolean 把任意值按条件表达式语义转换为布尔值。
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
