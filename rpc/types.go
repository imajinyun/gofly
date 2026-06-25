// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/imajinyun/gofly/rpc/endpoint"
)

// Handler is an RPC method handler.
type Handler func(ctx context.Context, req any) (any, error)

// StreamHandler is an RPC streaming handler.
type StreamHandler func(ctx context.Context, stream *Stream) error

// StreamMiddleware wraps a streaming RPC handler with cross-cutting behavior.
type StreamMiddleware func(StreamHandler) StreamHandler

// ClientStreamHandler opens a client-side RPC stream.
type ClientStreamHandler func(ctx context.Context, method string) (*Stream, error)

// ClientStreamMiddleware wraps client-side stream creation with cross-cutting behavior.
type ClientStreamMiddleware func(ClientStreamHandler) ClientStreamHandler

// MethodDesc describes a single RPC method.
type MethodDesc struct {
	Name        string
	Handler     Handler
	NewRequest  func() any
	Request     string
	Response    string
	Codec       string
	HTTP        HTTPBinding
	Timeout     time.Duration
	Metadata    map[string]string
	Middlewares []endpoint.Middleware
}

// StreamDesc describes a single RPC stream.
type StreamDesc struct {
	Name        string
	Handler     StreamHandler
	NewMessage  func() any
	Message     string
	Codec       string
	Mode        StreamMode
	Timeout     time.Duration
	Metadata    map[string]string
	Middlewares []StreamMiddleware
}

// ServiceDesc describes a registered RPC service.
type ServiceDesc struct {
	Name     string
	Version  string
	Metadata map[string]string
	Methods  []MethodDesc
	Streams  []StreamDesc
}

// Descriptor is a JSON-friendly service descriptor.
type Descriptor struct {
	Name     string             `json:"name"`
	Version  string             `json:"version,omitempty"`
	Metadata map[string]string  `json:"metadata,omitempty"`
	Methods  []MethodDescriptor `json:"methods,omitempty"`
	Streams  []StreamDescriptor `json:"streams,omitempty"`
}

// MethodDescriptor is a JSON-friendly method descriptor.
type MethodDescriptor struct {
	Name     string            `json:"name"`
	Timeout  time.Duration     `json:"timeout,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Request  string            `json:"request,omitempty"`
	Response string            `json:"response,omitempty"`
	Codec    string            `json:"codec,omitempty"`
	HTTP     *HTTPBinding      `json:"http,omitempty"`
}

type StreamDescriptor struct {
	Name     string            `json:"name"`
	Timeout  time.Duration     `json:"timeout,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Message  string            `json:"message,omitempty"`
	Codec    string            `json:"codec,omitempty"`
	Mode     StreamMode        `json:"mode,omitempty"`
}

type HTTPBinding struct {
	Method       string `json:"method,omitempty"`
	Path         string `json:"path,omitempty"`
	Body         string `json:"body,omitempty"`
	ResponseBody string `json:"responseBody,omitempty"`
}

func (b HTTPBinding) Empty() bool {
	return strings.TrimSpace(b.Method) == "" &&
		strings.TrimSpace(b.Path) == "" &&
		strings.TrimSpace(b.Body) == "" &&
		strings.TrimSpace(b.ResponseBody) == ""
}

type StreamMode string

const (
	StreamModeUnary        StreamMode = "unary"
	StreamModeClientStream StreamMode = "client_stream"
	StreamModeServerStream StreamMode = "server_stream"
	StreamModeBidiStream   StreamMode = "bidi_stream"
)

type DescriptorChangeSeverity string

const (
	DescriptorChangeBreaking DescriptorChangeSeverity = "breaking"
	DescriptorChangeWarning  DescriptorChangeSeverity = "warning"
	DescriptorChangeInfo     DescriptorChangeSeverity = "info"
)

type DescriptorChangeCategory string

const (
	DescriptorChangeService   DescriptorChangeCategory = "service"
	DescriptorChangeMethod    DescriptorChangeCategory = "method"
	DescriptorChangeStream    DescriptorChangeCategory = "stream"
	DescriptorChangeType      DescriptorChangeCategory = "type"
	DescriptorChangeField     DescriptorChangeCategory = "field"
	DescriptorChangeEnum      DescriptorChangeCategory = "enum"
	DescriptorChangeSignature DescriptorChangeCategory = "signature"
	DescriptorChangeVersion   DescriptorChangeCategory = "version"
	DescriptorChangeTimeout   DescriptorChangeCategory = "timeout"
	DescriptorChangeCodec     DescriptorChangeCategory = "codec"
	DescriptorChangeBinding   DescriptorChangeCategory = "binding"
)

