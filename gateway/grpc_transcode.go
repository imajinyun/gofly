package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	coremetadata "github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// NewGRPCTranscoderFactory enables unary protobuf JSON calls for protocol grpc; callers configure TLS through client options.
func NewGRPCTranscoderFactory(descriptors *descriptorpb.FileDescriptorSet, opts ...flygrpc.ClientOption) (TranscoderFactory, error) {
	if descriptors == nil {
		return nil, errors.New("grpc descriptor set is required")
	}
	files, err := protodesc.NewFiles(descriptors)
	if err != nil {
		return nil, err
	}
	methods := make(map[string]protoreflect.MethodDescriptor)
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			for j := 0; j < service.Methods().Len(); j++ {
				method := service.Methods().Get(j)
				methods[string(service.FullName())+"/"+string(method.Name())] = method
			}
		}
		return true
	})
	if len(methods) == 0 {
		return nil, errors.New("grpc descriptor set has no methods")
	}
	types := dynamicpb.NewTypes(files)
	options := append([]flygrpc.ClientOption(nil), opts...)
	return func(endpoint string, route Route) (rpc.GenericClient, error) {
		if route.Transcode.Protocol != "grpc" {
			return defaultTranscoderFactory(endpoint, route)
		}
		if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
			return nil, errors.New("grpc targets must not use HTTP URL schemes; configure TLS using client options")
		}
		conn, err := flygrpc.NewDefaultClient(context.Background(), endpoint, route.Service, nil, nil, options...)
		if err != nil {
			return nil, err
		}
		return &grpcTranscoder{conn: conn, methods: methods, types: types}, nil
	}, nil
}

type grpcTranscoder struct {
	conn    *flygrpc.ClientConn
	methods map[string]protoreflect.MethodDescriptor
	types   *dynamicpb.Types
}

func (c *grpcTranscoder) CallRaw(ctx context.Context, method string, request any) (json.RawMessage, coremetadata.MD, error) {
	method = strings.TrimPrefix(method, "/")
	descriptor, ok := c.methods[method]
	if !ok {
		return nil, nil, status.Error(codes.Unimplemented, "grpc method is not in descriptor set")
	}
	if descriptor.IsStreamingClient() || descriptor.IsStreamingServer() {
		return nil, nil, status.Error(codes.Unimplemented, "HTTP JSON transcoding supports unary RPCs only")
	}
	payload, err := rpc.EncodeJSONPayload(request)
	if err != nil {
		return nil, nil, status.Error(codes.InvalidArgument, "invalid protobuf JSON request")
	}
	if len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
		payload = []byte("{}")
	}
	input, output := dynamicpb.NewMessage(descriptor.Input()), dynamicpb.NewMessage(descriptor.Output())
	if err := (protojson.UnmarshalOptions{Resolver: c.types}).Unmarshal(payload, input); err != nil {
		return nil, nil, status.Error(codes.InvalidArgument, "invalid protobuf JSON request")
	}
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	if md, ok := coremetadata.FromContext(ctx); ok {
		for key, value := range md {
			key = strings.ToLower(key)
			if validGRPCMetadataKey(key) {
				outgoing.Set(key, value)
			}
		}
	}
	ctx = metadata.NewOutgoingContext(ctx, outgoing)
	var headers, trailers metadata.MD
	if err := c.conn.Invoke(ctx, "/"+method, input, output, stdgrpc.Header(&headers), stdgrpc.Trailer(&trailers)); err != nil {
		return nil, nil, err
	}
	data, err := (protojson.MarshalOptions{Resolver: c.types}).Marshal(output)
	if err != nil {
		return nil, nil, status.Error(codes.Internal, "encode protobuf JSON response")
	}
	md := coremetadata.MD{}
	for key, values := range metadata.Join(headers, trailers) {
		if validGRPCMetadataKey(key) && len(values) > 0 {
			md[key] = values[0]
		}
	}
	return data, md, nil
}

func (c *grpcTranscoder) Close() error { return c.conn.Close() }

func validGRPCMetadataKey(key string) bool {
	if key == "" || key == "content-type" || strings.HasPrefix(key, "grpc-") || strings.HasSuffix(key, "-bin") {
		return false
	}
	for _, char := range key {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}
