package trace

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTraceParentRoundTrip(t *testing.T) {
	sc := SpanContext{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7", Sampled: true}
	got, ok := ParseTraceParent(TraceParent(sc))
	if !ok {
		t.Fatal("traceparent should parse")
	}
	if got != sc {
		t.Fatalf("span context = %#v, want %#v", got, sc)
	}
}

func TestStartUsesParentTraceIDAndNewSpanID(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx, sc := Start(context.Background(), parent)
	if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q", sc.TraceID)
	}
	if sc.SpanID == "00f067aa0ba902b7" || len(sc.SpanID) != 16 {
		t.Fatalf("span id = %q, want new 16-char span id", sc.SpanID)
	}
	fromCtx, ok := FromContext(ctx)
	if !ok || fromCtx != sc {
		t.Fatalf("context span = %#v, %v; want %#v", fromCtx, ok, sc)
	}
}

func TestStartWithSamplerHonorsSamplerAndParent(t *testing.T) {
	ctx, sc := StartWithSampler(context.Background(), "", NeverSampler())
	if sc.Sampled {
		t.Fatal("root span should be unsampled when sampler rejects")
	}
	fromCtx, ok := FromContext(ctx)
	if !ok || fromCtx != sc {
		t.Fatalf("context span = %#v, %v; want %#v", fromCtx, ok, sc)
	}

	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	_, sc = StartWithSampler(context.Background(), parent, ParentBasedSampler(NeverSampler()))
	if !sc.Sampled {
		t.Fatal("parent-based sampler should preserve sampled parent")
	}

	parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	_, sc = StartWithSampler(context.Background(), parent, ParentBasedSampler(AlwaysSampler()))
	if sc.Sampled {
		t.Fatal("parent-based sampler should preserve unsampled parent")
	}
}

func TestRatioSamplerIsDeterministic(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	sampler := RatioSampler(0.5)
	first := sampler.Sample(traceID, false, false)
	for i := 0; i < 10; i++ {
		if got := sampler.Sample(traceID, false, false); got != first {
			t.Fatalf("sample decision changed: got %v, want %v", got, first)
		}
	}
	if RatioSampler(0).Sample(traceID, false, false) {
		t.Fatal("ratio 0 should reject")
	}
	if !RatioSampler(1).Sample(traceID, false, false) {
		t.Fatal("ratio 1 should accept")
	}
}

func TestParseTraceParentRejectsInvalidValues(t *testing.T) {
	values := []string{
		"",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-zz",
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			if _, ok := ParseTraceParent(value); ok {
				t.Fatalf("ParseTraceParent(%q) should fail", value)
			}
		})
	}
}

func TestOTelSpanContextConversion(t *testing.T) {
	sc := SpanContext{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7", Sampled: true}
	otelSC, ok := ToOTel(sc)
	if !ok {
		t.Fatal("ToOTel failed")
	}
	got, ok := FromOTel(otelSC)
	if !ok {
		t.Fatal("FromOTel failed")
	}
	if got != sc {
		t.Fatalf("converted span context = %#v, want %#v", got, sc)
	}
}

func TestToOTelRejectsInvalidSpanContext_BitsUT(t *testing.T) {
	tests := []struct {
		name string
		sc   SpanContext
	}{
		{name: "bad trace id", sc: SpanContext{TraceID: "bad", SpanID: "00f067aa0ba902b7"}},
		{name: "bad span id", sc: SpanContext{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "bad"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			otelSC, ok := ToOTel(tt.sc)
			if ok {
				t.Fatalf("ToOTel(%#v) ok = true, want false", tt.sc)
			}
			if otelSC.IsValid() {
				t.Fatalf("ToOTel(%#v) returned valid span context", tt.sc)
			}
		})
	}
}

func TestStartAgentDisabledAndShutdownNoop(t *testing.T) {
	var nilCtx context.Context
	agent, err := StartAgent(nilCtx, AgentConfig{Enabled: false})
	if err != nil {
		t.Fatalf("StartAgent disabled error = %v", err)
	}
	if agent == nil {
		t.Fatal("StartAgent disabled returned nil agent")
	}
	if err := agent.Shutdown(nilCtx); err != nil {
		t.Fatalf("Shutdown disabled agent error = %v", err)
	}
	if err := (*Agent)(nil).Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown nil agent error = %v", err)
	}
}