type DescriptorChange struct {
	Category    DescriptorChangeCategory `json:"category"`
	Severity    DescriptorChangeSeverity `json:"severity"`
	Subject     string                   `json:"subject"`
	Description string                   `json:"description"`
}

type DescriptorCompatibilityReport struct {
	Changes  []DescriptorChange `json:"changes"`
	Breaking int                `json:"breaking"`
	Warnings int                `json:"warnings"`
}

func (r DescriptorCompatibilityReport) IsCompatible() bool { return r.Breaking == 0 }

func (r DescriptorCompatibilityReport) HasBreaking() bool { return r.Breaking > 0 }

func (d Descriptor) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("descriptor name is required")
	}
	seenMethods := make(map[string]struct{}, len(d.Methods))
	for _, method := range d.Methods {
		name := strings.TrimSpace(method.Name)
		if name == "" {
			return errors.New("descriptor method name is required")
		}
		if _, ok := seenMethods[name]; ok {
			return fmt.Errorf("descriptor method %q is duplicated", name)
		}
		if method.Timeout < 0 {
			return fmt.Errorf("descriptor method %q timeout must be non-negative", name)
		}
		if err := validateDescriptorCodec(method.Codec); err != nil {
			return fmt.Errorf("descriptor method %q %w", name, err)
		}
		if err := validateDescriptorHTTPBinding(method.HTTP); err != nil {
			return fmt.Errorf("descriptor method %q %w", name, err)
		}
		seenMethods[name] = struct{}{}
	}
	seenStreams := make(map[string]struct{}, len(d.Streams))
	for _, stream := range d.Streams {
		name := strings.TrimSpace(stream.Name)
		if name == "" {
			return errors.New("descriptor stream name is required")
		}
		if _, ok := seenStreams[name]; ok {
			return fmt.Errorf("descriptor stream %q is duplicated", name)
		}
		if stream.Timeout < 0 {
			return fmt.Errorf("descriptor stream %q timeout must be non-negative", name)
		}
		if err := validateDescriptorCodec(stream.Codec); err != nil {
			return fmt.Errorf("descriptor stream %q %w", name, err)
		}
		if err := validateDescriptorStreamMode(stream.Mode); err != nil {
			return fmt.Errorf("descriptor stream %q %w", name, err)
		}
		seenStreams[name] = struct{}{}
	}
	return nil
}

func validateDescriptorCodec(codec string) error {
	codec = strings.ToLower(strings.TrimSpace(codec))
	if codec == "" {
		return nil
	}
	switch codec {
	case "json", "proto", "protobuf", "thrift":
		return nil
	default:
		return fmt.Errorf("codec %q is unsupported", codec)
	}
}

func validateDescriptorHTTPBinding(binding *HTTPBinding) error {
	if binding == nil || binding.Empty() {
		return nil
	}
	method := strings.ToUpper(strings.TrimSpace(binding.Method))
	path := strings.TrimSpace(binding.Path)
	if method == "" || path == "" {
		return errors.New("http binding requires both method and path")
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		return fmt.Errorf("http binding method %q is unsupported", binding.Method)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("http binding path %q must be absolute", binding.Path)
	}
	return nil
}

func validateDescriptorStreamMode(mode StreamMode) error {
	mode = StreamMode(strings.TrimSpace(string(mode)))
	if mode == "" {
		return nil
	}
	switch mode {
	case StreamModeUnary, StreamModeClientStream, StreamModeServerStream, StreamModeBidiStream:
		return nil
	default:
		return fmt.Errorf("stream mode %q is unsupported", mode)
	}
}

