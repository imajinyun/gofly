// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	coreerrors "github.com/gofly/gofly/core/errors"
)

type Code = coreerrors.Code

const (
	CodeOK                 = coreerrors.CodeOK
	CodeCanceled           = coreerrors.CodeCanceled
	CodeUnknown            = coreerrors.CodeUnknown
	CodeInvalidArgument    = coreerrors.CodeInvalidArgument
	CodeDeadlineExceeded   = coreerrors.CodeDeadlineExceeded
	CodeNotFound           = coreerrors.CodeNotFound
	CodeAlreadyExists      = coreerrors.CodeAlreadyExists
	CodePermissionDenied   = coreerrors.CodePermissionDenied
	CodeResourceExhausted  = coreerrors.CodeResourceExhausted
	CodeFailedPrecondition = coreerrors.CodeFailedPrecondition
	CodeAborted            = coreerrors.CodeAborted
	CodeOutOfRange         = coreerrors.CodeOutOfRange
	CodeUnimplemented      = coreerrors.CodeUnimplemented
	CodeInternal           = coreerrors.CodeInternal
	CodeUnavailable        = coreerrors.CodeUnavailable
	CodeDataLoss           = coreerrors.CodeDataLoss
	CodeUnauthenticated    = coreerrors.CodeUnauthenticated
)

type Error = coreerrors.Error

func NewError(code Code, text string) *Error {
	return coreerrors.New(code, text)
}

func Errorf(code Code, format string, args ...any) *Error {
	return coreerrors.Errorf(code, format, args...)
}

func CodeOf(err error) Code {
	return coreerrors.CodeOf(err)
}

func textOf(err error) string {
	return coreerrors.TextOf(err)
}

func messageOf(err error) string {
	return textOf(err)
}

func httpStatusFromCode(code Code) int {
	return coreerrors.HTTPStatus(code)
}

func isRetryable(err error) bool {
	return coreerrors.Retryable(err)
}
