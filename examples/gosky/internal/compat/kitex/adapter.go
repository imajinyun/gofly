// Package kitex contains small migration adapters for projects that previously
// used unary endpoint signatures from other RPC ecosystems. Kitex is a
// third-party project;
// this package is not endorsed by or affiliated with its maintainers and does
// not include or depend on Kitex source code.
package kitex

import (
	"context"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/rpc"
)

// Endpoint is the minimal unary endpoint shape used by generated migration
// handlers. It keeps migration code independent from third-party RPC runtimes.
type Endpoint func(context.Context, any) (any, error)

// Method binds a migration endpoint to a gofly RPC method descriptor.
func Method(name string, newRequest func() any, endpoint Endpoint, opts ...MethodOption) rpc.MethodDesc {
	desc := rpc.MethodDesc{
		Name:       strings.TrimSpace(name),
		NewRequest: newRequest,
		Handler: func(ctx context.Context, req any) (any, error) {
			if endpoint == nil {
				return nil, fmt.Errorf("kitex endpoint %s is nil", name)
			}
			return endpoint(ctx, req)
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&desc)
		}
	}
	return desc
}

type MethodOption func(*rpc.MethodDesc)

// WithMetadata attaches service metadata to the generated gofly method.
func WithMetadata(metadata map[string]string) MethodOption {
	return func(desc *rpc.MethodDesc) {
		if len(metadata) == 0 {
			return
		}
		desc.Metadata = make(map[string]string, len(metadata))
		for key, value := range metadata {
			desc.Metadata[key] = value
		}
	}
}

// Service assembles a gofly service from migration method descriptors.
func Service(name string, methods ...rpc.MethodDesc) rpc.ServiceDesc {
	return rpc.ServiceDesc{Name: strings.TrimSpace(name), Methods: append([]rpc.MethodDesc(nil), methods...)}
}
