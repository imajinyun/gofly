package command

import (
	"encoding/json"
	"io"
)

// errorCodeClass returns a short machine-readable label for an error,
// suitable for use in JSON error envelopes.
func errorCodeClass(err error) string {
	if err == nil {
		return "OK"
	}
	if ExitCode(err) == exitUsage {
		return "USAGE_ERROR"
	}
	return "INTERNAL_ERROR"
}

// WriteErrorJSON writes a structured JSON error envelope to w. The envelope
// includes a machine-readable code and the error message. It is a no-op
// if err is nil.
func WriteErrorJSON(w io.Writer, err error) {
	if err == nil {
		return
	}
	type errorEnvelope struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.NewEncoder(w).Encode(struct {
		Error errorEnvelope `json:"error"`
	}{
		Error: errorEnvelope{
			Code:    errorCodeClass(err),
			Message: err.Error(),
		},
	})
}