func CompareDescriptors(base, target Descriptor) DescriptorCompatibilityReport {
	var report DescriptorCompatibilityReport
	if strings.TrimSpace(base.Name) != "" && strings.TrimSpace(target.Name) != "" && base.Name != target.Name {
		report.addDescriptorChange(DescriptorChangeService, DescriptorChangeBreaking, base.Name, fmt.Sprintf("service name changed from %s to %s", base.Name, target.Name))
	}
	if base.Version != "" && target.Version != "" && base.Version != target.Version {
		report.addDescriptorChange(DescriptorChangeVersion, DescriptorChangeInfo, base.Name, fmt.Sprintf("service version changed from %s to %s", base.Version, target.Version))
	}

	baseMethods := methodDescriptorMap(base.Methods)
	targetMethods := methodDescriptorMap(target.Methods)
	for _, name := range methodDescriptorNames(baseMethods) {
		bm := baseMethods[name]
		tm, ok := targetMethods[name]
		if !ok {
			report.addDescriptorChange(DescriptorChangeMethod, DescriptorChangeBreaking, base.Name+"/"+name, "method was removed")
			continue
		}
		report.compareMethodDescriptor(base.Name, bm, tm)
	}
	for _, name := range methodDescriptorNames(targetMethods) {
		if _, ok := baseMethods[name]; !ok {
			report.addDescriptorChange(DescriptorChangeMethod, DescriptorChangeInfo, target.Name+"/"+name, "method was added")
		}
	}

	baseStreams := streamDescriptorMap(base.Streams)
	targetStreams := streamDescriptorMap(target.Streams)
	for _, name := range streamDescriptorNames(baseStreams) {
		bs := baseStreams[name]
		ts, ok := targetStreams[name]
		if !ok {
			report.addDescriptorChange(DescriptorChangeStream, DescriptorChangeBreaking, base.Name+"/"+name, "stream was removed")
			continue
		}
		report.compareStreamDescriptor(base.Name, bs, ts)
	}
	for _, name := range streamDescriptorNames(targetStreams) {
		if _, ok := baseStreams[name]; !ok {
			report.addDescriptorChange(DescriptorChangeStream, DescriptorChangeInfo, target.Name+"/"+name, "stream was added")
		}
	}
	return report
}

func (r *DescriptorCompatibilityReport) compareMethodDescriptor(service string, base, target MethodDescriptor) {
	subject := service + "/" + base.Name
	compareDescriptorType(r, subject+" request", base.Request, target.Request)
	compareDescriptorType(r, subject+" response", base.Response, target.Response)
	compareDescriptorString(r, DescriptorChangeCodec, subject+" codec", base.Codec, target.Codec)
	compareDescriptorHTTPBinding(r, subject+" http", base.HTTP, target.HTTP)
	compareDescriptorTimeout(r, subject, base.Timeout, target.Timeout)
}

func (r *DescriptorCompatibilityReport) compareStreamDescriptor(service string, base, target StreamDescriptor) {
	subject := service + "/" + base.Name
	compareDescriptorType(r, subject+" message", base.Message, target.Message)
	compareDescriptorString(r, DescriptorChangeCodec, subject+" codec", base.Codec, target.Codec)
	compareDescriptorString(r, DescriptorChangeBinding, subject+" mode", string(base.Mode), string(target.Mode))
	compareDescriptorTimeout(r, subject, base.Timeout, target.Timeout)
}

func compareDescriptorType(r *DescriptorCompatibilityReport, subject string, base, target string) {
	base = strings.TrimSpace(base)
	target = strings.TrimSpace(target)
	switch {
	case base == target:
		return
	case base != "" && target == "":
		r.addDescriptorChange(DescriptorChangeSignature, DescriptorChangeBreaking, subject, fmt.Sprintf("type metadata %s was removed", base))
	case base != "" && target != "":
		r.addDescriptorChange(DescriptorChangeSignature, DescriptorChangeBreaking, subject, fmt.Sprintf("type changed from %s to %s", base, target))
	case base == "" && target != "":
		r.addDescriptorChange(DescriptorChangeSignature, DescriptorChangeInfo, subject, fmt.Sprintf("type metadata %s was added", target))
	}
}

func compareDescriptorTimeout(r *DescriptorCompatibilityReport, subject string, base, target time.Duration) {
	if base <= 0 || target <= 0 || base == target {
		return
	}
	if target < base {
		r.addDescriptorChange(DescriptorChangeTimeout, DescriptorChangeWarning, subject, fmt.Sprintf("timeout decreased from %s to %s", base, target))
		return
	}
	r.addDescriptorChange(DescriptorChangeTimeout, DescriptorChangeInfo, subject, fmt.Sprintf("timeout increased from %s to %s", base, target))
}

func compareDescriptorString(r *DescriptorCompatibilityReport, category DescriptorChangeCategory, subject string, base, target string) {
	base = strings.TrimSpace(base)
	target = strings.TrimSpace(target)
	switch {
	case base == target:
		return
	case base != "" && target == "":
		r.addDescriptorChange(category, DescriptorChangeBreaking, subject, fmt.Sprintf("%s was removed", base))
	case base != "" && target != "":
		r.addDescriptorChange(category, DescriptorChangeBreaking, subject, fmt.Sprintf("changed from %s to %s", base, target))
	case base == "" && target != "":
		r.addDescriptorChange(category, DescriptorChangeInfo, subject, fmt.Sprintf("%s was added", target))
	}
}

