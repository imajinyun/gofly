package generator

import (
	"strings"
	"testing"
)

func TestGenerateGRPCBindingCodeSupportsStreaming(t *testing.T) {
	doc, err := ParseProto(`syntax = "proto3";
package chat.v1;
message ChatRequest {
  string text = 1;
}
message ChatResponse {
  string text = 1;
}
service Chat {
  rpc Talk(stream ChatRequest) returns (stream ChatResponse);
}`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := GenerateGRPCBindingCode(doc, "chatv1")
	if err != nil {
		t.Fatal(err)
	}
	text := string(code)
	for _, want := range []string{"func NewChatGRPCServer", "RegisterChatServer", "func DialChat"} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated grpc binding missing %q:\n%s", want, text)
		}
	}
}
