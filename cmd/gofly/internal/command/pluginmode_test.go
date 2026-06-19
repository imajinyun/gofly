package command

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestIsCompilerPluginMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want bool
	}{
		{name: "empty mode", mode: "", want: false},
		{name: "protoc mode", mode: "protoc", want: true},
		{name: "protobuf mode", mode: "protobuf", want: true},
		{name: "normal mode", mode: "api", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOFLY_PLUGIN_MODE", tt.mode)
			if got := IsCompilerPluginMode(); got != tt.want {
				t.Fatalf("IsCompilerPluginMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecuteCompilerPluginMode(t *testing.T) {
	t.Setenv("GOFLY_MODULE", "github.com/acme/demo")
	t.Setenv("GOFLY_NAME_FROM_FILENAME", "true")
	t.Setenv("GOFLY_NO_CLIENT", "1")
	t.Setenv("GOFLY_MULTIPLE", "true")

	req := &pluginpb.CodeGeneratorRequest{}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal protoc request: %v", err)
	}

	var out bytes.Buffer
	if err := ExecuteCompilerPluginMode(bytes.NewReader(data), &out); err != nil {
		t.Fatalf("ExecuteCompilerPluginMode() returned error: %v", err)
	}
	resp := &pluginpb.CodeGeneratorResponse{}
	if err := proto.Unmarshal(out.Bytes(), resp); err != nil {
		t.Fatalf("decode plugin response: %v", err)
	}
}

func TestExecuteCompilerPluginModeReturnsInputAndDecodeErrors(t *testing.T) {
	if err := ExecuteCompilerPluginMode(errorReader{}, &bytes.Buffer{}); !errors.Is(err, errReadFailed) {
		t.Fatalf("ExecuteCompilerPluginMode(read error) = %v, want errReadFailed", err)
	}
	if err := ExecuteCompilerPluginMode(strings.NewReader("not protobuf"), &bytes.Buffer{}); err == nil {
		t.Fatal("ExecuteCompilerPluginMode(invalid protobuf) succeeded, want error")
	}
}

var errReadFailed = errors.New("read failed")

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errReadFailed }

var _ io.Reader = errorReader{}
