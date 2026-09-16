// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/imajinyun/gofly/core/breaker"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

const (
	grpcClientStreamMediaType     = "application/x-ndjson"
	grpcClientStreamMaxBodyBytes  = 16 * 1024 * 1024
	grpcClientStreamMaxMessages   = 10_000
	grpcClientStreamMaxFrameBytes = 1024 * 1024
	grpcBidiWebSocketSubprotocol  = "gofly.grpc.bidi.v1"
)

// TranscoderFactory builds a generic RPC client for a resolved upstream
// endpoint. It allows callers to customize how transcoded requests reach the
// backend (codec, TLS, protocol). When unset the gateway uses a default
// JSON-over-HTTP generic client.
type TranscoderFactory func(endpoint string, route Route) (rpc.GenericClient, error)

// rawServerStream is the gateway-internal response side of a server-streaming
// RPC. Implementations must make Close cancel any blocked receive operation.
type rawServerStream interface {
	RecvRaw() (json.RawMessage, error)
	Trailer() metadata.MD
	Close() error
}

// serverStreamingTranscoder is an optional capability implemented by native
// transports. The bool reports whether the descriptor identifies a streaming
// method, keeping existing GenericClient implementations source-compatible.
type serverStreamingTranscoder interface {
	OpenServerStreamRaw(ctx context.Context, method string, request any) (rawServerStream, metadata.MD, bool, error)
}

type clientStreamingTranscoder interface {
	CallClientStreamRaw(ctx context.Context, method string, messages <-chan clientStreamFrame) (json.RawMessage, metadata.MD, bool, error)
}

type rawBidirectionalStream interface {
	SendRaw(json.RawMessage) error
	RecvRaw() (json.RawMessage, error)
	Header() (metadata.MD, error)
	Trailer() metadata.MD
	CloseSend() error
	Close() error
}

type bidirectionalStreamingTranscoder interface {
	OpenBidirectionalStreamRaw(ctx context.Context, method string) (rawBidirectionalStream, bool, error)
}

type bidiWebSocketEnvelope struct {
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data,omitempty"`
	Metadata metadata.MD     `json:"metadata,omitempty"`
}

type clientStreamFrame struct {
	payload json.RawMessage
	err     error
}

type clientStreamInputError struct {
	err error
}

func (e *clientStreamInputError) Error() string { return e.err.Error() }
func (e *clientStreamInputError) Unwrap() error { return e.err }

func (g *Gateway) isBidirectionalStreamingTranscode(r *http.Request, route Route) bool {
	if g == nil || r == nil || !route.Transcode.Enabled || !strings.EqualFold(route.Transcode.Protocol, "grpc") {
		return false
	}
	target, err := g.transcodeTarget(r, route)
	if err != nil || strings.TrimSpace(route.Transcode.Descriptor) == "" {
		return false
	}
	desc, ok := g.descriptor(route.Transcode.Descriptor)
	if !ok {
		return false
	}
	for _, stream := range desc.Streams {
		if strings.Trim(strings.TrimSpace(stream.Name), "/") == target.method {
			return stream.Mode == rpc.StreamModeBidiStream
		}
	}
	return false
}

func (g *Gateway) proxyBidirectionalStream(w http.ResponseWriter, r *http.Request, route Route) (proxyResult, error) {
	if !webSocketSubprotocolOffered(r.Header.Values("Sec-WebSocket-Protocol"), grpcBidiWebSocketSubprotocol) {
		return proxyResult{Status: http.StatusBadRequest}, nil
	}
	brk := g.breakerFor(route)
	if brk != nil {
		if err := brk.Allow(); err != nil {
			return proxyResult{Err: err}, err
		}
	}
	endpoint, err := g.pickEndpoint(r.Context(), route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Err: err}, err
	}
	target, err := g.transcodeTarget(r, route)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	client, err := g.transcoderFor(endpoint, route)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	streaming, ok := client.(bidirectionalStreamingTranscoder)
	if !ok {
		return g.transcodeCallError(route, endpoint, target.profile, brk, status.Error(codes.Unimplemented, "transcoder does not support bidirectional-streaming RPCs"))
	}
	methodPath, err := rpc.MethodPath(target.service, target.method)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	stream, handled, err := streaming.OpenBidirectionalStreamRaw(transcodeContext(r.Context(), r, route), methodPath)
	if err != nil {
		return g.transcodeCallError(route, endpoint, target.profile, brk, err)
	}
	if !handled {
		return g.transcodeCallError(route, endpoint, target.profile, brk, status.Error(codes.Unimplemented, "transcoder did not handle bidirectional-streaming RPC"))
	}
	settlement := &transcodeStreamSettlement{fn: func(success bool) {
		g.reportEndpoint(route, endpoint, success)
		if brk != nil {
			if success {
				brk.MarkSuccess()
			} else {
				brk.MarkFailure()
			}
		}
	}}
	done := make(chan struct{})
	ctx := &rest.Context{Response: w, Request: r}
	err = ctx.WebSocket(func(streamCtx context.Context, conn *rest.WebSocketConn) {
		defer close(done)
		g.bridgeBidirectionalStream(streamCtx, r, conn, stream, route, target.profile, settlement)
	}, rest.WithWebSocketSubprotocol(grpcBidiWebSocketSubprotocol), rest.WithWebSocketMaxMessageBytes(grpcClientStreamMaxFrameBytes))
	if err != nil {
		_ = stream.Close()
		settlement.done(true)
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	<-done
	return proxyResult{Endpoint: endpoint, Status: http.StatusSwitchingProtocols, Hijacked: true}, nil
}

