package apperr

import (
	"errors"
	"fmt"
)

type Kind string

const (
	KindInvalidArgument Kind = "invalid_argument"
	KindNotFound        Kind = "not_found"
	KindConflict        Kind = "conflict"
	KindGone            Kind = "gone"
	KindInternal        Kind = "internal"
)

type Error struct {
	Kind    Kind
	Code    string
	Message string
	Err     error
	Details map[string]any
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *Error) Clone() *Error {
	if e == nil {
		return nil
	}

	clone := *e
	if len(e.Details) > 0 {
		clone.Details = cloneMap(e.Details)
	}
	return &clone
}

func (e *Error) WithCause(err error) *Error {
	e.Err = err
	return e
}

func (e *Error) WithCode(code string) *Error {
	e.Code = code
	return e
}

func (e *Error) WithDetails(details map[string]any) *Error {
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

func (e *Error) WithDetail(key string, value any) *Error {
	return e.WithDetails(map[string]any{key: value})
}

func Wrap(err error, kind Kind, code, message string) *Error {
	if err == nil {
		return E(kind, message).WithCode(code)
	}

	var appErr *Error
	if errors.As(err, &appErr) {
		clone := appErr.Clone()
		if clone.Kind == "" {
			clone.Kind = kind
		}
		if clone.Code == "" {
			clone.Code = code
		}
		if clone.Message == "" {
			clone.Message = message
		}
		if clone.Err == nil {
			clone.Err = err
		}
		return clone
	}

	return E(kind, message).WithCode(code).WithCause(err)
}

func E(kind Kind, message string) *Error {
	return &Error{
		Kind:    kind,
		Code:    defaultCode(kind),
		Message: message,
	}
}

func InvalidArgument(message string) *Error {
	return E(KindInvalidArgument, message)
}

func InvalidArgumentf(format string, args ...interface{}) *Error {
	return InvalidArgument(fmt.Sprintf(format, args...))
}

func NotFound(message string) *Error {
	return E(KindNotFound, message)
}

func NotFoundf(format string, args ...interface{}) *Error {
	return NotFound(fmt.Sprintf(format, args...))
}

func Conflict(message string) *Error {
	return E(KindConflict, message)
}

func Conflictf(format string, args ...interface{}) *Error {
	return Conflict(fmt.Sprintf(format, args...))
}

func Gone(message string) *Error {
	return E(KindGone, message)
}

func Gonef(format string, args ...interface{}) *Error {
	return Gone(fmt.Sprintf(format, args...))
}

func Internal(message string) *Error {
	return E(KindInternal, message)
}

func Internalf(format string, args ...interface{}) *Error {
	return Internal(fmt.Sprintf(format, args...))
}

func defaultCode(kind Kind) string {
	switch kind {
	case KindInvalidArgument:
		return "invalid_argument"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindGone:
		return "gone"
	default:
		return "internal_error"
	}
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
