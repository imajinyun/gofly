package mq

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/metrics"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/core/trace"
)

func TestMemoryBrokerPublishConsumeAck(t *testing.T) {
	broker := NewMemoryBroker()
	received := make(chan Message, 1)
	sub, err := broker.Subscribe(context.Background(), "orders", "workers", func(ctx context.Context, msg Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop(context.Background())
	headers := map[string]string{"trace": "t1"}
	body := []byte("create")
	if err := broker.Publish(context.Background(), Message{Topic: "orders", Key: "k1", Body: body, Headers: headers}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	body[0] = 'X'
	headers["trace"] = "mutated"
	select {
	case msg := <-received:
		if msg.Topic != "orders" || string(msg.Body) != "create" || msg.Headers["trace"] != "t1" || msg.ID == "" {
			t.Fatalf("unexpected message: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
	waitFor(t, time.Second, func() bool { return broker.Snapshot().Acked == 1 })
	snapshot := broker.Snapshot()
	if snapshot.Published != 1 || snapshot.Delivered != 1 || snapshot.Acked != 1 || snapshot.Topics["orders"].Published != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestGovernanceBrokerManagerOverridesExplicitRuleSet(t *testing.T) {
	stale := governance.NewRuleSet(governance.Rule{Name: "stale", Transport: governance.TransportMQ, Service: "orders"})
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{Name: "live", Transport: governance.TransportMQ, Service: "orders"}}})
	if err != nil {
		t.Fatal(err)
	}

	broker, err := NewGovernanceBroker(
		AsBroker(NewMemoryBroker()),
		WithGovernanceService("orders"),
		WithGovernanceRuleSet(stale),
		WithGovernanceManager(manager),
	)
	if err != nil {
		t.Fatal(err)
	}
	decision := broker.decision(governance.Request{Transport: governance.TransportMQ, Service: "orders"})
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("decision = %#v, want manager rule", decision)
	}
}

func TestGovernanceBrokerSuiteProvidesRules(t *testing.T) {
	suite := governance.MustNewSuite(governance.NewPlugin("mq-default", governance.Rule{Name: "suite", Transport: governance.TransportMQ, Service: "orders"}))
	broker, err := NewGovernanceBroker(AsBroker(NewMemoryBroker()), WithGovernanceService("orders"), WithGovernanceSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	decision := broker.decision(governance.Request{Transport: governance.TransportMQ, Service: "orders"})
	if !decision.Matched || decision.RuleName != "suite" {
		t.Fatalf("decision = %#v, want suite rule", decision)
	}
}

func TestGovernanceBrokerManagerOverridesLaterSuite(t *testing.T) {
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{Name: "live", Transport: governance.TransportMQ, Service: "orders"}}})
	if err != nil {
		t.Fatal(err)
	}
	suite := governance.MustNewSuite(governance.NewPlugin("stale", governance.Rule{Name: "stale", Transport: governance.TransportMQ, Service: "orders"}))
	broker, err := NewGovernanceBroker(AsBroker(NewMemoryBroker()), WithGovernanceService("orders"), WithGovernanceManager(manager), WithGovernanceSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	decision := broker.decision(governance.Request{Transport: governance.TransportMQ, Service: "orders"})
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("decision = %#v, want manager rule", decision)
	}
}

func TestMemoryBrokerRetryThenAck(t *testing.T) {
	broker := NewMemoryBroker()
	var calls atomic.Int64
	sub, err := broker.Subscribe(context.Background(), "tasks", "workers", func(ctx context.Context, msg Message) error {
		if calls.Add(1) == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}, WithRetryBackoff(5*time.Millisecond), WithMaxAttempts(3))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop(context.Background())
	if err := broker.Publish(context.Background(), Message{Topic: "tasks", Body: []byte("run")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	waitFor(t, time.Second, func() bool { return broker.Snapshot().Acked == 1 })
	snapshot := broker.Snapshot()
	if calls.Load() != 2 || snapshot.Nacked != 1 || snapshot.Retried != 1 || snapshot.Dead != 0 {
		t.Fatalf("unexpected calls/snapshot: calls=%d snapshot=%+v", calls.Load(), snapshot)
	}
}

func TestMemoryBrokerDeadLetterAfterRetries(t *testing.T) {
	broker := NewMemoryBroker()
	dlq := make(chan Message, 1)
	dlqSub, err := broker.Subscribe(context.Background(), "tasks.dlq", "audit", func(ctx context.Context, msg Message) error {
		dlq <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe dlq: %v", err)
	}
	defer dlqSub.Stop(context.Background())
	sub, err := broker.Subscribe(context.Background(), "tasks", "workers", func(ctx context.Context, msg Message) error {
		return errors.New("permanent failure")
	}, WithRetryBackoff(time.Millisecond), WithMaxAttempts(2), WithDeadLetterTopic("tasks.dlq"))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop(context.Background())
	if err := broker.Publish(context.Background(), Message{Topic: "tasks", Key: "bad"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case msg := <-dlq:
		if msg.Topic != "tasks.dlq" || msg.Key != "bad" || msg.Attempts != 2 {
			t.Fatalf("unexpected DLQ message: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DLQ message")
	}
	waitFor(t, time.Second, func() bool { return broker.Snapshot().Dead == 1 && broker.Snapshot().Acked == 1 })
}

func TestMemoryBrokerRecordsDroppedDeadLetterPublish(t *testing.T) {
	broker := NewMemoryBroker()
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sub := &Subscription{
		broker: broker,
		topic:  "tasks",
		group:  "workers",
		cfg:    newSubscriptionConfig("tasks", WithMaxAttempts(1), WithDeadLetterTopic("tasks.dlq")),
		ctx:    context.Background(),
	}

	sub.handle(func(context.Context, Message) error { return errors.New("permanent failure") }, Message{Topic: "tasks"})

	snapshot := broker.Snapshot()
	if snapshot.Dead != 1 || snapshot.Dropped != 1 || snapshot.Groups["tasks:workers"].Dropped != 1 {
		t.Fatalf("snapshot = %+v, want dead and dropped counters", snapshot)
	}
}

func TestMemoryBrokerValidationCloseAndCancellation(t *testing.T) {
	broker := NewMemoryBroker()
	if err := broker.Publish(context.Background(), Message{}); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("Publish invalid = %v, want ErrInvalidTopic", err)
	}
	if _, err := broker.Subscribe(context.Background(), "topic", "", func(ctx context.Context, msg Message) error { return nil }); !errors.Is(err, ErrInvalidGroup) {
		t.Fatalf("Subscribe invalid group = %v, want ErrInvalidGroup", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := broker.Publish(ctx, Message{Topic: "topic"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish canceled = %v, want context.Canceled", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := broker.Publish(context.Background(), Message{Topic: "topic"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish closed = %v, want ErrClosed", err)
	}
}

func TestGovernanceBrokerPublishAppliesRulesTraceRetryAndMetrics(t *testing.T) {
	var calls atomic.Int64
	fake := &governanceFakeBroker{publish: func(ctx context.Context, msg Message) error {
		calls.Add(1)
		if msg.Headers["x-governance"] != "on" || msg.Headers[governance.HeaderCanary] != "true" || msg.Headers["x-lane"] != "beta" {
			t.Fatalf("publish headers missing governance values: %#v", msg.Headers)
		}
		if msg.Headers[trace.TraceParentHeader] == "" || msg.Headers[metadata.RequestIDKey] != "rid-1" {
			t.Fatalf("publish headers missing trace/request metadata: %#v", msg.Headers)
		}
		if calls.Load() == 1 {
			return errors.New("temporary publish failure")
		}
		return nil
	}}
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "mq-publish",
		Transport: governance.TransportMQ,
		Service:   "ordersvc",
		Method:    "PUBLISH",
		Path:      "/orders",
		Policy: governance.Policy{
			Retry:    governance.RetryPolicy{Attempts: 2},
			Metadata: map[string]string{"x-governance": "on"},
			Canary: governance.CanaryPolicy{
				MatchHeaders: map[string]string{"X-Tenant": "beta"},
				Headers:      map[string]string{"x-lane": "beta"},
			},
		},
	})
	registry := metrics.NewRegistry()
	broker, err := NewGovernanceBroker(fake,
		WithGovernanceService("ordersvc"),
		WithGovernanceRuleSet(rules),
		WithGovernanceMetrics(registry),
		WithGovernanceTrace(true),
		WithGovernanceTraceSampler(trace.AlwaysSampler()),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.Append(context.Background(), "x-tenant", "beta", metadata.RequestIDKey, "rid-1")
	if err := broker.Publish(ctx, Message{Topic: "orders", Body: []byte("create")}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("publish calls = %d, want 2", calls.Load())
	}
	if stats := rules.Stats(); len(stats) != 1 || stats[0].Hits != 1 {
		t.Fatalf("rules stats = %#v, want one hit", stats)
	}
	snapshot := registry.Snapshot()
	if route := snapshot.Routes["mq.PUBLISH.orders"]; route.Requests != 1 || route.Errors != 0 {
		t.Fatalf("metrics route = %#v, want one successful publish", route)
	}
}

func TestGovernanceBrokerSubscribeWrapsHandlerWithContextAndRules(t *testing.T) {
	fake := &governanceFakeBroker{}
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "mq-consume",
		Transport: governance.TransportMQ,
		Service:   "ordersvc",
		Method:    "CONSUME",
		Path:      "/orders",
		Tags:      map[string]string{"mq.group": "workers", "env": "prod"},
		Policy: governance.Policy{
			Metadata:    map[string]string{"x-governance": "consumer"},
			Concurrency: governance.ConcurrencyPolicy{Limit: 1},
		},
	})
	tags := map[string]string{"env": "prod"}
	broker, err := NewGovernanceBroker(fake, WithGovernanceService("ordersvc"), WithGovernanceRuleSet(rules), WithGovernanceTags(tags), WithGovernanceTrace(true))
	if err != nil {
		t.Fatal(err)
	}
	tags["env"] = "staging"
	if _, err := broker.Subscribe(context.Background(), "orders", "workers", func(ctx context.Context, msg Message) error {
		if msg.Headers["x-governance"] != "consumer" || msg.Headers[trace.TraceParentHeader] == "" {
			t.Fatalf("handler message headers = %#v, want governance and trace", msg.Headers)
		}
		md, ok := metadata.FromContext(ctx)
		if !ok || md.Get("mq.topic") != "orders" || md.Get("mq.group") != "workers" || md.Get("x-governance") != "consumer" {
			t.Fatalf("handler metadata = %#v, want mq context", md)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if fake.handler == nil {
		t.Fatal("wrapped handler was not registered")
	}
	if err := fake.handler(context.Background(), Message{Topic: "orders", Headers: map[string]string{"source": "test"}}); err != nil {
		t.Fatal(err)
	}
	if stats := rules.Stats(); len(stats) != 1 || stats[0].Hits != 1 {
		t.Fatalf("rules stats = %#v, want one consumer hit", stats)
	}
}

func TestGovernanceBrokerRateLimitRejectsBeforePublish(t *testing.T) {
	var calls atomic.Int64
	fake := &governanceFakeBroker{publish: func(ctx context.Context, msg Message) error {
		calls.Add(1)
		return nil
	}}
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportMQ,
		Service:   "ordersvc",
		Method:    "PUBLISH",
		Path:      "/orders",
		Policy: governance.Policy{
			RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1},
		},
	})
	broker, err := NewGovernanceBroker(fake, WithGovernanceService("ordersvc"), WithGovernanceRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), Message{Topic: "orders"}); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), Message{Topic: "orders"}); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("second publish error = %v, want local rate-limit error", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("publish calls = %d, want 1", calls.Load())
	}
}

func TestGovernanceBrokerUsesManagerMQRules(t *testing.T) {
	var calls atomic.Int64
	fake := &governanceFakeBroker{publish: func(ctx context.Context, msg Message) error {
		calls.Add(1)
		if msg.Headers["x-source"] != "manager" {
			t.Fatalf("publish headers = %#v, want manager metadata", msg.Headers)
		}
		return nil
	}}
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "mq-manager",
		Transport: governance.TransportMQ,
		Service:   "ordersvc",
		Method:    "PUBLISH",
		Path:      "/orders",
		Policy: governance.Policy{
			Metadata: map[string]string{"x-source": "manager"},
		},
	}}})
	if err != nil {
		t.Fatalf("NewManager mq rule: %v", err)
	}
	broker, err := NewGovernanceBroker(fake, WithGovernanceService("ordersvc"), WithGovernanceRuleSet(manager.RuleSet()))
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), Message{Topic: "orders"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("publish calls = %d, want 1", calls.Load())
	}
	if stats := manager.RuleSet().Stats(); len(stats) != 1 || stats[0].RuleName != "mq-manager" || stats[0].Hits != 1 {
		t.Fatalf("manager mq stats = %#v, want one hit", stats)
	}
}