func webSocketSubprotocolOffered(values []string, protocol string) bool {
	for _, value := range values {
		for _, offered := range strings.Split(value, ",") {
			if strings.TrimSpace(offered) == protocol {
				return true
			}
		}
	}
	return false
}

func (g *Gateway) bridgeBidirectionalStream(ctx context.Context, request *http.Request, conn *rest.WebSocketConn, stream rawBidirectionalStream, route Route, profile *TranscodeProfile, settlement *transcodeStreamSettlement) {
	receiveDone := make(chan struct{})
	bridgeDone := make(chan struct{})
	go func() {
		defer close(receiveDone)
		count := 0
		totalBytes := 0
		headers, err := stream.Header()
		if err != nil {
			if channelClosed(bridgeDone) {
				return
			}
			settlement.done(false)
			_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(err)})
			return
		}
		if len(headers) > 0 {
			if err := writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "headers", Metadata: headers}); err != nil {
				settlement.done(true)
				return
			}
		}
		for {
			raw, err := stream.RecvRaw()
			if errors.Is(err, io.EOF) {
				trailers := stream.Trailer()
				if len(trailers) > 0 {
					_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "trailers", Metadata: trailers})
				}
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "complete"})
				settlement.done(true)
				return
			}
			if err != nil {
				if channelClosed(bridgeDone) {
					return
				}
				settlement.done(errors.Is(ctx.Err(), context.Canceled))
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(err)})
				return
			}
			payload, mapErr := transcodeResponsePayload(raw, profile)
			if mapErr != nil {
				g.recordTranscodeMappingError(route, "response", mapErr)
				settlement.done(true)
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, mapErr.Error()))})
				return
			}
			count++
			totalBytes += len(payload)
			if bidirectionalStreamLimitExceeded(count, totalBytes, len(payload)) {
				settlement.done(false)
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeResourceExhausted, "bidirectional stream response exceeds limits"))})
				return
			}
			if err := writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "message", Data: payload}); err != nil {
				settlement.done(true)
				return
			}
		}
	}()
	defer func() {
		close(bridgeDone)
		_ = stream.Close()
		<-receiveDone
	}()

	count := 0
	totalBytes := 0
	halfClosed := false
	for {
		type readResult struct {
			messageType int
			payload     []byte
			err         error
		}
		read := make(chan readResult, 1)
		go func() {
			messageType, payload, err := conn.ReadMessage()
			read <- readResult{messageType: messageType, payload: payload, err: err}
		}()
		var inbound readResult
		select {
		case <-receiveDone:
			_ = conn.Close()
			return
		case <-ctx.Done():
			settlement.done(true)
			_ = conn.Close()
			return
		case inbound = <-read:
		}
		if inbound.err != nil {
			settlement.done(true)
			return
		}
		if inbound.messageType == rest.WebSocketPingMessage {
			_ = conn.WriteMessage(rest.WebSocketPongMessage, inbound.payload)
			continue
		}
		if inbound.messageType == rest.WebSocketPongMessage {
			continue
		}
		if inbound.messageType != rest.WebSocketTextMessage {
			settlement.done(true)
			_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, "bidirectional stream requires text JSON envelopes"))})
			return
		}
		count++
		totalBytes += len(inbound.payload)
		if bidirectionalStreamLimitExceeded(count, totalBytes, len(inbound.payload)) {
			settlement.done(true)
			_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeResourceExhausted, "bidirectional stream exceeds request limits"))})
			return
		}
		var envelope bidiWebSocketEnvelope
		if err := json.Unmarshal(inbound.payload, &envelope); err != nil {
			settlement.done(true)
			_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, "invalid bidirectional stream envelope"))})
			return
		}
		switch envelope.Type {
		case "message":
			if halfClosed {
				settlement.done(true)
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, "bidirectional stream send side is closed"))})
				return
			}
			mapped, err := transcodeRequestPayload(request, route, envelope.Data, profile)
			if err != nil {
				settlement.done(true)
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, err.Error()))})
				return
			}
			if err := stream.SendRaw(mapped); err != nil {
				var inputErr *clientStreamInputError
				settlement.done(errors.As(err, &inputErr))
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(err)})
				return
			}
		case "half_close":
			if halfClosed {
				settlement.done(true)
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, "bidirectional stream send side is already closed"))})
				return
			}
			if err := stream.CloseSend(); err != nil {
				settlement.done(false)
				_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(err)})
				return
			}
			halfClosed = true
		default:
			settlement.done(true)
			_ = writeBidiEnvelope(conn, bidiWebSocketEnvelope{Type: "error", Data: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, "unsupported bidirectional stream envelope type"))})
			return
		}
	}
}

func channelClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func writeBidiEnvelope(conn *rest.WebSocketConn, envelope bidiWebSocketEnvelope) error {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return conn.WriteMessage(rest.WebSocketTextMessage, payload)
}

func bidirectionalStreamLimitExceeded(count, totalBytes, frameBytes int) bool {
	return count > grpcClientStreamMaxMessages ||
		frameBytes > grpcClientStreamMaxFrameBytes ||
		totalBytes > grpcClientStreamMaxBodyBytes
}

