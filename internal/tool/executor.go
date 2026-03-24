package tool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
)

// ExecutionConfig holds configuration for tool execution
type ExecutionConfig struct {
	Timeout      time.Duration
	MaxRetries   int
	RetryDelay   time.Duration
	RetryBackoff float64 // Exponential backoff multiplier (e.g., 2.0)
}

// DefaultExecutionConfig returns sensible defaults
func DefaultExecutionConfig() *ExecutionConfig {
	return &ExecutionConfig{
		Timeout:      30 * time.Second,
		MaxRetries:   3,
		RetryDelay:   1 * time.Second,
		RetryBackoff: 2.0,
	}
}

// Executor handles safe tool execution with timeout, retry, and error handling
type Executor struct {
	registry *Registry
	config   *ExecutionConfig
	metrics  *monitor.ToolMetrics
}

// NewExecutor creates a new tool executor
func NewExecutor(registry *Registry, config *ExecutionConfig) *Executor {
	if config == nil {
		config = DefaultExecutionConfig()
	}
	return &Executor{
		registry: registry,
		config:   config,
	}
}

func (e *Executor) SetMetrics(metrics *monitor.ToolMetrics) {
	e.metrics = metrics
}

// Execute executes a tool with full error handling, timeout, and retry logic
func (e *Executor) Execute(ctx context.Context, toolName string, params map[string]interface{}) (*Result, error) {
	start := time.Now()
	e.observeToolStarted(toolName)

	// Get tool from registry
	toolInstance, err := e.registry.Get(toolName)
	if err != nil {
		e.observeToolFinished(toolName, "not_found", start)
		return nil, ErrToolNotFound(toolName)
	}

	// Validate parameters against schema
	if err := e.validateParams(toolInstance, params); err != nil {
		e.observeToolValidationFailed(toolName)
		e.observeToolFinished(toolName, "validation_failed", start)
		return nil, err
	}

	// Execute with retry logic
	var lastErr error
	retryDelay := e.config.RetryDelay

	for attempt := 0; attempt <= e.config.MaxRetries; attempt++ {
		// Add timeout to context
		execCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)

		// Execute with timeout
		resultChan := make(chan *Result, 1)
		errChan := make(chan error, 1)

		go func() {
			// Panic recovery to prevent tool panics from crashing the agent
			defer func() {
				if r := recover(); r != nil {
					e.observeToolPanic(toolName)
					errChan <- ErrExecutionFailed(
						toolName,
						fmt.Sprintf("tool execution panicked: %v", r),
						fmt.Errorf("panic: %v", r),
					)
				}
			}()

			result, err := toolInstance.Execute(execCtx, params)
			if err != nil {
				errChan <- err
			} else {
				resultChan <- result
			}
		}()

		// Wait for result or timeout
		select {
		case <-execCtx.Done():
			cancel()
			if execCtx.Err() == context.DeadlineExceeded {
				e.observeToolTimeout(toolName)
				lastErr = ErrTimeout(toolName, e.config.Timeout)
			} else {
				lastErr = fmt.Errorf("context cancelled: %w", execCtx.Err())
			}
		case err := <-errChan:
			cancel()
			lastErr = e.wrapExecutionError(toolName, err)
		case result := <-resultChan:
			cancel()
			if !result.Success {
				lastErr = ErrExecutionFailed(toolName, result.Error, nil)
			} else {
				e.observeToolFinished(toolName, "success", start)
				return result, nil // Success!
			}
		}

		// Check if error is retryable
		if !IsRetryable(lastErr) {
			break // Don't retry non-retryable errors
		}
		e.observeToolRetry(toolName, retryReason(lastErr))

		// Don't sleep after last attempt
		if attempt < e.config.MaxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
				// Exponential backoff
				retryDelay = time.Duration(float64(retryDelay) * e.config.RetryBackoff)
			}
		}
	}

	e.observeToolFinished(toolName, toolStatus(lastErr), start)
	return nil, lastErr
}