func waitFor(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}

type governanceFakeBroker struct {
	publish func(context.Context, Message) error
	handler Handler
}

func (b *governanceFakeBroker) Publish(ctx context.Context, msg Message) error {
	if b.publish != nil {
		return b.publish(ctx, msg)
	}
	return nil
}

func (b *governanceFakeBroker) Subscribe(ctx context.Context, topic, group string, handler Handler, opts ...SubscribeOption) (Unsubscriber, error) {
	b.handler = handler
	return noopUnsubscriber{}, nil
}

func (b *governanceFakeBroker) Close(ctx context.Context) error { return nil }

type noopUnsubscriber struct{}

func (noopUnsubscriber) Stop(context.Context) error { return nil }

func TestSubscriptionConfigOptionFunctions(t *testing.T) {
	cfg := newSubscriptionConfig("orders",
		WithBuffer(128),
		WithConcurrency(4),
		WithMaxAttempts(5),
		WithRetryBackoff(100*time.Millisecond),
		WithDeadLetterTopic("orders.dlq"),
	)
	if cfg.Buffer() != 128 {
		t.Fatalf("Buffer = %d, want 128", cfg.Buffer())
	}
	if cfg.Concurrency() != 4 {
		t.Fatalf("Concurrency = %d, want 4", cfg.Concurrency())
	}
	if cfg.MaxAttempts() != 5 {
		t.Fatalf("MaxAttempts = %d, want 5", cfg.MaxAttempts())
	}
	if cfg.RetryBackoff() != 100*time.Millisecond {
		t.Fatalf("RetryBackoff = %v, want 100ms", cfg.RetryBackoff())
	}
	if cfg.DeadLetterTopic() != "orders.dlq" {
		t.Fatalf("DeadLetterTopic = %s, want orders.dlq", cfg.DeadLetterTopic())
	}
}

