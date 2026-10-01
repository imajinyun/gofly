package app

import "testing"

func TestPing(t *testing.T) {
	resp := Ping()
	if resp.Message != "pong" {
		t.Fatalf("Ping().Message = %q, want pong", resp.Message)
	}
}