func (g *Gateway) isClientStreamingTranscode(r *http.Request, route Route) bool {
	if g == nil || r == nil || !route.Transcode.Enabled || !strings.EqualFold(route.Transcode.Protocol, "grpc") {
		return false
	}
	target, err := g.transcodeTarget(r, route)
	if err != nil || strings.TrimSpace(route.Transcode.Descriptor) == "" {
		return false
	}
	desc, ok := g.descriptor(route.Transcode.Descriptor)
	if !ok {
		return false
	}
	for _, stream := range desc.Streams {
		if strings.Trim(strings.TrimSpace(stream.Name), "/") == target.method {
			return stream.Mode == rpc.StreamModeClientStream
		}
	}
	return false
}

func (g *Gateway) proxyClientStream(r *http.Request, route Route) (proxyResult, error) {
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || !strings.EqualFold(mediaType, grpcClientStreamMediaType) {
		return proxyResult{
			Status: http.StatusUnsupportedMediaType,
			Header: transcodeResponseHeader(nil),
			Body:   transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, "client-streaming grpc transcoding requires application/x-ndjson")),
		}, nil
	}
	brk := g.breakerFor(route)
	if brk != nil {
		if err := brk.Allow(); err != nil {
			return proxyResult{Err: err}, err
		}
	}
	endpoint, err := g.pickEndpoint(r.Context(), route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Err: err}, err
	}
	return g.transcodeClientStreamOnce(r, route, endpoint, brk)
}

func (g *Gateway) transcodeClientStreamOnce(r *http.Request, route Route, endpoint string, brk *breaker.AdaptiveBreaker) (proxyResult, error) {
	target, err := g.transcodeTarget(r, route)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	client, err := g.transcoderFor(endpoint, route)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	streaming, ok := client.(clientStreamingTranscoder)
	if !ok {
		return g.transcodeCallError(route, endpoint, target.profile, brk, status.Error(codes.Unimplemented, "transcoder does not support client-streaming RPCs"))
	}
	methodPath, err := rpc.MethodPath(target.service, target.method)
	if err != nil {
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	decodeCtx, cancelDecode := context.WithCancel(r.Context())
	defer func() {
		cancelDecode()
		_ = r.Body.Close()
	}()
	messages := make(chan clientStreamFrame)
	go decodeNDJSONStream(decodeCtx, r.Body, func(frame json.RawMessage) (json.RawMessage, error) {
		return transcodeRequestPayload(r, route, frame, target.profile)
	}, messages)
	raw, md, handled, callErr := streaming.CallClientStreamRaw(transcodeContext(r.Context(), r, route), methodPath, messages)
	if !handled {
		callErr = status.Error(codes.Unimplemented, "transcoder did not handle client-streaming RPC")
	}
	if callErr != nil {
		cancelDecode()
		_ = r.Body.Close() // close unblocks an in-flight HTTP body read before waiting for decoder exit
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		g.reportEndpoint(route, endpoint, true)
		if brk != nil {
			brk.MarkSuccess()
		}
		return proxyResult{Endpoint: endpoint, Err: r.Context().Err()}, r.Context().Err()
	}
	var inputErr *clientStreamInputError
	if errors.As(callErr, &inputErr) {
		httpStatus := coreerrors.HTTPStatus(rpc.CodeOf(inputErr.err))
		if rpc.CodeOf(inputErr.err) == rpc.CodeResourceExhausted {
			httpStatus = http.StatusRequestEntityTooLarge
		}
		return proxyResult{
			Endpoint: endpoint,
			Status:   httpStatus,
			Header:   transcodeResponseHeader(nil),
			Body:     transcodeErrorBody(inputErr.err),
		}, nil
	}
	if callErr != nil {
		result, _ := g.transcodeCallError(route, endpoint, target.profile, brk, callErr)
		result.Err = nil // client streams are never replayed after request consumption starts
		return result, nil
	}
	responseBody, mapErr := transcodeResponsePayload(raw, target.profile)
	if mapErr != nil {
		g.recordTranscodeMappingError(route, "response", mapErr)
		return proxyResult{Endpoint: endpoint, Status: http.StatusBadRequest, Header: transcodeResponseHeader(nil), Body: transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, mapErr.Error()))}, nil
	}
	g.reportEndpoint(route, endpoint, true)
	if brk != nil {
		brk.MarkSuccess()
	}
	return proxyResult{Endpoint: endpoint, Status: http.StatusOK, Header: transcodeResponseHeader(md), Body: responseBody}, nil
}

func decodeNDJSONStream(ctx context.Context, body io.Reader, mapFrame func(json.RawMessage) (json.RawMessage, error), messages chan<- clientStreamFrame) {
	defer close(messages)
	sendError := func(err error) {
		select {
		case messages <- clientStreamFrame{err: &clientStreamInputError{err: err}}:
		case <-ctx.Done():
		}
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), grpcClientStreamMaxFrameBytes)
	count := 0
	totalBytes := 0
	for scanner.Scan() {
		frame := bytes.TrimSpace(scanner.Bytes())
		if len(frame) == 0 {
			continue
		}
		count++
		if count > grpcClientStreamMaxMessages {
			sendError(status.Error(codes.ResourceExhausted, "client stream exceeds message limit"))
			return
		}
		totalBytes += len(frame)
		if totalBytes > grpcClientStreamMaxBodyBytes {
			sendError(status.Error(codes.ResourceExhausted, "client stream exceeds body size limit"))
			return
		}
		message := append(json.RawMessage(nil), frame...)
		if !json.Valid(message) {
			sendError(status.Error(codes.InvalidArgument, "client stream frame must be valid JSON"))
			return
		}
		if mapFrame != nil {
			var err error
			message, err = mapFrame(message)
			if err != nil {
				sendError(status.Error(codes.InvalidArgument, err.Error()))
				return
			}
		}
		select {
		case messages <- clientStreamFrame{payload: message}:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return
		}
		sendError(status.Error(codes.ResourceExhausted, "client stream frame exceeds size limit"))
		return
	}
	if count == 0 {
		sendError(status.Error(codes.InvalidArgument, "client stream requires at least one NDJSON message"))
		return
	}
}

