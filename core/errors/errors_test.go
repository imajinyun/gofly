package errors

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCodeOfAndTextOf(t *testing.T) {
	err := Wrap(CodeUnavailable, "upstream unavailable", stderrors.New("dial refused"))
	if got := CodeOf(err); got != CodeUnavailable {
		t.Fatalf("code = %s, want %s", got, CodeUnavailable)
	}
	if got := TextOf(err); got != "upstream unavailable" {
		t.Fatalf("text = %q, want upstream unavailable", got)
	}
	if got := MessageOf(err); got != "upstream unavailable" {
		t.Fatalf("message compatibility = %q, want upstream unavailable", got)
	}
	if !stderrors.Is(err, err.Base) {
		t.Fatal("wrapped base error should be inspectable")
	}
}

func TestConstructorsAndErrorString(t *testing.T) {
	base := stderrors.New("root cause")

	created := New("", "missing code")
	if created.Code != CodeInternal {
		t.Fatalf("New empty code = %s, want %s", created.Code, CodeInternal)
	}
	if created.Error() != "internal: missing code" {
		t.Fatalf("New empty code error = %q, want internal: missing code", created.Error())
	}

	wrapped := Wrap("", "wrapped", base)
	if wrapped.Code != CodeInternal {
		t.Fatalf("Wrap empty code = %s, want %s", wrapped.Code, CodeInternal)
	}
	if !stderrors.Is(wrapped, base) {
		t.Fatal("Wrap should preserve base error for errors.Is")
	}

	formatted := Errorf(CodeInvalidArgument, "field %s is required", "name")
	if formatted.Code != CodeInvalidArgument {
		t.Fatalf("Errorf code = %s, want %s", formatted.Code, CodeInvalidArgument)
	}
	if formatted.Text != "field name is required" {
		t.Fatalf("Errorf text = %q, want field name is required", formatted.Text)
	}

	codeOnly := New(CodeNotFound, "")
	if got := codeOnly.Error(); got != string(CodeNotFound) {
		t.Fatalf("empty text error = %q, want %q", got, CodeNotFound)
	}

	var nilErr *Error
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil Error() = %q, want empty string", got)
	}
	if got := nilErr.Unwrap(); got != nil {
		t.Fatalf("nil Unwrap() = %v, want nil", got)
	}
}

func TestNilAndPlainErrorHelpers(t *testing.T) {
	if got := CodeOf(nil); got != CodeOK {
		t.Fatalf("nil code = %s, want %s", got, CodeOK)
	}
	if got := TextOf(nil); got != "" {
		t.Fatalf("nil text = %q, want empty string", got)
	}

	plain := stderrors.New("plain failure")
	if got := CodeOf(plain); got != CodeInternal {
		t.Fatalf("plain error code = %s, want %s", got, CodeInternal)
	}
	if got := TextOf(plain); got != "plain failure" {
		t.Fatalf("plain error text = %q, want plain failure", got)
	}
}

func TestErrorReceiverStatusMethods(t *testing.T) {
	var nilErr *Error
	if got := nilErr.HTTPStatus(); got != http.StatusOK {
		t.Fatalf("nil HTTPStatus = %d, want %d", got, http.StatusOK)
	}
	if got := nilErr.GRPCStatus(); got != codes.OK {
		t.Fatalf("nil GRPCStatus = %s, want %s", got, codes.OK)
	}

	err := New(CodeUnauthenticated, "login required")
	if got := err.HTTPStatus(); got != http.StatusUnauthorized {
		t.Fatalf("error HTTPStatus = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := err.GRPCStatus(); got != codes.Unauthenticated {
		t.Fatalf("error GRPCStatus = %s, want %s", got, codes.Unauthenticated)
	}
}

func TestHTTPStatusAndRetryable(t *testing.T) {
	if got := HTTPStatus(CodeResourceExhausted); got != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", got, http.StatusTooManyRequests)
	}
	if !Retryable(New(CodeDeadlineExceeded, "timeout")) {
		t.Fatal("deadline exceeded should be retryable")
	}
	if Retryable(New(CodeInvalidArgument, "bad request")) {
		t.Fatal("invalid argument should not be retryable")
	}
}

