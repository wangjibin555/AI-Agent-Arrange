package midware

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// HTTPError carries a public-facing response shape and the target HTTP status.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
	Err        error
	Details    map[string]any
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

func (e *HTTPError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewHTTPError(statusCode int, message string) *HTTPError {
	return &HTTPError{
		StatusCode: statusCode,
		Message:    message,
	}
}

func (e *HTTPError) WithCode(code string) *HTTPError {
	e.Code = code
	return e
}

func (e *HTTPError) WithCause(err error) *HTTPError {
	e.Err = err
	return e
}

func (e *HTTPError) WithDetails(details map[string]any) *HTTPError {
	if len(details) == 0 {
		return e
	}
	if e.Details == nil {
		e.Details = make(map[string]any, len(details))
	}
	for key, value := range details {
		e.Details[key] = value
	}
	return e
}

func (e *HTTPError) WithDetail(key string, value any) *HTTPError {
	return e.WithDetails(map[string]any{key: value})
}

type ErrorHandler struct {
	includeDetails   bool
	includeRequestID bool
	includeTimestamp bool
	now              func() time.Time
}

type ErrorHandlerOption func(*ErrorHandler)

func NewErrorHandler(opts ...ErrorHandlerOption) *ErrorHandler {
	h := &ErrorHandler{
		now: time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func WithErrorHandlerDetails(enabled bool) ErrorHandlerOption {
	return func(h *ErrorHandler) {
		h.includeDetails = enabled
	}
}

func WithErrorHandlerRequestID(enabled bool) ErrorHandlerOption {
	return func(h *ErrorHandler) {
		h.includeRequestID = enabled
	}
}

func WithErrorHandlerTimestamp(enabled bool) ErrorHandlerOption {
	return func(h *ErrorHandler) {
		h.includeTimestamp = enabled
	}
}

func WithErrorHandlerNow(now func() time.Time) ErrorHandlerOption {
	return func(h *ErrorHandler) {
		if now != nil {
			h.now = now
		}
	}
}

type errorResponse struct {
	Success   bool           `json:"success"`
	Error     string         `json:"error"`
	Code      string         `json:"code,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Timestamp string         `json:"timestamp,omitempty"`
}

func (h *ErrorHandler) HandleError(w http.ResponseWriter, r *http.Request, err error) {
	statusCode := http.StatusInternalServerError
	message := "internal server error"
	code := ""
	var details map[string]any

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode > 0 {
			statusCode = httpErr.StatusCode
		}
		if httpErr.Message != "" {
			message = httpErr.Message
		}
		if httpErr.Code != "" {
			code = httpErr.Code
		}
		if len(httpErr.Details) > 0 {
			details = cloneDetails(httpErr.Details)
		}
	}
	if code == "" {
		code = defaultErrorCodeForStatus(statusCode)
	}

	h.logError(r, err, statusCode, code, message, details)
	writeJSON(w, statusCode, h.buildErrorResponse(r, statusCode, message, code, details))
}

func (h *ErrorHandler) HandlePanic(w http.ResponseWriter, r *http.Request, rec any) {
	fields := []zap.Field{
		zap.Any("panic", rec),
		zap.ByteString("stack", debug.Stack()),
	}
	if r != nil {
		fields = append(fields,
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
		)
		if ip := clientIPFromRequest(r); ip != "" {
			fields = append(fields, zap.String("ip", ip))
		}
		if meta, ok := RequestMetadataFromRequest(r); ok {
			if meta.RequestID != "" {
				fields = append(fields, zap.String("request_id", meta.RequestID))
			}
			if meta.TraceID != "" {
				fields = append(fields, zap.String("trace_id", meta.TraceID))
			}
		}
	}

	logger.Error("HTTP panic recovered", fields...)
	writeJSON(w, http.StatusInternalServerError, h.buildErrorResponse(r, http.StatusInternalServerError, "internal server error", "internal_error", nil))
}

func (h *ErrorHandler) buildErrorResponse(r *http.Request, statusCode int, message, code string, details map[string]any) errorResponse {
	resp := errorResponse{
		Success: false,
		Error:   message,
		Code:    code,
	}
	if h.includeDetails && len(details) > 0 {
		resp.Details = cloneDetails(details)
	}
	if h.includeRequestID {
		resp.RequestID = requestIDFromRequest(r)
	}
	if h.includeTimestamp && h.now != nil {
		resp.Timestamp = h.now().Format(time.RFC3339)
	}
	return resp
}

func (h *ErrorHandler) logError(r *http.Request, err error, statusCode int, code, message string, details map[string]any) {
	fields := []zap.Field{
		zap.Int("status", statusCode),
		zap.String("code", code),
		zap.String("error", message),
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	if len(details) > 0 {
		fields = append(fields, zap.Any("details", details))
	}
	if r != nil {
		fields = append(fields,
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
		)
		if ip := clientIPFromRequest(r); ip != "" {
			fields = append(fields, zap.String("ip", ip))
		}
		if meta, ok := RequestMetadataFromRequest(r); ok {
			if meta.RequestID != "" {
				fields = append(fields, zap.String("request_id", meta.RequestID))
			}
			if meta.TraceID != "" {
				fields = append(fields, zap.String("trace_id", meta.TraceID))
			}
		}
	}

	logger.Error("HTTP request failed", fields...)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	if w == nil {
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("Failed to write JSON response", zap.Error(fmt.Errorf("encode json response: %w", err)))
	}
}

func clientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	return r.RemoteAddr
}

func requestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if meta, ok := RequestMetadataFromRequest(r); ok && meta.RequestID != "" {
		return meta.RequestID
	}
	return r.Header.Get(HeaderRequestID)
}

func cloneDetails(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func defaultErrorCodeForStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusGone:
		return "gone"
	case http.StatusRequestTimeout:
		return "request_timeout"
	case http.StatusGatewayTimeout:
		return "gateway_timeout"
	default:
		return "internal_error"
	}
}
