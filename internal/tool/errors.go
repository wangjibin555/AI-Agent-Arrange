package tool

import (
	"errors"
	"fmt"
)

// ErrorType represents different categories of tool execution errors
type ErrorType string

const (
	// ErrorTypeNotFound indicates the tool was not found in registry
	ErrorTypeNotFound ErrorType = "TOOL_NOT_FOUND"

	// ErrorTypeInvalidParams indicates the parameters are invalid
	ErrorTypeInvalidParams ErrorType = "INVALID_PARAMETERS"

	// ErrorTypeExecutionFailed indicates the tool execution failed
	ErrorTypeExecutionFailed ErrorType = "EXECUTION_FAILED"

	// ErrorTypeTimeout indicates the tool execution timed out
	ErrorTypeTimeout ErrorType = "TIMEOUT"

	// ErrorTypePermissionDenied indicates permission issues
	ErrorTypePermissionDenied ErrorType = "PERMISSION_DENIED"

	// ErrorTypeRateLimited indicates rate limiting
	ErrorTypeRateLimited ErrorType = "RATE_LIMITED"

	// ErrorTypeInternalError indicates internal system errors
	ErrorTypeInternalError ErrorType = "INTERNAL_ERROR"
)

// ToolError represents a structured error for tool execution
type ToolError struct {
	Type          ErrorType              `json:"type"`
	Message       string                 `json:"message"`
	ToolName      string                 `json:"tool_name,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	Retryable     bool                   `json:"retryable"`
	OriginalError error                  `json:"-"`
	SuggestedFix  string                 `json:"suggested_fix,omitempty"`
}

// Error implements the error interface
func (e *ToolError) Error() string {
	if e.ToolName != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Type, e.ToolName, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// Unwrap returns the original error
func (e *ToolError) Unwrap() error {
	return e.OriginalError
}

// ToLLMMessage formats the error in a way that's helpful for LLM
func (e *ToolError) ToLLMMessage() string {
	msg := fmt.Sprintf("Tool execution failed: %s\n\nError Type: %s\nMessage: %s",
		e.ToolName, e.Type, e.Message)

	if e.SuggestedFix != "" {
		msg += fmt.Sprintf("\n\nSuggested Fix: %s", e.SuggestedFix)
	}

	if len(e.Details) > 0 {
		msg += "\n\nDetails:"
		for key, value := range e.Details {
			msg += fmt.Sprintf("\n  - %s: %v", key, value)
		}
	}

	if e.Retryable {
		msg += "\n\nThis error is retryable. You may try again with the same or modified parameters."
	}

	return msg
}

// NewToolError creates a new ToolError
func NewToolError(errType ErrorType, toolName, message string) *ToolError {
	return &ToolError{
		Type:      errType,
		ToolName:  toolName,
		Message:   message,
		Details:   make(map[string]interface{}),
		Retryable: isRetryable(errType),
	}
}

// WithOriginalError adds the original error
func (e *ToolError) WithOriginalError(err error) *ToolError {
	e.OriginalError = err
	return e
}

// WithDetails adds additional details
func (e *ToolError) WithDetails(key string, value interface{}) *ToolError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// WithSuggestedFix adds a suggestion for fixing the error
func (e *ToolError) WithSuggestedFix(fix string) *ToolError {
	e.SuggestedFix = fix
	return e
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Retryable
	}
	return false
}

// isRetryable determines if an error type is retryable
func isRetryable(errType ErrorType) bool {
	switch errType {
	case ErrorTypeTimeout, ErrorTypeRateLimited, ErrorTypeInternalError:
		return true
	default:
		return false
	}
}

// Common error constructors

// ErrToolNotFound creates a tool not found error
func ErrToolNotFound(toolName string) *ToolError {
	return NewToolError(
		ErrorTypeNotFound,
		toolName,
		fmt.Sprintf("Tool '%s' is not registered", toolName),
	).WithSuggestedFix(
		"Check the tool name spelling or verify that the tool is properly registered",
	)
}

// ErrInvalidParams creates an invalid parameters error
func ErrInvalidParams(toolName, reason string) *ToolError {
	return NewToolError(
		ErrorTypeInvalidParams,
		toolName,
		fmt.Sprintf("Invalid parameters: %s", reason),
	).WithSuggestedFix(
		"Review the tool's parameter schema and provide valid values",
	)
}

// ErrExecutionFailed creates an execution failed error
func ErrExecutionFailed(toolName, reason string, originalErr error) *ToolError {
	return NewToolError(
		ErrorTypeExecutionFailed,
		toolName,
		reason,
	).WithOriginalError(originalErr)
}

// ErrTimeout creates a timeout error
func ErrTimeout(toolName string, duration interface{}) *ToolError {
	return NewToolError(
		ErrorTypeTimeout,
		toolName,
		"Tool execution timed out",
	).WithDetails("timeout_duration", duration).
		WithSuggestedFix("Try again or increase the timeout duration")
}

// ErrRateLimited creates a rate limited error
func ErrRateLimited(toolName string, retryAfter interface{}) *ToolError {
	return NewToolError(
		ErrorTypeRateLimited,
		toolName,
		"Tool execution rate limited",
	).WithDetails("retry_after", retryAfter).
		WithSuggestedFix("Wait before retrying the operation")
}