// transcodeOnce converts an inbound HTTP/JSON request into a generic RPC call
// against the resolved upstream endpoint and maps the RPC response back to an
// HTTP proxyResult.
func (g *Gateway) transcodeOnce(r *http.Request, route Route, endpoint string, body []byte, brk *breaker.AdaptiveBreaker) (proxyResult, error) {
	target, err := g.transcodeTarget(r, route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	payload, err := transcodeRequestPayload(r, route, body, target.profile)
	if err != nil {
		g.recordTranscodeMappingError(route, "request", err)
		callErr := rpc.NewError(rpc.CodeInvalidArgument, err.Error())
		return proxyResult{
			Endpoint: endpoint,
			Status:   http.StatusBadRequest,
			Header:   transcodeResponseHeader(nil),
			Body:     transcodeErrorBody(callErr),
		}, nil
	}
	client, err := g.transcoderFor(endpoint, route)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	ctx := transcodeContext(r.Context(), r, route)
	methodPath, err := rpc.MethodPath(target.service, target.method)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	if streaming, ok := client.(serverStreamingTranscoder); ok {
		stream, md, handled, streamErr := streaming.OpenServerStreamRaw(ctx, methodPath, payload)
		if handled {
			if streamErr != nil {
				return g.transcodeCallError(route, endpoint, target.profile, brk, streamErr)
			}
			first, recvErr := stream.RecvRaw()
			if recvErr != nil && !errors.Is(recvErr, io.EOF) {
				_ = stream.Close()
				return g.transcodeCallError(route, endpoint, target.profile, brk, recvErr)
			}
			return proxyResult{
				Endpoint: endpoint,
				Status:   http.StatusOK,
				Header:   transcodeStreamResponseHeader(md),
				BodyStream: newTranscodeSSEBody(ctx, stream, target.profile, first, recvErr, func(success bool) {
					g.reportEndpoint(route, endpoint, success)
					if brk == nil {
						return
					}
					if success {
						brk.MarkSuccess()
						return
					}
					brk.MarkFailure()
				}),
			}, nil
		}
	}
	raw, md, callErr := client.CallRaw(ctx, methodPath, payload)
	if callErr != nil {
		return g.transcodeCallError(route, endpoint, target.profile, brk, callErr)
	}
	g.reportEndpoint(route, endpoint, true)
	if brk != nil {
		brk.MarkSuccess()
	}
	responseBody, err := transcodeResponsePayload(raw, target.profile)
	if err != nil {
		g.recordTranscodeMappingError(route, "response", err)
		callErr := rpc.NewError(rpc.CodeInvalidArgument, err.Error())
		return proxyResult{
			Endpoint: endpoint,
			Status:   http.StatusBadRequest,
			Header:   transcodeResponseHeader(nil),
			Body:     transcodeErrorBody(callErr),
		}, nil
	}
	return proxyResult{
		Endpoint: endpoint,
		Status:   http.StatusOK,
		Header:   transcodeResponseHeader(md),
		Body:     responseBody,
	}, nil
}

func (g *Gateway) transcodeCallError(route Route, endpoint string, profile *TranscodeProfile, brk *breaker.AdaptiveBreaker, callErr error) (proxyResult, error) {
	g.reportEndpoint(route, endpoint, false)
	if brk != nil {
		brk.MarkFailure()
	}
	httpStatus := coreerrors.HTTPStatus(rpc.CodeOf(callErr))
	result := proxyResult{
		Endpoint: endpoint,
		Status:   httpStatus,
		Header:   transcodeResponseHeader(nil),
	}
	errorBody, mapErr := transcodeMappedErrorBody(callErr, httpStatus, profile)
	if mapErr != nil {
		g.recordTranscodeMappingError(route, "error", mapErr)
	}
	result.Body = errorBody
	// Surface non-retryable failures as a completed response so callers see
	// the mapped status, while retryable errors propagate for retry.
	if rpc.CodeOf(callErr) == rpc.CodeUnavailable || rpc.CodeOf(callErr) == rpc.CodeDeadlineExceeded {
		result.Err = callErr
		return result, callErr
	}
	return result, nil
}

type transcodeSSEBody struct {
	*io.PipeReader
	stream     rawServerStream
	settlement *transcodeStreamSettlement
}

func (b *transcodeSSEBody) Close() error {
	// The downstream ending the response is not evidence that the selected
	// upstream is unhealthy. A terminal upstream error wins this race because
	// it settles before closing the pipe writer.
	b.settlement.done(true)
	return errors.Join(b.PipeReader.Close(), b.stream.Close())
}

type transcodeStreamSettlement struct {
	once sync.Once
	fn   func(bool)
}

func (s *transcodeStreamSettlement) done(success bool) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.fn != nil {
			s.fn(success)
		}
	})
}

