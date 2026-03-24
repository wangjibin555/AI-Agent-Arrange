package midware

import (
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// RequestContextMiddleware extracts request-scoped metadata and writes it into
// both the request context and the response headers.
func RequestContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		meta := BuildRequestMetadata(c.Request, c.ClientIP())
		c.Request = c.Request.WithContext(WithRequestMetadata(c.Request.Context(), meta))

		c.Writer.Header().Set(HeaderRequestID, meta.RequestID)
		c.Writer.Header().Set(HeaderTraceID, meta.TraceID)
		c.Next()
	}
}

// AccessLogMiddleware writes structured access logs with request metadata.
func AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		meta, _ := RequestMetadataFromRequest(c.Request)
		fields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", time.Since(start)),
			zap.Int("bytes", c.Writer.Size()),
			zap.String("user-agent", c.Request.UserAgent()),
		}

		if meta.RequestID != "" {
			fields = append(fields, zap.String("request_id", meta.RequestID))
		}
		if meta.TraceID != "" {
			fields = append(fields, zap.String("trace_id", meta.TraceID))
		}
		if meta.UserID != "" {
			fields = append(fields, zap.String("user_id", meta.UserID))
		}
		if meta.TenantID != "" {
			fields = append(fields, zap.String("tenant_id", meta.TenantID))
		}
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("errors", c.Errors.String()))
		}

		logger.Info("HTTP request", fields...)
	}
}

// RecoveryMiddleware catches panics and emits structured error logs.
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				meta, _ := RequestMetadataFromRequest(c.Request)
				fields := []zap.Field{
					zap.Any("panic", rec),
					zap.ByteString("stack", debug.Stack()),
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
					zap.String("ip", c.ClientIP()),
				}
				if meta.RequestID != "" {
					fields = append(fields, zap.String("request_id", meta.RequestID))
				}
				if meta.TraceID != "" {
					fields = append(fields, zap.String("trace_id", meta.TraceID))
				}

				logger.Error("HTTP panic recovered", fields...)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"error":   "internal server error",
				})
			}
		}()

		c.Next()
	}
}

// CORSMiddleware handles CORS and exposes request tracing headers for clients.
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", strings.Join([]string{
			"Content-Type",
			"Authorization",
			HeaderRequestID,
			HeaderTraceID,
			HeaderUserID,
			HeaderTenantID,
		}, ", "))
		c.Writer.Header().Set("Access-Control-Expose-Headers", strings.Join([]string{
			HeaderRequestID,
			HeaderTraceID,
		}, ", "))

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
