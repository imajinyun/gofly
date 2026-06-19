// Package errors provides structured error types with gRPC-compatible codes for
// gofly services. It supports error wrapping, HTTP status code mapping, and
// sentinel error matching.
package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Code is a gRPC-compatible error code.
type Code string

const (
	CodeOK                 Code = "ok"
	CodeCanceled           Code = "canceled"
	CodeUnknown            Code = "unknown"
	CodeInvalidArgument    Code = "invalid_argument"
	CodeDeadlineExceeded   Code = "deadline_exceeded"
	CodeNotFound           Code = "not_found"
	CodeAlreadyExists      Code = "already_exists"
	CodePermissionDenied   Code = "permission_denied"
	CodeResourceExhausted  Code = "resource_exhausted"
	CodeFailedPrecondition Code = "failed_precondition"
	CodeAborted            Code = "aborted"
	CodeOutOfRange         Code = "out_of_range"
	CodeUnimplemented      Code = "unimplemented"
	CodeInternal           Code = "internal"
	CodeUnavailable        Code = "unavailable"
	CodeDataLoss           Code = "data_loss"
	CodeUnauthenticated    Code = "unauthenticated"
)

// Error is a structured error with a code, message, and optional wrapped cause.
type Error struct {
	Code Code   `json:"code"`
	Text string `json:"text"`
	Base error  `json:"-"`
}

// New creates a new Error with the given code and text.
func New(code Code, text string) *Error {
	if code == "" {
		code = CodeInternal
	}
	return &Error{Code: code, Text: text}
}

// Wrap creates a new Error that wraps base.
func Wrap(code Code, text string, base error) *Error {
	if code == "" {
		code = CodeInternal
	}
	return &Error{Code: code, Text: text, Base: base}
}

// Errorf creates a new Error with a formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return New(code, fmt.Sprintf(format, args...))
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Text == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Text
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Base
}

func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}
	if stderrors.Is(err, context.Canceled) {
		return CodeCanceled
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return CodeDeadlineExceeded
	}
	var coded *Error
	if stderrors.As(err, &coded) {
		return coded.Code
	}
	if st, ok := status.FromError(err); ok {
		return CodeFromGRPCStatus(st.Code())
	}
	return CodeInternal
}

func TextOf(err error) string {
	if err == nil {
		return ""
	}
	var coded *Error
	if stderrors.As(err, &coded) {
		return coded.Text
	}
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}

func MessageOf(err error) string { return TextOf(err) }

func (e *Error) HTTPStatus() int {
	if e == nil {
		return http.StatusOK
	}
	return HTTPStatus(e.Code)
}

func (e *Error) GRPCStatus() codes.Code {
	if e == nil {
		return codes.OK
	}
	return GRPCStatus(e.Code)
}

func HTTPStatus(code Code) int {
	switch code {
	case CodeOK:
		return http.StatusOK
	case CodeCanceled:
		return 499
	case CodeUnknown:
		return http.StatusInternalServerError
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists:
		return http.StatusConflict
	case CodePermissionDenied:
		return http.StatusForbidden
	case CodeResourceExhausted:
		return http.StatusTooManyRequests
	case CodeFailedPrecondition:
		return http.StatusBadRequest
	case CodeAborted:
		return http.StatusConflict
	case CodeOutOfRange:
		return http.StatusBadRequest
	case CodeUnimplemented:
		return http.StatusNotImplemented
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeDataLoss:
		return http.StatusInternalServerError
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

func HTTPStatusFromCode(code Code) int { return HTTPStatus(code) }

func GRPCStatus(code Code) codes.Code {
	switch code {
	case CodeOK:
		return codes.OK
	case CodeCanceled:
		return codes.Canceled
	case CodeUnknown:
		return codes.Unknown
	case CodeInvalidArgument:
		return codes.InvalidArgument
	case CodeDeadlineExceeded:
		return codes.DeadlineExceeded
	case CodeNotFound:
		return codes.NotFound
	case CodeAlreadyExists:
		return codes.AlreadyExists
	case CodePermissionDenied:
		return codes.PermissionDenied
	case CodeResourceExhausted:
		return codes.ResourceExhausted
	case CodeFailedPrecondition:
		return codes.FailedPrecondition
	case CodeAborted:
		return codes.Aborted
	case CodeOutOfRange:
		return codes.OutOfRange
	case CodeUnimplemented:
		return codes.Unimplemented
	case CodeInternal:
		return codes.Internal
	case CodeUnavailable:
		return codes.Unavailable
	case CodeDataLoss:
		return codes.DataLoss
	case CodeUnauthenticated:
		return codes.Unauthenticated
	default:
		return codes.Internal
	}
}

func GRPCStatusFromCode(code Code) codes.Code { return GRPCStatus(code) }

func CodeFromGRPCStatus(code codes.Code) Code {
	switch code {
	case codes.OK:
		return CodeOK
	case codes.Canceled:
		return CodeCanceled
	case codes.Unknown:
		return CodeUnknown
	case codes.InvalidArgument:
		return CodeInvalidArgument
	case codes.DeadlineExceeded:
		return CodeDeadlineExceeded
	case codes.NotFound:
		return CodeNotFound
	case codes.AlreadyExists:
		return CodeAlreadyExists
	case codes.PermissionDenied:
		return CodePermissionDenied
	case codes.ResourceExhausted:
		return CodeResourceExhausted
	case codes.FailedPrecondition:
		return CodeFailedPrecondition
	case codes.Aborted:
		return CodeAborted
	case codes.OutOfRange:
		return CodeOutOfRange
	case codes.Unimplemented:
		return CodeUnimplemented
	case codes.Internal:
		return CodeInternal
	case codes.Unavailable:
		return CodeUnavailable
	case codes.DataLoss:
		return CodeDataLoss
	case codes.Unauthenticated:
		return CodeUnauthenticated
	default:
		return CodeInternal
	}
}

func GRPCError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(GRPCStatus(CodeOf(err)), TextOf(err))
}

func Retryable(err error) bool {
	switch CodeOf(err) {
	case CodeUnavailable, CodeDeadlineExceeded, CodeResourceExhausted, CodeAborted, CodeInternal:
		return true
	default:
		return false
	}
}
