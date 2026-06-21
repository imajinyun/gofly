package main

import (
	"errors"
	"testing"
)

func TestFlakyDownstreamBoundaries_BitsUT(t *testing.T) {
	if err := flakyDownstream(0); err != nil {
		t.Fatalf("flakyDownstream(0) = %v, want nil", err)
	}
	if err := flakyDownstream(1); !errors.Is(err, errDownstream) {
		t.Fatalf("flakyDownstream(1) = %v, want errDownstream", err)
	}
}

func TestMainDemo_BitsUT(t *testing.T) {
	main()
}
