package main

import (
	"context"
	"strings"
	"testing"
)

func TestGreeterAndDescriptor(t *testing.T) {
	resp, err := (greeter{}).SayHello(context.Background(), nil)
	if err != nil || resp.Message != "hello world" {
		t.Fatalf("SayHello nil = %#v/%v, want hello world", resp, err)
	}
	resp, err = (greeter{}).SayHello(context.Background(), &helloReq{Name: "gofly"})
	if err != nil || resp.Message != "hello gofly" {
		t.Fatalf("SayHello named = %#v/%v, want hello gofly", resp, err)
	}
	desc := greeterServiceDesc(greeter{})
	if err := desc.Validate(); err != nil {
		t.Fatalf("descriptor Validate: %v", err)
	}
	if _, err := desc.Methods[0].Handler(context.Background(), "bad"); err == nil || !strings.Contains(err.Error(), "unexpected request type") {
		t.Fatalf("bad handler error = %v, want type error", err)
	}
}
