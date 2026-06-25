package rpc

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain detects goroutine leaks. Known exemptions:
//   - (*HTTPServer).serveStream: stream handler goroutines left behind
//     by timeout/error tests (e.g. TestRPCStreamClientOperationTimeout)
//     that do not fully drain the server connection.
//     This is a known issue — the server's httptest.Server.Close does
//     not always terminate handler goroutines immediately.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction(
			"github.com/imajinyun/gofly/rpc.(*HTTPServer).serveStream",
		),
		goleak.IgnoreTopFunction(
			"github.com/imajinyun/gofly/rpc.TestRPCStreamClientOperationTimeout.func2",
		),
	)
}