// validateParams validates parameters against tool schema
func (e *Executor) validateParams(tool Tool, params map[string]interface{}) error {
	def := tool.GetDefinition()
	if def == nil || def.Parameters == nil {
		return nil // No validation needed
	}

	// Check required parameters
	for _, required := range def.Parameters.Required {
		if _, exists := params[required]; !exists {
			return ErrInvalidParams(
				def.Name,
				fmt.Sprintf("missing required parameter: %s", required),
			).WithDetails("required_params", def.Parameters.Required)
		}
	}

	// Validate parameter types
	for key, value := range params {
		propSchema, exists := def.Parameters.Properties[key]
		if !exists {
			return ErrInvalidParams(
				def.Name,
				fmt.Sprintf("unknown parameter: %s", key),
			).WithDetails("allowed_params", getPropertyNames(def.Parameters.Properties))
		}

		if err := e.validateType(def.Name, key, value, propSchema); err != nil {
			return err
		}
	}

	return nil
}

// validateType validates a parameter value against its schema type
func (e *Executor) validateType(toolName, paramName string, value interface{}, schema *PropertySchema) error {
	actualType := getJSONType(value)

	// Handle special cases
	if schema.Type == "integer" && actualType == "number" {
		if _, ok := value.(int); ok {
			return nil
		}
		if floatVal, ok := value.(float64); ok {
			if floatVal == float64(int(floatVal)) {
				return nil
			}
		}
	}

	if actualType != schema.Type {
		return ErrInvalidParams(
			toolName,
			fmt.Sprintf("parameter '%s' has wrong type: expected %s, got %s",
				paramName, schema.Type, actualType),
		).WithDetails("parameter", paramName).
			WithDetails("expected_type", schema.Type).
			WithDetails("actual_type", actualType)
	}

	// Validate enum values
	if len(schema.Enum) > 0 {
		strVal, ok := value.(string)
		if !ok {
			return ErrInvalidParams(toolName, fmt.Sprintf("enum parameter '%s' must be string", paramName))
		}

		valid := false
		for _, enumVal := range schema.Enum {
			if strVal == enumVal {
				valid = true
				break
			}
		}

		if !valid {
			return ErrInvalidParams(
				toolName,
				fmt.Sprintf("parameter '%s' must be one of: %v", paramName, schema.Enum),
			).WithDetails("allowed_values", schema.Enum).
				WithDetails("provided_value", strVal)
		}
	}

	return nil
}

// wrapExecutionError wraps execution errors into ToolError
func (e *Executor) wrapExecutionError(toolName string, err error) error {
	// If already a ToolError, return as-is
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return err
	}

	// Wrap generic errors
	return ErrExecutionFailed(toolName, err.Error(), err)
}

func (e *Executor) observeToolStarted(toolName string) {
	if e.metrics != nil {
		e.metrics.ObserveToolStarted(toolName)
	}
}

func (e *Executor) observeToolFinished(toolName, status string, start time.Time) {
	if e.metrics != nil {
		e.metrics.ObserveToolFinished(toolName, status, time.Since(start))
	}
}

func (e *Executor) observeToolRetry(toolName, reason string) {
	if e.metrics != nil {
		e.metrics.ObserveToolRetry(toolName, reason)
	}
}

func (e *Executor) observeToolTimeout(toolName string) {
	if e.metrics != nil {
		e.metrics.ObserveToolTimeout(toolName)
	}
}

func (e *Executor) observeToolValidationFailed(toolName string) {
	if e.metrics != nil {
		e.metrics.ObserveToolValidationFailed(toolName)
	}
}

func (e *Executor) observeToolPanic(toolName string) {
	if e.metrics != nil {
		e.metrics.ObserveToolPanic(toolName)
	}
}

func toolStatus(err error) string {
	if err == nil {
		return "success"
	}
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		switch toolErr.Type {
		case ErrorTypeNotFound:
			return "not_found"
		case ErrorTypeInvalidParams:
			return "validation_failed"
		case ErrorTypeTimeout:
			return "timeout"
		default:
			return "failed"
		}
	}
	return "failed"
}

func retryReason(err error) string {
	var toolErr *ToolError
	if errors.As(err, &toolErr) && toolErr.Type != "" {
		return string(toolErr.Type)
	}
	return "retryable_error"
}

// getJSONType returns JSON type name for a Go value
func getJSONType(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case int, int32, int64, uint, uint32, uint64:
		return "integer"
	case float32, float64:
		return "number"
	case bool:
		return "boolean"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

// getPropertyNames extracts property names from schema
func getPropertyNames(props map[string]*PropertySchema) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	return names
}