func newTranscodeSSEBody(ctx context.Context, stream rawServerStream, profile *TranscodeProfile, first json.RawMessage, firstErr error, settle func(bool)) io.ReadCloser {
	reader, writer := io.Pipe()
	settlement := &transcodeStreamSettlement{fn: settle}
	body := &transcodeSSEBody{PipeReader: reader, stream: stream, settlement: settlement}
	go func() {
		defer stream.Close()
		defer writer.Close()
		raw, err := first, firstErr
		for {
			if err != nil {
				if errors.Is(err, io.EOF) {
					trailers, marshalErr := json.Marshal(stream.Trailer())
					if marshalErr == nil && string(trailers) != "{}" {
						_ = writeSSEEvent(writer, "trailers", trailers)
					}
					settlement.done(true)
					return
				}
				// A downstream cancellation terminates a healthy upstream stream and
				// must not poison passive health or the circuit breaker. Deadlines and
				// all other terminal RPC errors remain endpoint failures.
				settlement.done(errors.Is(ctx.Err(), context.Canceled))
				_ = writeSSEEvent(writer, "error", transcodeErrorBody(err))
				return
			}
			payload, mapErr := transcodeResponsePayload(raw, profile)
			if mapErr != nil {
				// Response mapping is local gateway work; the upstream completed its
				// part successfully and should not be marked unhealthy.
				settlement.done(true)
				_ = writeSSEEvent(writer, "error", transcodeErrorBody(rpc.NewError(rpc.CodeInvalidArgument, mapErr.Error())))
				return
			}
			if err := writeSSEEvent(writer, "message", payload); err != nil {
				settlement.done(true)
				return
			}
			raw, err = stream.RecvRaw()
		}
	}()
	return body
}

func writeSSEEvent(writer io.Writer, event string, data []byte) error {
	if _, err := fmt.Fprintf(writer, "event: %s\ndata: ", event); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "\n\n")
	return err
}

func (g *Gateway) recordTranscodeMappingError(route Route, stage string, err error) {
	if g == nil || err == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.transcodeRuntime == nil {
		g.transcodeRuntime = make(map[string]TranscodeRuntimeSnapshot)
	}
	item := g.transcodeRuntime[routeKey(route)]
	item.LastErrorStage = strings.TrimSpace(stage)
	item.LastError = err.Error()
	g.transcodeRuntime[routeKey(route)] = item
}

func (g *Gateway) transcoderFor(endpoint string, route Route) (rpc.GenericClient, error) {
	g.transcoderMu.Lock()
	defer g.transcoderMu.Unlock()
	if g.transcodersClosed {
		return nil, errors.New("gateway transcoders are closed")
	}
	if g.transcoders == nil {
		g.transcoders = make(map[string]rpc.GenericClient)
	}
	key := fmt.Sprintf("%q|%q|%q", routeKey(route), route.Transcode.Protocol, endpoint)
	if client, ok := g.transcoders[key]; ok {
		return client, nil
	}
	factory := g.transcoderFactory
	if factory == nil {
		factory = defaultTranscoderFactory
	}
	client, err := factory(endpoint, route)
	if err != nil {
		return nil, err
	}
	g.transcoders[key] = client
	return client, nil
}

func defaultTranscoderFactory(endpoint string, route Route) (rpc.GenericClient, error) {
	if route.Transcode.Protocol == "grpc" {
		return nil, errors.New("native grpc transcoding requires NewGRPCTranscoderFactory")
	}
	target := endpoint
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	return rpc.NewClient(strings.TrimRight(target, "/"))
}

type transcodeResolvedTarget struct {
	service string
	method  string
	profile *TranscodeProfile
}

func (g *Gateway) transcodeTarget(r *http.Request, route Route) (transcodeResolvedTarget, error) {
	if descriptorName := strings.TrimSpace(route.Transcode.Descriptor); descriptorName != "" {
		return g.transcodeDescriptorTarget(r, route, descriptorName)
	}
	if strings.TrimSpace(route.Transcode.DescriptorMethod) != "" {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor is required when descriptorMethod is set")
	}
	service, method, err := transcodeTarget(r, route)
	if err != nil {
		return transcodeResolvedTarget{}, err
	}
	return transcodeResolvedTarget{service: service, method: method}, nil
}

func (g *Gateway) transcodeDescriptorTarget(r *http.Request, route Route, descriptorName string) (transcodeResolvedTarget, error) {
	desc, ok := g.descriptor(descriptorName)
	if !ok {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor not found")
	}
	method := strings.Trim(strings.TrimSpace(route.Transcode.DescriptorMethod), "/")
	if method == "" {
		method = strings.Trim(strings.TrimSpace(route.Transcode.Method), "/")
	}
	if method == "" {
		method = transcodeMethodFromPath(r.URL.Path, route.PathPrefix)
	}
	if method == "" {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor method is required")
	}
	if !descriptorHasMethod(desc, method) {
		return transcodeResolvedTarget{}, errors.New("transcode descriptor method not found")
	}
	return transcodeResolvedTarget{service: desc.Name, method: method, profile: g.transcodeProfile(desc.Name, method)}, nil
}

func (g *Gateway) descriptor(name string) (rpc.Descriptor, bool) {
	if g == nil {
		return rpc.Descriptor{}, false
	}
	name = strings.TrimSpace(name)
	g.mu.RLock()
	defer g.mu.RUnlock()
	desc, ok := g.descriptors[name]
	if !ok {
		return rpc.Descriptor{}, false
	}
	return cloneDescriptor(desc), true
}

func descriptorHasMethod(desc rpc.Descriptor, name string) bool {
	name = strings.Trim(strings.TrimSpace(name), "/")
	for _, method := range desc.Methods {
		if strings.Trim(strings.TrimSpace(method.Name), "/") == name {
			return true
		}
	}
	for _, stream := range desc.Streams {
		if strings.Trim(strings.TrimSpace(stream.Name), "/") == name {
			return true
		}
	}
	return false
}

