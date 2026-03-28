package tool

import (
	"errors"
	"fmt"
)

// ErrorType 表示工具执行错误的分类。
type ErrorType string

const (
	// ErrorTypeNotFound 表示工具未在注册中心中找到。
	ErrorTypeNotFound ErrorType = "TOOL_NOT_FOUND"

	// ErrorTypeInvalidParams 表示调用参数不合法。
	ErrorTypeInvalidParams ErrorType = "INVALID_PARAMETERS"

	// ErrorTypeExecutionFailed 表示工具执行过程失败。
	ErrorTypeExecutionFailed ErrorType = "EXECUTION_FAILED"

	// ErrorTypeTimeout 表示工具调用超时。
	ErrorTypeTimeout ErrorType = "TIMEOUT"

	// ErrorTypePermissionDenied 表示权限不足。
	ErrorTypePermissionDenied ErrorType = "PERMISSION_DENIED"

	// ErrorTypeRateLimited 表示调用被限流。
	ErrorTypeRateLimited ErrorType = "RATE_LIMITED"

	// ErrorTypeInternalError 表示系统内部错误。
	ErrorTypeInternalError ErrorType = "INTERNAL_ERROR"
)

// ToolError 表示结构化的工具执行错误。
type ToolError struct {
	Type          ErrorType              `json:"type"`
	Message       string                 `json:"message"`
	ToolName      string                 `json:"tool_name,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	Retryable     bool                   `json:"retryable"`
	OriginalError error                  `json:"-"`
	SuggestedFix  string                 `json:"suggested_fix,omitempty"`
}

// Error 实现 error 接口。
func (e *ToolError) Error() string {
	if e.ToolName != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Type, e.ToolName, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Type, e.Message)
}

// Unwrap 返回原始错误，便于 errors.Is / errors.As 继续透传。
func (e *ToolError) Unwrap() error {
	return e.OriginalError
}

// ToLLMMessage 将错误格式化为更适合 LLM 理解和修复的文本。
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

// NewToolError 创建一个新的 ToolError。
func NewToolError(errType ErrorType, toolName, message string) *ToolError {
	return &ToolError{
		Type:      errType,
		ToolName:  toolName,
		Message:   message,
		Details:   make(map[string]interface{}),
		Retryable: isRetryable(errType),
	}
}

// WithOriginalError 附加底层原始错误。
func (e *ToolError) WithOriginalError(err error) *ToolError {
	e.OriginalError = err
	return e
}

// WithDetails 追加结构化错误细节。
func (e *ToolError) WithDetails(key string, value interface{}) *ToolError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// WithSuggestedFix 补充建议修复方式。
func (e *ToolError) WithSuggestedFix(fix string) *ToolError {
	e.SuggestedFix = fix
	return e
}

// IsRetryable 判断一个错误是否适合重试。
func IsRetryable(err error) bool {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return toolErr.Retryable
	}
	return false
}

// isRetryable 根据错误类型判断是否允许重试。
func isRetryable(errType ErrorType) bool {
	switch errType {
	case ErrorTypeTimeout, ErrorTypeRateLimited, ErrorTypeInternalError:
		return true
	default:
		return false
	}
}

// 常用错误构造函数。

// ErrToolNotFound 创建“工具不存在”错误。
func ErrToolNotFound(toolName string) *ToolError {
	return NewToolError(
		ErrorTypeNotFound,
		toolName,
		fmt.Sprintf("Tool '%s' is not registered", toolName),
	).WithSuggestedFix(
		"Check the tool name spelling or verify that the tool is properly registered",
	)
}

// ErrInvalidParams 创建“参数非法”错误。
func ErrInvalidParams(toolName, reason string) *ToolError {
	return NewToolError(
		ErrorTypeInvalidParams,
		toolName,
		fmt.Sprintf("Invalid parameters: %s", reason),
	).WithSuggestedFix(
		"Review the tool's parameter schema and provide valid values",
	)
}

// ErrExecutionFailed 创建“执行失败”错误。
func ErrExecutionFailed(toolName, reason string, originalErr error) *ToolError {
	return NewToolError(
		ErrorTypeExecutionFailed,
		toolName,
		reason,
	).WithOriginalError(originalErr)
}

// ErrTimeout 创建“调用超时”错误。
func ErrTimeout(toolName string, duration interface{}) *ToolError {
	return NewToolError(
		ErrorTypeTimeout,
		toolName,
		"Tool execution timed out",
	).WithDetails("timeout_duration", duration).
		WithSuggestedFix("Try again or increase the timeout duration")
}

// ErrRateLimited 创建“限流”错误。
func ErrRateLimited(toolName string, retryAfter interface{}) *ToolError {
	return NewToolError(
		ErrorTypeRateLimited,
		toolName,
		"Tool execution rate limited",
	).WithDetails("retry_after", retryAfter).
		WithSuggestedFix("Wait before retrying the operation")
}
