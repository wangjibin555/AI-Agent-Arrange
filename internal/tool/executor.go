package tool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
)

// ExecutionConfig 定义工具执行器的运行配置。
type ExecutionConfig struct {
	Timeout      time.Duration
	MaxRetries   int
	RetryDelay   time.Duration
	RetryBackoff float64 // 指数退避倍率，例如 2.0
}

// DefaultExecutionConfig 返回一组通用默认配置。
func DefaultExecutionConfig() *ExecutionConfig {
	return &ExecutionConfig{
		Timeout:      30 * time.Second,
		MaxRetries:   3,
		RetryDelay:   1 * time.Second,
		RetryBackoff: 2.0,
	}
}

// Executor 负责安全执行工具调用。
// 它统一处理参数校验、超时控制、重试策略、panic 保护和指标上报。
type Executor struct {
	registry *Registry
	config   *ExecutionConfig
	metrics  *monitor.ToolMetrics
}

// NewExecutor 创建工具执行器。
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

// Execute 执行一次工具调用，并统一处理超时、重试和错误包装。
func (e *Executor) Execute(ctx context.Context, toolName string, params map[string]interface{}) (*Result, error) {
	start := time.Now()
	e.observeToolStarted(toolName)

	// 先从注册中心解析工具实例。
	toolInstance, err := e.registry.Get(toolName)
	if err != nil {
		e.observeToolFinished(toolName, "not_found", start)
		return nil, ErrToolNotFound(toolName)
	}

	// 再按工具 schema 校验参数。
	if err := e.validateParams(toolInstance, params); err != nil {
		e.observeToolValidationFailed(toolName)
		e.observeToolFinished(toolName, "validation_failed", start)
		return nil, err
	}

	// 最后在统一的重试框架中执行工具。
	var lastErr error
	retryDelay := e.config.RetryDelay

	for attempt := 0; attempt <= e.config.MaxRetries; attempt++ {
		// 为本次尝试附加独立超时控制。
		execCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)

		// 将真实工具调用放到 goroutine 中，便于同时监听结果和超时。
		resultChan := make(chan *Result, 1)
		errChan := make(chan error, 1)

		go func() {
			// 拦截工具 panic，避免异常直接打崩调用方。
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

		// 等待结果、错误或超时信号。
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
				return result, nil // 成功返回
			}
		}

		// 只有可重试错误才继续下一轮尝试。
		if !IsRetryable(lastErr) {
			break // 非可重试错误直接结束
		}
		e.observeToolRetry(toolName, retryReason(lastErr))

		// 最后一轮失败后不再等待。
		if attempt < e.config.MaxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
				// 使用指数退避降低连续失败时的冲击。
				retryDelay = time.Duration(float64(retryDelay) * e.config.RetryBackoff)
			}
		}
	}

	e.observeToolFinished(toolName, toolStatus(lastErr), start)
	return nil, lastErr
}

// validateParams 按工具 schema 校验请求参数。
func (e *Executor) validateParams(tool Tool, params map[string]interface{}) error {
	def := tool.GetDefinition()
	if def == nil || def.Parameters == nil {
		return nil // 没有 schema 时跳过校验
	}

	// 先检查必填参数。
	for _, required := range def.Parameters.Required {
		if _, exists := params[required]; !exists {
			return ErrInvalidParams(
				def.Name,
				fmt.Sprintf("missing required parameter: %s", required),
			).WithDetails("required_params", def.Parameters.Required)
		}
	}

	// 再检查每个参数是否存在于 schema 且类型正确。
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

// validateType 校验单个参数值是否符合 schema 定义的类型。
func (e *Executor) validateType(toolName, paramName string, value interface{}, schema *PropertySchema) error {
	actualType := getJSONType(value)

	// 针对 integer/number 做一层兼容处理。
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

	// 如果声明了枚举值，还需要额外校验取值是否合法。
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
