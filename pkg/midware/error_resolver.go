package midware

import (
	"context"
	"errors"
	"net/http"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

type ErrorMapping struct {
	Match func(error) bool
	Map   func(error) *HTTPError
}

type ErrorResolver struct {
	internalMessage string
	mappings        []ErrorMapping
}

type ErrorResolverOption func(*ErrorResolver)

func NewErrorResolver(opts ...ErrorResolverOption) *ErrorResolver {
	r := &ErrorResolver{
		internalMessage: "internal server error",
	}
	r.registerDefaultMappings()
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func WithResolverInternalMessage(message string) ErrorResolverOption {
	return func(r *ErrorResolver) {
		if message != "" {
			r.internalMessage = message
		}
	}
}

func WithResolverMappings(mappings ...ErrorMapping) ErrorResolverOption {
	return func(r *ErrorResolver) {
		r.mappings = append(r.mappings, mappings...)
	}
}

func (r *ErrorResolver) RegisterMapping(mapping ErrorMapping) {
	if mapping.Match == nil || mapping.Map == nil {
		return
	}
	r.mappings = append(r.mappings, mapping)
}

func (r *ErrorResolver) Resolve(err error) *HTTPError {
	if err == nil {
		return NewHTTPError(http.StatusInternalServerError, r.internalMessage).
			WithCode("internal_error")
	}

	for i := len(r.mappings) - 1; i >= 0; i-- {
		mapping := r.mappings[i]
		if mapping.Match == nil || mapping.Map == nil || !mapping.Match(err) {
			continue
		}
		if resolved := mapping.Map(err); resolved != nil {
			return r.sanitize(resolved, err)
		}
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return r.sanitize(httpErr, err)
	}

	return r.sanitize(
		NewHTTPError(http.StatusInternalServerError, r.internalMessage).
			WithCode("internal_error").
			WithCause(err),
		err,
	)
}

func (r *ErrorResolver) registerDefaultMappings() {
	r.mappings = append(r.mappings,
		ErrorMapping{
			Match: func(err error) bool {
				var appErr *apperr.Error
				return errors.As(err, &appErr)
			},
			Map: func(err error) *HTTPError {
				var appErr *apperr.Error
				if !errors.As(err, &appErr) {
					return nil
				}

				code := appErr.Code
				if code == "" {
					code = defaultAppErrorCode(appErr.Kind)
				}

				httpErr := NewHTTPError(appErrorStatus(appErr.Kind), appErr.Error()).
					WithCode(code).
					WithCause(err)
				if len(appErr.Details) > 0 {
					httpErr.WithDetails(appErr.Details)
				}
				return httpErr
			},
		},
		ErrorMapping{
			Match: func(err error) bool { return errors.Is(err, context.Canceled) },
			Map: func(err error) *HTTPError {
				return NewHTTPError(http.StatusRequestTimeout, "request canceled").
					WithCode("request_canceled").
					WithCause(err)
			},
		},
		ErrorMapping{
			Match: func(err error) bool { return errors.Is(err, context.DeadlineExceeded) },
			Map: func(err error) *HTTPError {
				return NewHTTPError(http.StatusGatewayTimeout, "request timed out").
					WithCode("request_timeout").
					WithCause(err)
			},
		},
	)
}

func (r *ErrorResolver) sanitize(httpErr *HTTPError, cause error) *HTTPError {
	if httpErr == nil {
		httpErr = NewHTTPError(http.StatusInternalServerError, r.internalMessage)
	}

	resolved := NewHTTPError(httpErr.StatusCode, httpErr.Message)
	resolved.Code = httpErr.Code
	resolved.Err = httpErr.Err
	if resolved.Err == nil {
		resolved.Err = cause
	}
	if len(httpErr.Details) > 0 {
		resolved.WithDetails(httpErr.Details)
	}

	if resolved.StatusCode == 0 {
		resolved.StatusCode = http.StatusInternalServerError
	}
	if resolved.Message == "" {
		if resolved.StatusCode >= http.StatusInternalServerError {
			resolved.Message = r.internalMessage
		} else {
			resolved.Message = http.StatusText(resolved.StatusCode)
		}
	}
	if resolved.Code == "" {
		resolved.Code = defaultErrorCodeForStatus(resolved.StatusCode)
	}

	return resolved
}

func appErrorStatus(kind apperr.Kind) int {
	switch kind {
	case apperr.KindInvalidArgument:
		return http.StatusBadRequest
	case apperr.KindNotFound:
		return http.StatusNotFound
	case apperr.KindConflict:
		return http.StatusConflict
	case apperr.KindGone:
		return http.StatusGone
	default:
		return http.StatusInternalServerError
	}
}

func defaultAppErrorCode(kind apperr.Kind) string {
	switch kind {
	case apperr.KindInvalidArgument:
		return "invalid_argument"
	case apperr.KindNotFound:
		return "not_found"
	case apperr.KindConflict:
		return "conflict"
	case apperr.KindGone:
		return "gone"
	default:
		return "internal_error"
	}
}
