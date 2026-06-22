package middleware

import (
	"encoding/json"
	"net/http"

	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/rest"
)

// JSONValidation binds and validates JSON, writing field-aware validation
// errors in the standard REST error envelope.
func JSONValidation(c *rest.Context, value any) bool {
	if err := c.Bind(value); err != nil {
		WriteValidationError(c.Response, err)
		return false
	}
	return true
}

// RequestValidationHandler builds a handler that binds JSON, validates it, and
// calls onValid on success. This helper can be copied into internal/middleware
// alongside other reusable request-boundary helpers.
func RequestValidationHandler[T any](onValid func(*rest.Context, T)) rest.HandlerFunc {
	return func(c *rest.Context) {
		var req T
		if !JSONValidation(c, &req) {
			return
		}
		onValid(c, req)
	}
}

// WriteValidationError writes a validation error response including stable field details.
func WriteValidationError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(rest.ErrorResponse{Code: coreerrors.CodeInvalidArgument, Text: "invalid request", Message: err.Error(), Status: http.StatusBadRequest, Fields: rest.ValidationFailuresOf(err)})
}