func (g *Gateway) transcodeProfile(descriptor, method string) *TranscodeProfile {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	profile, ok := g.transcodeProfiles[transcodeProfileKey(descriptor, method)]
	if !ok {
		return nil
	}
	cloned := cloneTranscodeProfile(profile)
	return &cloned
}

func transcodeTarget(r *http.Request, route Route) (string, string, error) {
	service := strings.Trim(strings.TrimSpace(route.Transcode.Service), "/")
	if service == "" {
		service = strings.Trim(strings.TrimSpace(route.Service), "/")
	}
	if service == "" {
		return "", "", errors.New("transcode service is required")
	}
	method := strings.Trim(strings.TrimSpace(route.Transcode.Method), "/")
	if method == "" {
		method = transcodeMethodFromPath(r.URL.Path, route.PathPrefix)
	}
	if method == "" {
		return "", "", errors.New("transcode method is required")
	}
	return service, method, nil
}

func transcodeMethodFromPath(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, strings.TrimRight(prefix, "/"))
	trimmed = strings.Trim(trimmed, "/")
	return trimmed
}

func transcodeRequestPayload(r *http.Request, route Route, body []byte, profile *TranscodeProfile) (json.RawMessage, error) {
	if route.Transcode.Payload.Mode != "" {
		return transcodeMappedRequestPayload(r, route, body, profile)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(append([]byte(nil), body...)), nil
}

func transcodeMappedRequestPayload(r *http.Request, route Route, body []byte, profile *TranscodeProfile) (json.RawMessage, error) {
	bodyObject := map[string]any{}
	source := transcodePayloadSource{Body: bodyObject}
	if route.Transcode.Payload.MergeBodyObject {
		if err := mergeTranscodeBody(bodyObject, body, route.Transcode.Payload.BodyField, route.Transcode.Payload.BodyRequired, route.Transcode.Payload.BodySchema); err != nil {
			return nil, err
		}
	}
	pathValues := transcodePathValues(r.URL.Path, route.PathPrefix, route.Transcode.Payload.PathTemplate, route.Transcode.Payload.PathParams)
	typedPath, err := transcodeTypedPathValues(pathValues, route.Transcode.Payload.PathParameters)
	if err != nil {
		return nil, err
	}
	source.Path = typedPath
	queryValues, err := transcodeQueryValues(r.URL.Query(), route.Transcode.Payload.QueryParams, route.Transcode.Payload.QueryParameters)
	if err != nil {
		return nil, err
	}
	source.Query = queryValues
	headerValues, err := transcodeHeaderValues(r.Header, route.Transcode.Payload.HeaderParams, route.Transcode.Payload.HeaderParameters)
	if err != nil {
		return nil, err
	}
	source.Header = headerValues
	payloadConfig := route.Transcode.Payload
	if len(payloadConfig.Mappings) == 0 && profile != nil {
		payloadConfig.Mappings = profile.RequestMappings
	}
	payload, err := mappedTranscodePayload(source, payloadConfig)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(data), nil
}

type transcodePayloadSource struct {
	Body   any
	Path   map[string]any
	Query  map[string]any
	Header map[string]any
}

func mappedTranscodePayload(source transcodePayloadSource, config TranscodePayloadConfig) (map[string]any, error) {
	if len(config.Mappings) > 0 {
		return transcodePayloadFromMappings(source, config.Mappings)
	}
	body, _ := source.Body.(map[string]any)
	payload := make(map[string]any, len(body)+len(source.Path)+len(source.Query))
	for key, value := range body {
		payload[key] = value
	}
	for key, value := range source.Path {
		payload[key] = value
	}
	for key, value := range source.Query {
		payload[key] = value
	}
	return payload, nil
}

func transcodePayloadFromMappings(source transcodePayloadSource, mappings []TranscodePayloadMapping) (map[string]any, error) {
	payload := map[string]any{}
	for _, mapping := range mappings {
		targetPath := strings.TrimSpace(mapping.Target)
		if targetPath == "" {
			continue
		}
		value, ok, err := transcodeMappingSourceValue(source, mapping.Source)
		if err != nil {
			return nil, err
		}
		if !ok {
			if mapping.Default == nil {
				continue
			}
			value = cloneTranscodeMappingDefault(mapping.Default)
		}
		if err := setTranscodeMappingTarget(payload, targetPath, value); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func transcodeMappingSourceValue(source transcodePayloadSource, path string) (any, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false, nil
	}
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil, false, nil
	}
	var current any
	switch parts[0] {
	case "body":
		current = source.Body
	case "path":
		current = source.Path
	case "query":
		current = source.Query
	case "header":
		current = source.Header
	default:
		return nil, false, fmt.Errorf("transcode mapping source %s must start with body, path, query, or header", path)
	}
	return transcodeMappingValueAtPath(current, parts[1:], path)
}

func transcodeMappingValueAtPath(current any, parts []string, original string) (any, bool, error) {
	if len(parts) == 0 {
		return current, true, nil
	}
	part := strings.TrimSpace(parts[0])
	if part == "" {
		return nil, false, fmt.Errorf("transcode mapping source %s has empty segment", original)
	}
	if strings.HasSuffix(part, "[]") {
		name := strings.TrimSuffix(part, "[]")
		var arrayValue any
		if name == "" || name == "body" {
			arrayValue = current
		} else {
			object, ok := current.(map[string]any)
			if !ok {
				return nil, false, nil
			}
			var found bool
			arrayValue, found = object[name]
			if !found {
				return nil, false, nil
			}
		}
		items, ok := arrayValue.([]any)
		if !ok {
			return nil, false, nil
		}
		if len(parts) == 1 {
			return items, true, nil
		}
		out := make([]any, 0, len(items))
		for _, item := range items {
			value, ok, err := transcodeMappingValueAtPath(item, parts[1:], original)
			if err != nil {
				return nil, false, err
			}
			if ok {
				out = append(out, value)
			}
		}
		return out, true, nil
	}
	object, ok := current.(map[string]any)
	if !ok {
		return nil, false, nil
	}
	value, ok := object[part]
	if !ok {
		return nil, false, nil
	}
	return transcodeMappingValueAtPath(value, parts[1:], original)
}

func setTranscodeMappingTarget(payload map[string]any, path string, value any) error {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 {
		return nil
	}
	current := payload
	for index, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("transcode mapping target %s has empty segment", path)
		}
		if strings.HasSuffix(part, "[]") {
			name := strings.TrimSuffix(part, "[]")
			if name == "" {
				return fmt.Errorf("transcode mapping target %s has empty array segment", path)
			}
			if index != len(parts)-1 {
				return fmt.Errorf("transcode mapping target %s array segment must be terminal", path)
			}
			current[name] = cloneTranscodeMappingDefault(value)
			return nil
		}
		if index == len(parts)-1 {
			current[part] = cloneTranscodeMappingDefault(value)
			return nil
		}
		next, ok := current[part]
		if !ok {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("transcode mapping target %s conflicts with existing scalar", path)
		}
		current = child
	}
	return nil
}