func TestTransportMappings(t *testing.T) {
	tests := []struct {
		name string
		code Code
		http int
		grpc codes.Code
	}{
		{name: "ok", code: CodeOK, http: http.StatusOK, grpc: codes.OK},
		{name: "canceled", code: CodeCanceled, http: 499, grpc: codes.Canceled},
		{name: "invalid argument", code: CodeInvalidArgument, http: http.StatusBadRequest, grpc: codes.InvalidArgument},
		{name: "deadline exceeded", code: CodeDeadlineExceeded, http: http.StatusGatewayTimeout, grpc: codes.DeadlineExceeded},
		{name: "not found", code: CodeNotFound, http: http.StatusNotFound, grpc: codes.NotFound},
		{name: "already exists", code: CodeAlreadyExists, http: http.StatusConflict, grpc: codes.AlreadyExists},
		{name: "permission denied", code: CodePermissionDenied, http: http.StatusForbidden, grpc: codes.PermissionDenied},
		{name: "failed precondition", code: CodeFailedPrecondition, http: http.StatusBadRequest, grpc: codes.FailedPrecondition},
		{name: "aborted", code: CodeAborted, http: http.StatusConflict, grpc: codes.Aborted},
		{name: "out of range", code: CodeOutOfRange, http: http.StatusBadRequest, grpc: codes.OutOfRange},
		{name: "internal", code: CodeInternal, http: http.StatusInternalServerError, grpc: codes.Internal},
		{name: "unknown", code: CodeUnknown, http: http.StatusInternalServerError, grpc: codes.Unknown},
		{name: "unauthenticated", code: CodeUnauthenticated, http: http.StatusUnauthorized, grpc: codes.Unauthenticated},
		{name: "resource exhausted", code: CodeResourceExhausted, http: http.StatusTooManyRequests, grpc: codes.ResourceExhausted},
		{name: "unimplemented", code: CodeUnimplemented, http: http.StatusNotImplemented, grpc: codes.Unimplemented},
		{name: "unavailable", code: CodeUnavailable, http: http.StatusServiceUnavailable, grpc: codes.Unavailable},
		{name: "data loss", code: CodeDataLoss, http: http.StatusInternalServerError, grpc: codes.DataLoss},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTTPStatus(tt.code); got != tt.http {
				t.Fatalf("http status = %d, want %d", got, tt.http)
			}
			if got := GRPCStatus(tt.code); got != tt.grpc {
				t.Fatalf("grpc status = %s, want %s", got, tt.grpc)
			}
			if got := CodeFromGRPCStatus(tt.grpc); got != tt.code {
				t.Fatalf("code from grpc = %s, want %s", got, tt.code)
			}
		})
	}
}

func TestTransportMappingFallbacksAndAliases(t *testing.T) {
	unknownCode := Code("custom")
	if got := HTTPStatus(unknownCode); got != http.StatusInternalServerError {
		t.Fatalf("unknown HTTPStatus = %d, want %d", got, http.StatusInternalServerError)
	}
	if got := GRPCStatus(unknownCode); got != codes.Internal {
		t.Fatalf("unknown GRPCStatus = %s, want %s", got, codes.Internal)
	}
	if got := CodeFromGRPCStatus(codes.Code(99)); got != CodeInternal {
		t.Fatalf("unknown grpc code = %s, want %s", got, CodeInternal)
	}

	if got := HTTPStatusFromCode(CodeAlreadyExists); got != http.StatusConflict {
		t.Fatalf("HTTPStatusFromCode = %d, want %d", got, http.StatusConflict)
	}
	if got := GRPCStatusFromCode(CodeAlreadyExists); got != codes.AlreadyExists {
		t.Fatalf("GRPCStatusFromCode = %s, want %s", got, codes.AlreadyExists)
	}
}

func TestCodeOfRecognizesContextAndGRPCStatus(t *testing.T) {
	if got := CodeOf(context.Canceled); got != CodeCanceled {
		t.Fatalf("context canceled code = %s, want %s", got, CodeCanceled)
	}
	if got := CodeOf(context.DeadlineExceeded); got != CodeDeadlineExceeded {
		t.Fatalf("deadline code = %s, want %s", got, CodeDeadlineExceeded)
	}
	if got := CodeOf(status.Error(codes.NotFound, "missing")); got != CodeNotFound {
		t.Fatalf("grpc code = %s, want %s", got, CodeNotFound)
	}
	if got := TextOf(status.Error(codes.NotFound, "missing")); got != "missing" {
		t.Fatalf("grpc text = %q, want missing", got)
	}
}

func TestGRPCErrorConvertsCoreError(t *testing.T) {
	err := GRPCError(New(CodePermissionDenied, "forbidden"))
	if got := status.Code(err); got != codes.PermissionDenied {
		t.Fatalf("grpc code = %s, want %s", got, codes.PermissionDenied)
	}
	if got := status.Convert(err).Message(); got != "forbidden" {
		t.Fatalf("grpc message = %q, want forbidden", got)
	}
	if GRPCError(nil) != nil {
		t.Fatal("nil error should stay nil")
	}
}

func TestGRPCErrorPreservesStatusError(t *testing.T) {
	statusErr := status.Error(codes.Aborted, "try again")
	if got := GRPCError(statusErr); got != statusErr {
		t.Fatalf("GRPCError should return existing status error unchanged")
	}
}
