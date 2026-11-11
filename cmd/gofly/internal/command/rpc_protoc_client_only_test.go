package command

import (
	"strings"
	"testing"
)

func TestExecuteRPCProtocClientOnlyIsExplicitIntentionalGap(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unsupported",
			args: []string{"greeter.proto", "--client-only"},
			want: "--client-only is not supported",
		},
		{
			name: "conflicts with client=false",
			args: []string{"greeter.proto", "--client-only", "--client=false"},
			want: "--client-only cannot be combined with --client=false",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := rpcProtocCommand(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("rpcProtocCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}
