package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

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

// NewGRPCTranscoderFactory enables unary protobuf JSON, client-streaming NDJSON,
// server-streaming SSE, and bidirectional-streaming WebSocket calls for protocol
// grpc; callers configure TLS through client options.
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

type establishedServerStreamError struct {
	err error
}

func (e *establishedServerStreamError) Error() string { return e.err.Error() }
func (e *establishedServerStreamError) Unwrap() error { return e.err }

func (c *grpcTranscoder) CallRaw(ctx context.Context, method string, request any) (json.RawMessage, coremetadata.MD, error) {
	descriptor, err := c.methodDescriptor(method)
	if err != nil {
		return nil, nil, err
	}
	if descriptor.IsStreamingClient() || descriptor.IsStreamingServer() {
		return nil, nil, status.Error(codes.Unimplemented, "HTTP JSON unary transcoding does not accept streaming RPCs")
	}
	input, err := c.inputMessage(descriptor, request)
	if err != nil {
		return nil, nil, err
	}
	output := dynamicpb.NewMessage(descriptor.Output())
	ctx = grpcTranscodeOutgoingContext(ctx)
	var headers, trailers metadata.MD
	if err := c.conn.Invoke(ctx, "/"+strings.TrimPrefix(method, "/"), input, output, stdgrpc.Header(&headers), stdgrpc.Trailer(&trailers)); err != nil {
		return nil, nil, err
	}
	data, err := (protojson.MarshalOptions{Resolver: c.types}).Marshal(output)
	if err != nil {
		return nil, nil, status.Error(codes.Internal, "encode protobuf JSON response")
	}
	return data, grpcTranscodeMetadata(metadata.Join(headers, trailers)), nil
}

func (c *grpcTranscoder) OpenServerStreamRaw(ctx context.Context, method string, request any) (rawServerStream, coremetadata.MD, bool, error) {
	descriptor, err := c.methodDescriptor(method)
	if err != nil {
		return nil, nil, false, err
	}
	if descriptor.IsStreamingClient() {
		return nil, nil, true, status.Error(codes.Unimplemented, "HTTP SSE transcoding does not support bidirectional streaming RPCs")
	}
	if !descriptor.IsStreamingServer() {
		return nil, nil, false, nil
	}
	input, err := c.inputMessage(descriptor, request)
	if err != nil {
		return nil, nil, true, err
	}
	streamCtx, cancel := context.WithCancel(grpcTranscodeOutgoingContext(ctx))
	stream, err := c.conn.NewStream(streamCtx, &stdgrpc.StreamDesc{ServerStreams: true}, "/"+strings.TrimPrefix(method, "/"))
	if err != nil {
		cancel()
		return nil, nil, true, err
	}
	if err := stream.SendMsg(input); err != nil {
		cancel()
		return nil, nil, true, &establishedServerStreamError{err: err}
	}
	if err := stream.CloseSend(); err != nil {
		cancel()
		return nil, nil, true, &establishedServerStreamError{err: err}
	}
	headers, err := stream.Header()
	if err != nil {
		cancel()
		return nil, nil, true, &establishedServerStreamError{err: err}
	}
	return &grpcRawServerStream{stream: stream, descriptor: descriptor.Output(), types: c.types, cancel: cancel}, grpcTranscodeMetadata(headers), true, nil
}

func (c *grpcTranscoder) CallClientStreamRaw(ctx context.Context, method string, messages <-chan clientStreamFrame) (json.RawMessage, coremetadata.MD, bool, error) {
	descriptor, err := c.methodDescriptor(method)
	if err != nil {
		return nil, nil, false, err
	}
	if descriptor.IsStreamingServer() {
		return nil, nil, true, status.Error(codes.Unimplemented, "HTTP NDJSON transcoding does not support bidirectional streaming RPCs")
	}
	if !descriptor.IsStreamingClient() {
		return nil, nil, false, nil
	}
	streamCtx, cancel := context.WithCancel(grpcTranscodeOutgoingContext(ctx))
	defer cancel()
	stream, err := c.conn.NewStream(streamCtx, &stdgrpc.StreamDesc{ClientStreams: true}, "/"+strings.TrimPrefix(method, "/"))
	if err != nil {
		return nil, nil, true, err
	}
	type receiveResult struct {
		output *dynamicpb.Message
		err    error
	}
	received := make(chan receiveResult, 1)
	go func() {
		output := dynamicpb.NewMessage(descriptor.Output())
		received <- receiveResult{output: output, err: stream.RecvMsg(output)}
	}()
	var response receiveResult
	for {
		select {
		case response = <-received:
			if response.err != nil {
				return nil, nil, true, response.err
			}
			goto encode
		case frame, ok := <-messages:
			if !ok {
				if err := ctx.Err(); err != nil {
					return nil, nil, true, status.FromContextError(err).Err()
				}
				if err := stream.CloseSend(); err != nil {
					return nil, nil, true, err
				}
				response = <-received
				if response.err != nil {
					return nil, nil, true, response.err
				}
				goto encode
			}
			if frame.err != nil {
				return nil, nil, true, frame.err
			}
			input, inputErr := c.inputMessage(descriptor, frame.payload)
			if inputErr != nil {
				return nil, nil, true, &clientStreamInputError{err: inputErr}
			}
			if sendErr := stream.SendMsg(input); sendErr != nil {
				return nil, nil, true, sendErr
			}
		}
	}

encode:
	headers, err := stream.Header()
	if err != nil {
		return nil, nil, true, err
	}
	data, err := (protojson.MarshalOptions{Resolver: c.types}).Marshal(response.output)
	if err != nil {
		return nil, nil, true, status.Error(codes.Internal, "encode protobuf JSON client-stream response")
	}
	return data, grpcTranscodeMetadata(metadata.Join(headers, stream.Trailer())), true, nil
}

