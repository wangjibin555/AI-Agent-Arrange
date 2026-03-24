package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
)

// ginErrorAdapter keeps gin-specific behavior inside the API layer.
type ginErrorAdapter struct {
	handler  *midware.ErrorHandler
	resolver *midware.ErrorResolver
	now      func() time.Time
}

func newGinErrorAdapter() *ginErrorAdapter {
	return &ginErrorAdapter{
		handler: midware.NewErrorHandler(
			midware.WithErrorHandlerDetails(true),
			midware.WithErrorHandlerRequestID(true),
			midware.WithErrorHandlerTimestamp(true),
		),
		resolver: midware.NewErrorResolver(),
		now:      time.Now,
	}
}

func (a *ginErrorAdapter) Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				a.handler.HandlePanic(c.Writer, c.Request, rec)
				c.Abort()
			}
		}()

		c.Next()
	}
}

func (a *ginErrorAdapter) OK(c *gin.Context, data interface{}) {
	a.respond(c, http.StatusOK, data, "")
}

func (a *ginErrorAdapter) OKWithMessage(c *gin.Context, data interface{}, message string) {
	a.respond(c, http.StatusOK, data, message)
}

func (a *ginErrorAdapter) Created(c *gin.Context, data interface{}, message string) {
	a.respond(c, http.StatusCreated, data, message)
}

func (a *ginErrorAdapter) BindJSON(c *gin.Context, target interface{}) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		a.Abort(c, resolveBindError(err))
		return false
	}
	return true
}

func (a *ginErrorAdapter) Abort(c *gin.Context, err error) {
	a.handler.HandleError(c.Writer, c.Request, a.resolver.Resolve(err))
	c.Abort()
}

func (a *ginErrorAdapter) BadRequest(c *gin.Context, message string, cause error) {
	a.Abort(c, midware.NewHTTPError(http.StatusBadRequest, message).WithCause(cause))
}

func (a *ginErrorAdapter) NotFound(c *gin.Context, message string, cause error) {
	a.Abort(c, midware.NewHTTPError(http.StatusNotFound, message).WithCause(cause))
}

func (a *ginErrorAdapter) Internal(c *gin.Context, message string, cause error) {
	a.Abort(c, midware.NewHTTPError(http.StatusInternalServerError, message).WithCause(cause))
}

func (a *ginErrorAdapter) Gone(c *gin.Context, message string, cause error) {
	a.Abort(c, midware.NewHTTPError(http.StatusGone, message).WithCause(cause))
}

func (a *ginErrorAdapter) respond(c *gin.Context, statusCode int, data interface{}, message string) {
	c.JSON(statusCode, a.buildSuccessResponse(c.Request, data, message))
}

func (a *ginErrorAdapter) buildSuccessResponse(req *http.Request, data interface{}, message string) Response {
	resp := Response{
		Success: true,
		Data:    data,
		Message: message,
	}
	resp.RequestID = responseRequestID(req)
	if a.now != nil {
		resp.Timestamp = a.now().Format(time.RFC3339)
	}
	return resp
}

func responseRequestID(req *http.Request) string {
	if req == nil {
		return ""
	}
	if meta, ok := midware.RequestMetadataFromRequest(req); ok && meta.RequestID != "" {
		return meta.RequestID
	}
	return req.Header.Get(midware.HeaderRequestID)
}
