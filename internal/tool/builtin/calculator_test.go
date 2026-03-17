package builtin

import (
	"context"
	"testing"
)

func TestCalculator(t *testing.T) {
	calc := NewCalculator()
	ctx := context.Background()

	tests := []struct {
		name       string
		expression string
		wantResult float64
		wantError  bool
	}{
		{
			name:       "Addition",
			expression: "5 + 3",
			wantResult: 8,
			wantError:  false,
		},
		{
			name:       "Subtraction",
			expression: "10 - 4",
			wantResult: 6,
			wantError:  false,
		},
		{
			name:       "Multiplication",
			expression: "6 * 7",
			wantResult: 42,
			wantError:  false,
		},
		{
			name:       "Division",
			expression: "20 / 4",
			wantResult: 5,
			wantError:  false,
		},
		{
			name:       "Power",
			expression: "2 ^ 3",
			wantResult: 8,
			wantError:  false,
		},
		{
			name:       "Square Root",
			expression: "sqrt(16)",
			wantResult: 4,
			wantError:  false,
		},
		{
			name:       "Simple Number",
			expression: "42",
			wantResult: 42,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"expression": tt.expression,
			}

			result, err := calc.Execute(ctx, params)

			if tt.wantError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !result.Success {
				t.Errorf("Expected success but got failure: %s", result.Error)
			}

			resultValue, ok := result.Data["result"].(float64)
			if !ok {
				t.Fatalf("Result is not a float64")
			}

			if resultValue != tt.wantResult {
				t.Errorf("Expected %f but got %f", tt.wantResult, resultValue)
			}
		})
	}
}

func TestCalculatorDefinition(t *testing.T) {
	calc := NewCalculator()
	def := calc.GetDefinition()

	if def.Name != "calculator" {
		t.Errorf("Expected name 'calculator', got '%s'", def.Name)
	}

	if def.Parameters == nil {
		t.Fatal("Parameters should not be nil")
	}

	if _, ok := def.Parameters.Properties["expression"]; !ok {
		t.Error("Expected 'expression' parameter")
	}
}

func TestCalculatorMissingExpression(t *testing.T) {
	calc := NewCalculator()
	ctx := context.Background()

	params := map[string]interface{}{}
	result, err := calc.Execute(ctx, params)

	if err == nil {
		t.Error("Expected error for missing expression")
	}

	if result.Success {
		t.Error("Expected failure for missing expression")
	}
}