func transcodeResponsePayload(raw json.RawMessage, profile *TranscodeProfile) ([]byte, error) {
	if profile == nil || len(profile.ResponseMappings) == 0 {
		return append([]byte(nil), raw...), nil
	}
	var body any
	if len(bytes.TrimSpace(raw)) == 0 {
		body = nil
	} else if err := json.Unmarshal(raw, &body); err != nil {
		return nil, errors.New("transcode response body must be valid json")
	}
	source := transcodePayloadSource{}
	if object, ok := body.(map[string]any); ok {
		source.Body = object
	} else {
		source.Body = body
	}
	payload, err := transcodePayloadFromMappings(source, profile.ResponseMappings)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func transcodeMappedErrorBody(err error, status int, profile *TranscodeProfile) ([]byte, error) {
	if profile == nil || len(profile.ErrorMappings) == 0 {
		return transcodeErrorBody(err), nil
	}
	source := transcodePayloadSource{Body: map[string]any{
		"code":   string(rpc.CodeOf(err)),
		"error":  err.Error(),
		"status": status,
	}}
	payload, mapErr := transcodePayloadFromMappings(source, profile.ErrorMappings)
	if mapErr != nil {
		return transcodeErrorBody(err), mapErr
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return transcodeErrorBody(err), marshalErr
	}
	return data, nil
}

func mergeTranscodeBody(payload map[string]any, body []byte, bodyField string, required bool, schema *TranscodeSchemaConfig) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		if required {
			return errors.New("transcode body is required")
		}
		return nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		if schema != nil {
			return errors.New("transcode body must be valid json")
		}
		payload[transcodeBodyField(bodyField)] = string(body)
		return nil
	}
	if schema != nil {
		if err := validateTranscodeSchemaValue(transcodeBodyField(bodyField), value, *schema); err != nil {
			return err
		}
	}
	if object, ok := value.(map[string]any); ok && bodyField == "" {
		for key, item := range object {
			payload[key] = item
		}
		return nil
	}
	payload[transcodeBodyField(bodyField)] = value
	return nil
}

func transcodeBodyField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "body"
	}
	return value
}

func validateTranscodeSchemaValue(path string, value any, schema TranscodeSchemaConfig) error {
	switch strings.ToLower(strings.TrimSpace(schema.Type)) {
	case "", "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("transcode body field %s must be object", path)
		}
		for _, name := range schema.Required {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := object[name]; !ok {
				return fmt.Errorf("transcode body field %s.%s is required", path, name)
			}
		}
		for name, property := range schema.Properties {
			item, ok := object[name]
			if !ok || item == nil {
				continue
			}
			if err := validateTranscodeSchemaValue(path+"."+name, item, property); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("transcode body field %s must be array", path)
		}
		if schema.Items != nil {
			for index, item := range items {
				if err := validateTranscodeSchemaValue(fmt.Sprintf("%s[%d]", path, index), item, *schema.Items); err != nil {
					return err
				}
			}
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number {
			return fmt.Errorf("transcode body field %s must be integer", path)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("transcode body field %s must be number", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("transcode body field %s must be boolean", path)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("transcode body field %s must be string", path)
		}
	}
	return nil
}

func transcodePathValues(path, routePrefix, template string, names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 {
		return out
	}
	templateSegments := strings.Split(strings.Trim(template, "/"), "/")
	pathSuffix := strings.Trim(strings.TrimPrefix(path, strings.TrimRight(routePrefix, "/")), "/")
	pathSegments := strings.Split(pathSuffix, "/")
	for len(pathSegments) == 1 && pathSegments[0] == "" {
		pathSegments = nil
	}
	firstDynamic := -1
	for index, segment := range templateSegments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			firstDynamic = index
			break
		}
	}
	if firstDynamic >= 0 {
		templateSegments = templateSegments[firstDynamic:]
	}
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			nameSet[name] = struct{}{}
		}
	}
	for index, segment := range templateSegments {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}"))
		if _, ok := nameSet[name]; !ok {
			continue
		}
		if index < len(pathSegments) {
			out[name] = pathSegments[index]
		}
	}
	if len(out) == len(nameSet) {
		return out
	}
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := out[name]; ok {
			continue
		}
		if i < len(pathSegments) {
			out[name] = pathSegments[i]
		}
	}
	return out
}

