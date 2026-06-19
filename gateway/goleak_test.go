package gateway

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain detects goroutine leaks in gateway tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