func TestSubscriptionConfigDefaultsAndNilOption(t *testing.T) {
	cfg := newSubscriptionConfig("tasks", nil, nil)
	if cfg.Buffer() != 64 {
		t.Fatalf("default Buffer = %d, want 64", cfg.Buffer())
	}
	if cfg.Concurrency() != 1 {
		t.Fatalf("default Concurrency = %d, want 1", cfg.Concurrency())
	}
	if cfg.MaxAttempts() != 3 {
		t.Fatalf("default MaxAttempts = %d, want 3", cfg.MaxAttempts())
	}
	if cfg.DeadLetterTopic() != "tasks.dlq" {
		t.Fatalf("default DeadLetterTopic = %s, want tasks.dlq", cfg.DeadLetterTopic())
	}
}

func TestSubscriptionConfigZeroValuesIgnored(t *testing.T) {
	cfg := newSubscriptionConfig("orders",
		WithBuffer(0),
		WithConcurrency(0),
		WithMaxAttempts(0),
		WithRetryBackoff(-1*time.Second),
	)
	if cfg.Buffer() != 64 || cfg.Concurrency() != 1 || cfg.MaxAttempts() != 3 {
		t.Fatalf("zero values should be ignored: %+v", cfg)
	}
}

func TestBuildSubscriptionConfig(t *testing.T) {
	cfg := BuildSubscriptionConfig("events", WithBuffer(32))
	if cfg.Buffer() != 32 {
		t.Fatalf("BuildSubscriptionConfig Buffer = %d, want 32", cfg.Buffer())
	}
}