func (c *grpcTranscoder) OpenBidirectionalStreamRaw(ctx context.Context, method string) (rawBidirectionalStream, bool, error) {
	descriptor, err := c.methodDescriptor(method)
	if err != nil {
		return nil, false, err
	}
	if !descriptor.IsStreamingClient() || !descriptor.IsStreamingServer() {
		return nil, false, nil
	}
	streamCtx, cancel := context.WithCancel(grpcTranscodeOutgoingContext(ctx))
	stream, err := c.conn.NewStream(streamCtx, &stdgrpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/"+strings.TrimPrefix(method, "/"))
	if err != nil {
		cancel()
		return nil, true, err
	}
	return &grpcRawBidirectionalStream{
		stream:           stream,
		inputDescriptor:  descriptor.Input(),
		outputDescriptor: descriptor.Output(),
		types:            c.types,
		cancel:           cancel,
	}, true, nil
}

func (c *grpcTranscoder) methodDescriptor(method string) (protoreflect.MethodDescriptor, error) {
	method = strings.TrimPrefix(method, "/")
	descriptor, ok := c.methods[method]
	if !ok {
		return nil, status.Error(codes.Unimplemented, "grpc method is not in descriptor set")
	}
	return descriptor, nil
}

func (c *grpcTranscoder) inputMessage(descriptor protoreflect.MethodDescriptor, request any) (*dynamicpb.Message, error) {
	payload, err := rpc.EncodeJSONPayload(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid protobuf JSON request")
	}
	if len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
		payload = []byte("{}")
	}
	input := dynamicpb.NewMessage(descriptor.Input())
	if err := (protojson.UnmarshalOptions{Resolver: c.types}).Unmarshal(payload, input); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid protobuf JSON request")
	}
	return input, nil
}

type grpcRawServerStream struct {
	stream     stdgrpc.ClientStream
	descriptor protoreflect.MessageDescriptor
	types      *dynamicpb.Types
	cancel     context.CancelFunc
	closeOnce  sync.Once
}

type grpcRawBidirectionalStream struct {
	stream           stdgrpc.ClientStream
	inputDescriptor  protoreflect.MessageDescriptor
	outputDescriptor protoreflect.MessageDescriptor
	types            *dynamicpb.Types
	cancel           context.CancelFunc
	closeOnce        sync.Once
}

func (s *grpcRawBidirectionalStream) SendRaw(payload json.RawMessage) error {
	input := dynamicpb.NewMessage(s.inputDescriptor)
	if err := (protojson.UnmarshalOptions{Resolver: s.types}).Unmarshal(payload, input); err != nil {
		return &clientStreamInputError{err: status.Error(codes.InvalidArgument, "invalid protobuf JSON request")}
	}
	return s.stream.SendMsg(input)
}

func (s *grpcRawBidirectionalStream) RecvRaw() (json.RawMessage, error) {
	output := dynamicpb.NewMessage(s.outputDescriptor)
	if err := s.stream.RecvMsg(output); err != nil {
		return nil, err
	}
	data, err := (protojson.MarshalOptions{Resolver: s.types}).Marshal(output)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode protobuf JSON bidirectional-stream response")
	}
	return data, nil
}

func (s *grpcRawBidirectionalStream) Header() (coremetadata.MD, error) {
	headers, err := s.stream.Header()
	return grpcTranscodeMetadata(headers), err
}

func (s *grpcRawBidirectionalStream) Trailer() coremetadata.MD {
	return grpcTranscodeMetadata(s.stream.Trailer())
}

func (s *grpcRawBidirectionalStream) CloseSend() error {
	return s.stream.CloseSend()
}

func (s *grpcRawBidirectionalStream) Close() error {
	s.closeOnce.Do(s.cancel)
	return nil
}

func (s *grpcRawServerStream) RecvRaw() (json.RawMessage, error) {
	output := dynamicpb.NewMessage(s.descriptor)
	if err := s.stream.RecvMsg(output); err != nil {
		if errors.Is(err, io.EOF) {
			s.closeOnce.Do(s.cancel)
		}
		return nil, err
	}
	data, err := (protojson.MarshalOptions{Resolver: s.types}).Marshal(output)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode protobuf JSON stream response")
	}
	return data, nil
}

func (s *grpcRawServerStream) Trailer() coremetadata.MD {
	return grpcTranscodeMetadata(s.stream.Trailer())
}

func (s *grpcRawServerStream) Close() error {
	s.closeOnce.Do(s.cancel)
	return nil
}

func grpcTranscodeOutgoingContext(ctx context.Context) context.Context {
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
	return metadata.NewOutgoingContext(ctx, outgoing)
}

func grpcTranscodeMetadata(md metadata.MD) coremetadata.MD {
	out := coremetadata.MD{}
	for key, values := range md {
		if validGRPCMetadataKey(key) && len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
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