func compareDescriptorHTTPBinding(r *DescriptorCompatibilityReport, subject string, base, target *HTTPBinding) {
	var baseValue, targetValue HTTPBinding
	if base != nil {
		baseValue = *base
	}
	if target != nil {
		targetValue = *target
	}
	compareDescriptorString(r, DescriptorChangeBinding, subject+" method", baseValue.Method, targetValue.Method)
	compareDescriptorString(r, DescriptorChangeBinding, subject+" path", baseValue.Path, targetValue.Path)
	compareDescriptorString(r, DescriptorChangeBinding, subject+" body", baseValue.Body, targetValue.Body)
	compareDescriptorString(r, DescriptorChangeBinding, subject+" response body", baseValue.ResponseBody, targetValue.ResponseBody)
}

func (r *DescriptorCompatibilityReport) addDescriptorChange(category DescriptorChangeCategory, severity DescriptorChangeSeverity, subject, description string) {
	r.Changes = append(r.Changes, DescriptorChange{
		Category:    category,
		Severity:    severity,
		Subject:     subject,
		Description: description,
	})
	if severity == DescriptorChangeBreaking {
		r.Breaking++
	} else if severity == DescriptorChangeWarning {
		r.Warnings++
	}
}

func methodDescriptorMap(methods []MethodDescriptor) map[string]MethodDescriptor {
	out := make(map[string]MethodDescriptor, len(methods))
	for _, method := range methods {
		out[method.Name] = method
	}
	return out
}

func methodDescriptorNames(methods map[string]MethodDescriptor) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func streamDescriptorMap(streams []StreamDescriptor) map[string]StreamDescriptor {
	out := make(map[string]StreamDescriptor, len(streams))
	for _, stream := range streams {
		out[stream.Name] = stream
	}
	return out
}