func TestAsBrokerAndSubscribeBroker(t *testing.T) {
	mb := NewMemoryBroker()
	b := AsBroker(mb)
	if b == nil {
		t.Fatal("AsBroker returned nil")
	}
	received := make(chan Message, 1)
	unsub, err := mb.SubscribeBroker(context.Background(), "topics", "g1", func(ctx context.Context, msg Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeBroker: %v", err)
	}
	defer unsub.Stop(context.Background())
	if err := b.Publish(context.Background(), Message{Topic: "topics", Body: []byte("hello")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case msg := <-received:
		if string(msg.Body) != "hello" {
			t.Fatalf("unexpected message body: %s", msg.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestGovernanceBrokerSnapshotNilSafety(t *testing.T) {
	var nilBroker *GovernanceBroker
	stats := nilBroker.Snapshot()
	if stats.Published != 0 || stats.Delivered != 0 || len(stats.Topics) != 0 {
		t.Fatalf("nil GovernanceBroker.Snapshot = %+v, want zero", stats)
	}

	broker, err := NewGovernanceBroker(AsBroker(NewMemoryBroker()))
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), Message{Topic: "orders", Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	snapshot := broker.Snapshot()
	if snapshot.Published != 1 {
		t.Fatalf("snapshot.Published = %d, want 1", snapshot.Published)
	}
}

func TestGovernanceBrokerCloseDelegates(t *testing.T) {
	mb := NewMemoryBroker()
	broker, err := NewGovernanceBroker(AsBroker(mb))
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := mb.Publish(context.Background(), Message{Topic: "orders"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("underlying broker not closed: %v", err)
	}
}

func TestGovernanceOptionFunctions(t *testing.T) {
	o := governanceOptions{service: "mq", registry: metrics.Default, runtime: newMQRuleRuntime()}

	WithGovernanceLog(true)(&o)
	if !o.log {
		t.Fatal("WithGovernanceLog did not set log")
	}

	WithGovernanceLogSampler(trace.NeverSampler())(&o)
	if o.logSampler == nil {
		t.Fatal("WithGovernanceLogSampler did not set logSampler")
	}

	WithGovernanceTimeout(5 * time.Second)(&o)
	if o.timeout != 5*time.Second {
		t.Fatalf("WithGovernanceTimeout = %v, want 5s", o.timeout)
	}

	WithGovernanceTimeout(0)(&o)
	if o.timeout != 5*time.Second {
		t.Fatalf("WithGovernanceTimeout(0) should not overwrite: %v", o.timeout)
	}

	policy := retry.Policy{Attempts: 3, Backoff: 10 * time.Millisecond}
	WithGovernanceRetryPolicy(policy)(&o)
	if o.retryPolicy.Attempts != 3 {
		t.Fatalf("WithGovernanceRetryPolicy did not set retryPolicy")
	}

	brk := breaker.NewAdaptive()
	WithGovernanceBreaker(brk)(&o)
	if o.breaker != brk {
		t.Fatal("WithGovernanceBreaker did not set breaker")
	}
}

func TestMemoryBrokerAdapterSubscribe(t *testing.T) {
	mb := NewMemoryBroker()
	adapter := AsBroker(mb)
	received := make(chan Message, 1)
	unsub, err := adapter.Subscribe(context.Background(), "topics", "g1", func(ctx context.Context, msg Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("adapter.Subscribe: %v", err)
	}
	defer unsub.Stop(context.Background())
	if err := adapter.Publish(context.Background(), Message{Topic: "topics", Body: []byte("via-adapter")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case msg := <-received:
		if string(msg.Body) != "via-adapter" {
			t.Fatalf("unexpected body: %s", msg.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestMQAdaptiveBreakerFromPolicy(t *testing.T) {
	brk := mqAdaptiveBreakerFromPolicy(governance.BreakerPolicy{
		Enabled:      true,
		OpenTimeout:  100 * time.Millisecond,
		Window:       time.Second,
		Buckets:      10,
		MinRequests:  5,
		FailureRatio: 0.5,
	})
	if brk == nil {
		t.Fatal("mqAdaptiveBreakerFromPolicy returned nil")
	}

	// Minimal policy should still produce a breaker
	brkMinimal := mqAdaptiveBreakerFromPolicy(governance.BreakerPolicy{Enabled: true})
	if brkMinimal == nil {
		t.Fatal("mqAdaptiveBreakerFromPolicy with minimal policy returned nil")
	}
}

func BenchmarkMemoryBrokerPublish(b *testing.B) {
	broker := NewMemoryBroker()
	ctx := context.Background()
	msg := Message{Topic: "orders", Body: []byte("payload")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = broker.Publish(ctx, msg)
	}
}

func BenchmarkMemoryBrokerSubscribeAndPublish(b *testing.B) {
	broker := NewMemoryBroker()
	ctx := context.Background()
	msg := Message{Topic: "orders", Body: []byte("payload")}

	sub, err := broker.Subscribe(ctx, "orders", "workers", func(_ context.Context, m Message) error {
		return nil
	})
	if err != nil {
		b.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop(context.Background())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = broker.Publish(ctx, msg)
	}
}

func BenchmarkMemoryBrokerPublishParallel(b *testing.B) {
	broker := NewMemoryBroker()
	ctx := context.Background()
	msg := Message{Topic: "orders", Body: []byte("payload")}

	sub, err := broker.Subscribe(ctx, "orders", "workers", func(_ context.Context, m Message) error {
		return nil
	})
	if err != nil {
		b.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop(context.Background())

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = broker.Publish(ctx, msg)
		}
	})
}