func transcodeTypedPathValues(values map[string]string, parameters []TranscodeParameterConfig) (map[string]any, error) {
	out := make(map[string]any, len(values))
	byName := transcodeParameterByName(parameters)
	for name, value := range values {
		parameter := byName[name]
		if parameter.Required && value == "" {
			return nil, fmt.Errorf("transcode parameter %s is required", transcodeParameterName(parameter))
		}
		converted, err := convertTranscodeParameterValue(value, parameter)
		if err != nil {
			return nil, err
		}
		out[name] = converted
	}
	for name, parameter := range byName {
		if parameter.Required {
			if _, ok := values[name]; !ok {
				return nil, fmt.Errorf("transcode parameter %s is required", transcodeParameterName(parameter))
			}
		}
	}
	return out, nil
}

func transcodeQueryValues(values url.Values, names []string, parameters []TranscodeParameterConfig) (map[string]any, error) {
	out := map[string]any{}
	byName := transcodeParameterByName(parameters)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		items, ok := values[name]
		if !ok {
			if byName[name].Required {
				return nil, fmt.Errorf("transcode parameter %s is required", transcodeParameterName(byName[name]))
			}
			continue
		}
		converted, err := convertTranscodeQueryValues(items, byName[name])
		if err != nil {
			return nil, err
		}
		out[name] = converted
	}
	return out, nil
}

func transcodeHeaderValues(header http.Header, names []string, parameters []TranscodeParameterConfig) (map[string]any, error) {
	out := map[string]any{}
	byName := transcodeParameterByName(parameters)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		values, ok := header[http.CanonicalHeaderKey(name)]
		if !ok {
			if byName[name].Required {
				return nil, fmt.Errorf("transcode parameter %s is required", transcodeParameterName(byName[name]))
			}
			continue
		}
		converted, err := convertTranscodeQueryValues(values, byName[name])
		if err != nil {
			return nil, err
		}
		out[name] = converted
		out[strings.ToLower(name)] = converted
	}
	return out, nil
}

func transcodeParameterByName(parameters []TranscodeParameterConfig) map[string]TranscodeParameterConfig {
	out := make(map[string]TranscodeParameterConfig, len(parameters))
	for _, parameter := range parameters {
		name := strings.TrimSpace(parameter.Name)
		if name != "" {
			out[name] = parameter
			out[strings.ToLower(name)] = parameter
		}
	}
	return out
}

func convertTranscodeQueryValues(values []string, parameter TranscodeParameterConfig) (any, error) {
	if len(values) == 0 {
		return convertTranscodeParameterValue("", parameter)
	}
	if strings.EqualFold(strings.TrimSpace(parameter.Type), "array") {
		items := flattenTranscodeArrayValues(values)
		out := make([]any, 0, len(items))
		itemSchema := TranscodeParameterConfig{Type: "string"}
		if parameter.Items != nil {
			itemSchema = *parameter.Items
		}
		for _, item := range items {
			converted, err := convertTranscodeParameterValue(item, itemSchema)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	}
	if len(values) > 1 {
		out := make([]any, 0, len(values))
		for _, value := range values {
			converted, err := convertTranscodeParameterValue(value, parameter)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	}
	return convertTranscodeParameterValue(values[0], parameter)
}

func flattenTranscodeArrayValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ",")
		for _, part := range parts {
			out = append(out, strings.TrimSpace(part))
		}
	}
	return out
}

func convertTranscodeParameterValue(value string, parameter TranscodeParameterConfig) (any, error) {
	switch strings.ToLower(strings.TrimSpace(parameter.Type)) {
	case "integer":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("transcode parameter %s must be integer", transcodeParameterName(parameter))
		}
		return parsed, nil
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("transcode parameter %s must be number", transcodeParameterName(parameter))
		}
		return parsed, nil
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("transcode parameter %s must be boolean", transcodeParameterName(parameter))
		}
		return parsed, nil
	case "array":
		return convertTranscodeQueryValues([]string{value}, parameter)
	default:
		return value, nil
	}
}

func transcodeParameterName(parameter TranscodeParameterConfig) string {
	name := strings.TrimSpace(parameter.Name)
	if name == "" {
		return "value"
	}
	return name
}

func transcodeContext(ctx context.Context, r *http.Request, route Route) context.Context {
	md := metadata.MD{}
	for _, name := range route.Header.AllowRequest {
		if value := r.Header.Get(name); value != "" {
			md[strings.ToLower(name)] = value
		}
	}
	if len(md) == 0 {
		return ctx
	}
	return metadata.NewContext(ctx, md)
}

func transcodeResponseHeader(md metadata.MD) http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	for key, value := range md {
		header.Set("X-Gofly-Md-"+key, value)
	}
	return header
}

func transcodeStreamResponseHeader(md metadata.MD) http.Header {
	header := transcodeResponseHeader(md)
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	return header
}

func transcodeErrorBody(err error) []byte {
	payload := struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}{
		Code:  string(rpc.CodeOf(err)),
		Error: err.Error(),
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return []byte(`{"error":"transcode failure"}`)
	}
	return data
}
