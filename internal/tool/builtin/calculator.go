package builtin

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// Calculator is a tool for performing mathematical calculations
type Calculator struct{}

// NewCalculator creates a new calculator tool
func NewCalculator() *Calculator {
	return &Calculator{}
}

// GetDefinition returns the tool definition
func (c *Calculator) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "calculator",
		Description: "Perform mathematical calculations. Supports basic operations (+, -, *, /), power (^), and common functions (sqrt, sin, cos, tan, log, ln).",
		Parameters: &tool.ParametersSchema{
			Type: "object",
			Properties: map[string]*tool.PropertySchema{
				"expression": {
					Type:        "string",
					Description: "The mathematical expression to evaluate, e.g., '2 + 2', '10 * 5', 'sqrt(16)', 'sin(45)'",
				},
			},
			Required: []string{"expression"},
		},
	}
}

// Execute performs the calculation
func (c *Calculator) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	expression, ok := params["expression"].(string)
	if !ok || expression == "" {
		return &tool.Result{
			Success: false,
			Error:   "missing or invalid 'expression' parameter",
		}, fmt.Errorf("missing expression")
	}

	// Simple expression evaluator
	result, err := c.evaluate(expression)
	if err != nil {
		return &tool.Result{
			Success: false,
			Error:   fmt.Sprintf("calculation error: %v", err),
		}, err
	}

	return &tool.Result{
		Success: true,
		Data: map[string]interface{}{
			"expression": expression,
			"result":     result,
		},
	}, nil
}

// evaluate performs simple expression evaluation
func (c *Calculator) evaluate(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)

	// Handle common functions
	if strings.HasPrefix(expr, "sqrt(") && strings.HasSuffix(expr, ")") {
		arg := strings.TrimSuffix(strings.TrimPrefix(expr, "sqrt("), ")")
		val, err := c.evaluate(arg)
		if err != nil {
			return 0, err
		}
		return math.Sqrt(val), nil
	}

	if strings.HasPrefix(expr, "sin(") && strings.HasSuffix(expr, ")") {
		arg := strings.TrimSuffix(strings.TrimPrefix(expr, "sin("), ")")
		val, err := c.evaluate(arg)
		if err != nil {
			return 0, err
		}
		return math.Sin(val * math.Pi / 180), nil // Convert to radians
	}

	if strings.HasPrefix(expr, "cos(") && strings.HasSuffix(expr, ")") {
		arg := strings.TrimSuffix(strings.TrimPrefix(expr, "cos("), ")")
		val, err := c.evaluate(arg)
		if err != nil {
			return 0, err
		}
		return math.Cos(val * math.Pi / 180), nil
	}

	if strings.HasPrefix(expr, "tan(") && strings.HasSuffix(expr, ")") {
		arg := strings.TrimSuffix(strings.TrimPrefix(expr, "tan("), ")")
		val, err := c.evaluate(arg)
		if err != nil {
			return 0, err
		}
		return math.Tan(val * math.Pi / 180), nil
	}

	if strings.HasPrefix(expr, "log(") && strings.HasSuffix(expr, ")") {
		arg := strings.TrimSuffix(strings.TrimPrefix(expr, "log("), ")")
		val, err := c.evaluate(arg)
		if err != nil {
			return 0, err
		}
		return math.Log10(val), nil
	}

	if strings.HasPrefix(expr, "ln(") && strings.HasSuffix(expr, ")") {
		arg := strings.TrimSuffix(strings.TrimPrefix(expr, "ln("), ")")
		val, err := c.evaluate(arg)
		if err != nil {
			return 0, err
		}
		return math.Log(val), nil
	}

	// Handle basic operations
	for _, op := range []string{"+", "-", "*", "/", "^"} {
		parts := strings.Split(expr, op)
		if len(parts) == 2 {
			left, err := c.evaluate(strings.TrimSpace(parts[0]))
			if err != nil {
				continue
			}
			right, err := c.evaluate(strings.TrimSpace(parts[1]))
			if err != nil {
				continue
			}

			switch op {
			case "+":
				return left + right, nil
			case "-":
				return left - right, nil
			case "*":
				return left * right, nil
			case "/":
				if right == 0 {
					return 0, fmt.Errorf("division by zero")
				}
				return left / right, nil
			case "^":
				return math.Pow(left, right), nil
			}
		}
	}

	// Try to parse as a number
	return strconv.ParseFloat(expr, 64)
}