func streamDescriptorNames(streams map[string]StreamDescriptor) []string {
	names := make([]string, 0, len(streams))
	for name := range streams {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (d ServiceDesc) Descriptor() Descriptor {
	out := Descriptor{
		Name:     d.Name,
		Version:  d.Version,
		Metadata: cloneStringMap(d.Metadata),
		Methods:  make([]MethodDescriptor, 0, len(d.Methods)),
		Streams:  make([]StreamDescriptor, 0, len(d.Streams)),
	}
	for _, method := range d.Methods {
		md := MethodDescriptor{
			Name:     method.Name,
			Timeout:  method.Timeout,
			Metadata: cloneStringMap(method.Metadata),
			Request:  descriptorTypeName(method.Request, method.Metadata, "request"),
			Response: descriptorTypeName(method.Response, method.Metadata, "response"),
			Codec:    descriptorTypeName(method.Codec, method.Metadata, "codec"),
			HTTP:     descriptorHTTPBinding(method.HTTP, method.Metadata),
		}
		out.Methods = append(out.Methods, md)
	}
	sort.Slice(out.Methods, func(i, j int) bool { return out.Methods[i].Name < out.Methods[j].Name })
	for _, stream := range d.Streams {
		sd := StreamDescriptor{
			Name:     stream.Name,
			Timeout:  stream.Timeout,
			Metadata: cloneStringMap(stream.Metadata),
			Message:  descriptorTypeName(stream.Message, stream.Metadata, "message"),
			Codec:    descriptorTypeName(stream.Codec, stream.Metadata, "codec"),
			Mode:     descriptorStreamMode(stream.Mode, stream.Metadata),
		}
		out.Streams = append(out.Streams, sd)
	}
	sort.Slice(out.Streams, func(i, j int) bool { return out.Streams[i].Name < out.Streams[j].Name })
	return out
}

func descriptorTypeName(explicit string, metadata map[string]string, key string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	if len(metadata) == 0 {
		return ""
	}
	return strings.TrimSpace(metadata[key])
}

func descriptorHTTPBinding(explicit HTTPBinding, metadata map[string]string) *HTTPBinding {
	if !explicit.Empty() {
		binding := explicit
		return &binding
	}
	if len(metadata) == 0 {
		return nil
	}
	binding := HTTPBinding{
		Method:       strings.TrimSpace(metadata["http.method"]),
		Path:         strings.TrimSpace(metadata["http.path"]),
		Body:         strings.TrimSpace(metadata["http.body"]),
		ResponseBody: strings.TrimSpace(metadata["http.responseBody"]),
	}
	if binding.Empty() {
		return nil
	}
	return &binding
}

func descriptorStreamMode(explicit StreamMode, metadata map[string]string) StreamMode {
	if strings.TrimSpace(string(explicit)) != "" {
		return explicit
	}
	if len(metadata) == 0 {
		return ""
	}
	if mode := strings.TrimSpace(metadata["stream.mode"]); mode != "" {
		return StreamMode(mode)
	}
	client := strings.EqualFold(strings.TrimSpace(metadata["clientStream"]), "true")
	server := strings.EqualFold(strings.TrimSpace(metadata["serverStream"]), "true")
	switch {
	case client && server:
		return StreamModeBidiStream
	case client:
		return StreamModeClientStream
	case server:
		return StreamModeServerStream
	default:
		return StreamModeUnary
	}
}

func (d ServiceDesc) Method(name string) (MethodDesc, bool) {
	name = canonicalRPCName(name)
	for _, method := range d.Methods {
		if canonicalRPCName(method.Name) == name {
			return cloneMethodDesc(method), true
		}
	}
	return MethodDesc{}, false
}

func (d ServiceDesc) MethodPath(name string) (string, error) {
	method, ok := d.Method(name)
	if !ok {
		return "", fmt.Errorf("rpc method %s is not declared in service %s", canonicalRPCName(name), canonicalRPCName(d.Name))
	}
	return MethodPath(d.Name, method.Name)
}

func (d ServiceDesc) MustMethodPath(name string) string {
	path, err := d.MethodPath(name)
	if err != nil {
		panic(err)
	}
	return path
}

func (d ServiceDesc) Stream(name string) (StreamDesc, bool) {
	name = canonicalRPCName(name)
	for _, stream := range d.Streams {
		if canonicalRPCName(stream.Name) == name {
			return cloneStreamDesc(stream), true
		}
	}
	return StreamDesc{}, false
}

func (d ServiceDesc) StreamPath(name string) (string, error) {
	stream, ok := d.Stream(name)
	if !ok {
		return "", fmt.Errorf("rpc stream %s is not declared in service %s", canonicalRPCName(name), canonicalRPCName(d.Name))
	}
	return MethodPath(d.Name, stream.Name)
}

func (d ServiceDesc) MustStreamPath(name string) string {
	path, err := d.StreamPath(name)
	if err != nil {
		panic(err)
	}
	return path
}

func (d ServiceDesc) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("rpc service name is required")
	}
	seenMethods := make(map[string]struct{}, len(d.Methods))
	for _, method := range d.Methods {
		methodName := canonicalRPCName(method.Name)
		if methodName == "" {
			return fmt.Errorf("rpc service %s method name is required", d.Name)
		}
		if method.NewRequest == nil {
			return fmt.Errorf("rpc service %s method %s request factory is required", d.Name, method.Name)
		}
		if _, ok := seenMethods[methodName]; ok {
			return fmt.Errorf("rpc service %s method %s is duplicated", d.Name, methodName)
		}
		seenMethods[methodName] = struct{}{}
	}
	seenStreams := make(map[string]struct{}, len(d.Streams))
	for _, stream := range d.Streams {
		streamName := canonicalRPCName(stream.Name)
		if streamName == "" {
			return fmt.Errorf("rpc service %s stream name is required", d.Name)
		}
		if stream.NewMessage == nil {
			return fmt.Errorf("rpc service %s stream %s message factory is required", d.Name, stream.Name)
		}
		if _, ok := seenStreams[streamName]; ok {
			return fmt.Errorf("rpc service %s stream %s is duplicated", d.Name, streamName)
		}
		seenStreams[streamName] = struct{}{}
	}
	return d.Descriptor().Validate()
}

func CloneServiceDesc(desc ServiceDesc) ServiceDesc {
	return cloneServiceDesc(desc)
}

type Server interface {
	RegisterService(desc ServiceDesc, impl any) error
	GetServiceInfos() map[string]ServiceDesc
	GetServiceDescriptors() map[string]Descriptor
	GetServiceDescriptor(name string) (Descriptor, bool)
	Run() error
	Stop(ctx context.Context) error
}

type RuntimeDescriber interface {
	RuntimeDescriptor() Descriptor
}
