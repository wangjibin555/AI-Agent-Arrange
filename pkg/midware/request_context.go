package midware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const (
	HeaderRequestID    = "X-Request-ID"
	HeaderTraceID      = "X-Trace-ID"
	HeaderUserID       = "X-User-ID"
	HeaderTenantID     = "X-Tenant-ID"
	HeaderAuth         = "Authorization"
	HeaderForwardedFor = "X-Forwarded-For"
)

type contextKey string

const requestMetadataKey contextKey = "request-metadata"

// RequestMetadata carries inbound request context that should be preserved
// across logging, permission propagation, and outbound network calls.
type RequestMetadata struct {
	RequestID      string
	TraceID        string
	UserID         string
	TenantID       string
	SourceIP       string
	Method         string
	Path           string
	UserAgent      string
	ForwardHeaders map[string]string
}

func NewRequestID() string {
	return uuid.NewString()
}

func WithRequestMetadata(ctx context.Context, meta RequestMetadata) context.Context {
	return context.WithValue(ctx, requestMetadataKey, meta)
}

func RequestMetadataFromContext(ctx context.Context) (RequestMetadata, bool) {
	meta, ok := ctx.Value(requestMetadataKey).(RequestMetadata)
	return meta, ok
}

func RequestMetadataFromRequest(req *http.Request) (RequestMetadata, bool) {
	if req == nil {
		return RequestMetadata{}, false
	}
	return RequestMetadataFromContext(req.Context())
}

func RequestMetadataToMap(meta RequestMetadata) map[string]string {
	values := map[string]string{}
	if meta.RequestID != "" {
		values["request_id"] = meta.RequestID
	}
	if meta.TraceID != "" {
		values["trace_id"] = meta.TraceID
	}
	if meta.UserID != "" {
		values["user_id"] = meta.UserID
	}
	if meta.TenantID != "" {
		values["tenant_id"] = meta.TenantID
	}
	if meta.SourceIP != "" {
		values["source_ip"] = meta.SourceIP
	}
	if meta.Method != "" {
		values["method"] = meta.Method
	}
	if meta.Path != "" {
		values["path"] = meta.Path
	}
	if meta.UserAgent != "" {
		values["user_agent"] = meta.UserAgent
	}
	for key, value := range meta.ForwardHeaders {
		if value == "" {
			continue
		}
		values["header."+key] = value
	}
	return values
}

func RequestMetadataFromMap(values map[string]string) RequestMetadata {
	if len(values) == 0 {
		return RequestMetadata{}
	}

	forwardHeaders := map[string]string{}
	for key, value := range values {
		if value == "" || len(key) <= len("header.") || key[:len("header.")] != "header." {
			continue
		}
		forwardHeaders[key[len("header."):]] = value
	}

	return RequestMetadata{
		RequestID:      values["request_id"],
		TraceID:        values["trace_id"],
		UserID:         values["user_id"],
		TenantID:       values["tenant_id"],
		SourceIP:       values["source_ip"],
		Method:         values["method"],
		Path:           values["path"],
		UserAgent:      values["user_agent"],
		ForwardHeaders: forwardHeaders,
	}
}

func BuildRequestMetadata(req *http.Request, clientIP string) RequestMetadata {
	requestID := firstNonEmpty(req.Header.Get(HeaderRequestID), NewRequestID())
	traceID := firstNonEmpty(req.Header.Get(HeaderTraceID), requestID)

	forwardHeaders := map[string]string{}
	copyHeaderIfPresent(req, forwardHeaders, HeaderAuth)
	copyHeaderIfPresent(req, forwardHeaders, HeaderUserID)
	copyHeaderIfPresent(req, forwardHeaders, HeaderTenantID)
	copyHeaderIfPresent(req, forwardHeaders, HeaderRequestID)
	copyHeaderIfPresent(req, forwardHeaders, HeaderTraceID)

	return RequestMetadata{
		RequestID:      requestID,
		TraceID:        traceID,
		UserID:         req.Header.Get(HeaderUserID),
		TenantID:       req.Header.Get(HeaderTenantID),
		SourceIP:       clientIP,
		Method:         req.Method,
		Path:           req.URL.Path,
		UserAgent:      req.UserAgent(),
		ForwardHeaders: forwardHeaders,
	}
}

func InjectForwardHeaders(req *http.Request) {
	meta, ok := RequestMetadataFromRequest(req)
	if !ok {
		return
	}

	for key, value := range meta.ForwardHeaders {
		if value == "" {
			continue
		}
		req.Header.Set(key, value)
	}

	if req.Header.Get(HeaderRequestID) == "" {
		req.Header.Set(HeaderRequestID, meta.RequestID)
	}
	if req.Header.Get(HeaderTraceID) == "" {
		req.Header.Set(HeaderTraceID, meta.TraceID)
	}
}

func copyHeaderIfPresent(req *http.Request, dst map[string]string, key string) {
	if value := req.Header.Get(key); value != "" {
		dst[key] = value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
