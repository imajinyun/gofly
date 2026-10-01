package greeterrpc

import (
	"context"
	"testing"

	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
)

func TestGreeterService(t *testing.T) {
	desc := GreeterService(svc.NewServiceContext(config.Config{}))
	resp, err := desc.Methods[0].Handler(context.Background(), &SayHelloRequest{Name: "gofly"})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(SayHelloResponse).Message
	if got != "hello gofly" {
		t.Fatalf("message = %q, want hello gofly", got)
	}
}