func TestStartAgentWithExporterAndShutdown(t *testing.T) {
	exporter := &fakeSpanExporter{}
	agent, err := StartAgent(context.Background(), AgentConfig{Enabled: true, ServiceName: "checkout", SampleRatio: 0.5}, exporter)
	if err != nil {
		t.Fatalf("StartAgent error = %v", err)
	}
	if agent == nil || agent.provider == nil {
		t.Fatalf("agent = %#v, want initialized provider", agent)
	}
	if err := agent.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown error = %v", err)
	}
	if !exporter.shutdown {
		t.Fatal("exporter shutdown was not called")
	}
}

type fakeSpanExporter struct {
	shutdown bool
}

func (f *fakeSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}

func (f *fakeSpanExporter) Shutdown(context.Context) error {
	f.shutdown = true
	return nil
}

func TestShouldSampleNilSamplerAndEmptyTraceID(t *testing.T) {
	if !ShouldSample(nil, "abc") {
		t.Fatal("nil sampler should default to true")
	}
	if ShouldSample(NeverSampler(), "abc") {
		t.Fatal("NeverSampler should reject")
	}
	if !ShouldSample(AlwaysSampler(), "") {
		t.Fatal("empty traceID with AlwaysSampler should be true")
	}
}

func TestStartAgentNilContextAndDefaults(t *testing.T) {
	var nilCtx context.Context
	agent, err := StartAgent(nilCtx, AgentConfig{Enabled: true})
	if err != nil {
		t.Fatalf("StartAgent nil ctx error = %v", err)
	}
	if agent == nil {
		t.Fatal("StartAgent nil ctx should return agent")
	}
	if err := agent.Shutdown(nilCtx); err != nil {
		t.Fatalf("Shutdown nil ctx error = %v", err)
	}
}

func TestStartAgentBadSampleRatioClamped(t *testing.T) {
	agent, err := StartAgent(context.Background(), AgentConfig{Enabled: true, ServiceName: "test", SampleRatio: -0.5})
	if err != nil {
		t.Fatalf("StartAgent negative ratio error = %v", err)
	}
	if agent == nil {
		t.Fatal("agent should be non-nil")
	}
	if err := agent.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown error = %v", err)
	}

	agent, err = StartAgent(context.Background(), AgentConfig{Enabled: true, ServiceName: "test", SampleRatio: 2.0})
	if err != nil {
		t.Fatalf("StartAgent >1 ratio error = %v", err)
	}
	if err := agent.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown error = %v", err)
	}
}

func TestFromOTelInvalidSpanContext(t *testing.T) {
	_, ok := FromOTel(oteltrace.SpanContext{})
	if ok {
		t.Fatal("FromOTel with invalid span context should return false")
	}
}

func TestParentBasedSamplerNilRoot(t *testing.T) {
	s := ParentBasedSampler(nil)
	if !s.Sample("abc", false, false) {
		t.Fatal("ParentBasedSampler with nil root should default to AlwaysSampler")
	}
}

func TestRatioSamplerEdgeCases(t *testing.T) {
	if s := RatioSampler(-0.1); !AlwaysSampler().Sample("abc", false, false) && s.Sample("abc", false, false) {
		t.Fatal("negative ratio should behave like NeverSampler")
	}
	if s := RatioSampler(0); s.Sample("abc", false, false) {
		t.Fatal("ratio 0 should reject")
	}
	if s := RatioSampler(1.5); !s.Sample("abc", false, false) {
		t.Fatal("ratio >=1 should accept")
	}
}
