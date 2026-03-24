package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
)

func resolveBindError(err error) *midware.HTTPError {
	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		fe := validationErrs[0]
		field := lowerFirst(fe.Field())
		message := "field '" + field + "' is invalid"
		if fe.Tag() == "required" {
			message = "field '" + field + "' is required"
		}
		return midware.NewHTTPError(http.StatusBadRequest, message).
			WithCode("invalid_request").
			WithCause(err).
			WithDetails(map[string]any{
				"field":  field,
				"reason": fe.Tag(),
			})
	}

	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) || errors.Is(err, io.EOF) {
		return midware.NewHTTPError(http.StatusBadRequest, "request body must be valid JSON").
			WithCode("invalid_json").
			WithCause(err)
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return midware.NewHTTPError(http.StatusBadRequest, "field '"+field+"' has invalid type").
			WithCode("invalid_request").
			WithCause(err).
			WithDetails(map[string]any{
				"field": field,
				"type":  typeErr.Type.String(),
			})
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, "unexpected EOF") ||
		strings.Contains(errMsg, "invalid character") ||
		strings.Contains(errMsg, "cannot unmarshal") {
		return midware.NewHTTPError(http.StatusBadRequest, "request body must be valid JSON").
			WithCode("invalid_json").
			WithCause(err)
	}

	return midware.NewHTTPError(http.StatusBadRequest, "invalid request payload").
		WithCode("invalid_request").
		WithCause(err)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
