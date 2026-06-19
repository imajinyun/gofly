package redisstream

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofly/gofly/core/kv/redis"
	"github.com/gofly/gofly/core/mq"
	"github.com/gofly/gofly/core/observability/trace"
)

func TestNewValidationAndDefaults(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil || !errors.Is(err, errors.New("redisstream: client is nil")) {
		if err == nil || err.Error() != "redisstream: client is nil" {
			t.Fatalf("New(nil) error = %v, want client is nil", err)
		}
	}
	broker, err := New(&fakeRedisStreamClient{}, Options{})
	if err != nil {
		t.Fatalf("New valid error = %v", err)
	}
	if broker.opts.BlockInterval != 2*time.Second || broker.opts.ReadCount != 16 {
		t.Fatalf("defaults = block %s count %d, want 2s/16", broker.opts.BlockInterval, broker.opts.ReadCount)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	published := time.Unix(123, 456)
	msg := mq.Message{ID: "id-1", Key: "user-1", Body: []byte("hello"), Attempts: 2, PublishedAt: published, Headers: map[string]string{"trace": "abc"}}
	fields := encode(msg)
	got := decode(redis.StreamEntry{ID: "entry-1", Fields: fields})
	if got.ID != msg.ID || got.Key != msg.Key || string(got.Body) != string(msg.Body) || got.Attempts != msg.Attempts || !got.PublishedAt.Equal(published) {
		t.Fatalf("decode = %#v, want %#v", got, msg)
	}
	if !reflect.DeepEqual(got.Headers, msg.Headers) {
		t.Fatalf("headers = %#v, want %#v", got.Headers, msg.Headers)
	}

	fallback := decode(redis.StreamEntry{ID: "entry-2", Fields: map[string]string{fieldBody: "payload"}})
	if fallback.ID != "entry-2" {
		t.Fatalf("fallback ID = %q, want entry-2", fallback.ID)
	}
}

func TestEncodeDecodeBoundaryFields_BitsUT(t *testing.T) {
	fields := encode(mq.Message{ID: "id-1", Body: []byte("payload")})
	if fields[fieldID] != "id-1" || fields[fieldBody] != "payload" || fields[fieldAttempts] != "0" {
		t.Fatalf("encoded fields = %#v, want required envelope fields", fields)
	}
	if _, ok := fields[fieldKey]; ok {
		t.Fatalf("encoded fields = %#v, want empty key omitted", fields)
	}
	if _, ok := fields[fieldPublishedAt]; ok {
		t.Fatalf("encoded fields = %#v, want zero timestamp omitted", fields)
	}
	for key := range fields {
		if strings.HasPrefix(key, headerPrefix) {
			t.Fatalf("encoded fields = %#v, want no user headers for nil Headers", fields)
		}
	}

	got := decode(redis.StreamEntry{ID: "1-0", Fields: map[string]string{
		fieldAttempts:    "not-an-int",
		fieldPublishedAt: "not-a-nano",
		fieldBody:        "payload",
		"h:trace":        "abc",
		"h:":             "empty-name",
		"other":          "ignored",
	}})
	if got.ID != "1-0" || string(got.Body) != "payload" {
		t.Fatalf("decoded malformed fields = %#v, want fallback id and body", got)
	}
	if got.Attempts != 0 || !got.PublishedAt.IsZero() {
		t.Fatalf("decoded malformed fields attempts/time = %d/%s, want zero values", got.Attempts, got.PublishedAt)
	}
	if got.Headers["trace"] != "abc" {
		t.Fatalf("decoded headers = %#v, want trace header", got.Headers)
	}
	if _, ok := got.Headers[""]; ok {
		t.Fatalf("decoded headers = %#v, want malformed h: ignored", got.Headers)
	}
}

func TestRedisStreamTraceRoundTrip(t *testing.T) {
	sc := trace.SpanContext{TraceID: "abc12300000000000000000000000000", SpanID: "def4560000000000", Sampled: true}
	ctx := trace.NewContext(context.Background(), sc)
	msg := mq.Message{ID: "id-1", Key: "k", Body: []byte("body")}
	mq.InjectTrace(ctx, &msg)

	fields := encode(msg)
	got := decode(redis.StreamEntry{ID: "entry-1", Fields: fields})
	if got.Headers["traceparent"] != trace.TraceParent(sc) {
		t.Fatalf("trace header = %q, want %q", got.Headers["traceparent"], trace.TraceParent(sc))
	}

	extractedCtx := mq.ExtractTrace(context.Background(), got)
	extractedSC, ok := trace.FromContext(extractedCtx)
	if !ok {
		t.Fatal("expected trace context to be extracted")
	}
	if extractedSC.TraceID != sc.TraceID {
		t.Fatalf("extracted traceID = %q, want %q", extractedSC.TraceID, sc.TraceID)
	}
}

func TestPublishValidationClosedAndXAdd(t *testing.T) {
	client := &fakeRedisStreamClient{}
	broker, err := New(client, Options{MaxLen: 128})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), mq.Message{}); !errors.Is(err, mq.ErrInvalidTopic) {
		t.Fatalf("Publish empty topic error = %v, want ErrInvalidTopic", err)
	}
	msg := mq.Message{Topic: "orders", ID: "id-1", Body: []byte("body")}
	if err := broker.Publish(context.Background(), msg); err != nil {
		t.Fatalf("Publish valid error = %v", err)
	}
	if client.xaddStream != "orders" || client.xaddMaxLen != 128 || client.xaddFields[fieldID] != "id-1" || client.xaddFields[fieldBody] != "body" {
		t.Fatalf("XAdd call = stream %q maxLen %d fields %#v", client.xaddStream, client.xaddMaxLen, client.xaddFields)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := broker.Publish(context.Background(), msg); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Publish after close error = %v, want ErrClosed", err)
	}
}

func TestPublishNilContextAndXAddError_BitsUT(t *testing.T) {
	client := &fakeRedisStreamClient{}
	broker, err := New(client, Options{MaxLen: 256})
	if err != nil {
		t.Fatal(err)
	}
	var nilCtx context.Context
	if err := broker.Publish(nilCtx, mq.Message{Topic: "orders", ID: "id-1"}); err != nil {
		t.Fatalf("Publish nil ctx error = %v", err)
	}
	if client.xaddSawNilCtx {
		t.Fatal("Publish passed nil context to XAdd")
	}
	if client.xaddStream != "orders" || client.xaddMaxLen != 256 || client.xaddFields[fieldID] != "id-1" {
		t.Fatalf("XAdd call = stream %q maxLen %d fields %#v", client.xaddStream, client.xaddMaxLen, client.xaddFields)
	}

	wantErr := errors.New("xadd failed")
	client.xaddErr = wantErr
	if err := broker.Publish(context.Background(), mq.Message{Topic: "orders", ID: "id-2"}); !errors.Is(err, wantErr) {
		t.Fatalf("Publish XAdd error = %v, want %v", err, wantErr)
	}
}

func TestSubscribeValidationAndGroupCreate(t *testing.T) {
	client := &fakeRedisStreamClient{}
	broker, err := New(client, Options{Consumer: "consumer-1", ReadCount: 1, BlockInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Subscribe(context.Background(), "", "g", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrInvalidTopic) {
		t.Fatalf("Subscribe empty topic error = %v, want ErrInvalidTopic", err)
	}
	if _, err := broker.Subscribe(context.Background(), "orders", "", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrInvalidGroup) {
		t.Fatalf("Subscribe empty group error = %v, want ErrInvalidGroup", err)
	}
	if _, err := broker.Subscribe(context.Background(), "orders", "g", nil); err == nil || err.Error() != "redisstream: handler is nil" {
		t.Fatalf("Subscribe nil handler error = %v, want handler error", err)
	}

	sub, err := broker.Subscribe(context.Background(), "orders", "g", func(context.Context, mq.Message) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe valid error = %v", err)
	}
	if client.groupStream != "orders" || client.groupName != "g" || client.groupStart != "$" || !client.groupMkStream {
		t.Fatalf("XGroupCreate call = %q/%q/%q/%v", client.groupStream, client.groupName, client.groupStart, client.groupMkStream)
	}
	if rsSub, ok := sub.(*subscription); !ok || rsSub.topic != "orders" || rsSub.group != "g" || rsSub.consumer != "consumer-1" || rsSub.handler == nil {
		t.Fatalf("subscription = %#v, want configured redis stream subscription", sub)
	}
	if len(broker.subs) != 1 {
		t.Fatalf("broker subs = %d, want 1", len(broker.subs))
	}
	if err := sub.Stop(context.Background()); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
}

func TestSubscribeClosedGeneratedConsumerAndGroupError_BitsUT(t *testing.T) {
	closedClient := &fakeRedisStreamClient{}
	closedBroker, err := New(closedClient, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := closedBroker.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if _, err := closedBroker.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Subscribe closed error = %v, want ErrClosed", err)
	}
	if closedClient.groupStream != "" || closedClient.groupName != "" {
		t.Fatalf("closed Subscribe created group %q/%q, want no call", closedClient.groupStream, closedClient.groupName)
	}

	wantErr := errors.New("BUSYGROUP")
	groupClient := &fakeRedisStreamClient{groupErr: wantErr}
	groupBroker, err := New(groupClient, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := groupBroker.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "redisstream: create group") {
		t.Fatalf("Subscribe group error = %v, want wrapped BUSYGROUP", err)
	}

	generatedClient := &fakeRedisStreamClient{}
	generatedBroker, err := New(generatedClient, Options{BlockInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := generatedBroker.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe generated consumer error = %v", err)
	}
	rsSub := sub.(*subscription)
	if !strings.HasPrefix(rsSub.consumer, "workers-") || rsSub.consumer == "workers-" {
		t.Fatalf("generated consumer = %q, want workers-*", rsSub.consumer)
	}
	if err := sub.Stop(context.Background()); err != nil {
		t.Fatalf("Stop generated subscription error = %v", err)
	}
}

func TestSubscriptionProcessRetriesDeadLettersAndAcks(t *testing.T) {
	client := &fakeRedisStreamClient{}
	broker, err := New(client, Options{MaxLen: 64})
	if err != nil {
		t.Fatal(err)
	}
	var attempts int
	sub := &subscription{
		broker:  broker,
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(2), mq.WithRetryBackoff(0), mq.WithDeadLetterTopic("orders.dlq")),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { attempts++; return errors.New("boom") },
	}

	sub.process(redis.StreamEntry{ID: "1-0", Fields: map[string]string{fieldID: "msg-1", fieldBody: "payload"}})

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if client.xaddStream != "orders.dlq" || client.xaddMaxLen != 64 || client.xaddFields[fieldID] != "msg-1" || client.xaddFields[fieldAttempts] != "2" {
		t.Fatalf("DLQ XAdd = stream %q maxLen %d fields %#v", client.xaddStream, client.xaddMaxLen, client.xaddFields)
	}
	if client.xackStream != "orders" || client.xackGroup != "workers" || !reflect.DeepEqual(client.xackIDs, []string{"1-0"}) {
		t.Fatalf("XAck = stream %q group %q ids %#v", client.xackStream, client.xackGroup, client.xackIDs)
	}
}

func TestSubscriptionProcessSkipsAckWhenDeadLetterFails(t *testing.T) {
	client := &fakeRedisStreamClient{xaddErr: errors.New("dlq unavailable")}
	broker, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sub := &subscription{
		broker:  broker,
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1), mq.WithDeadLetterTopic("orders.dlq")),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return errors.New("boom") },
	}

	sub.process(redis.StreamEntry{ID: "1-1", Fields: map[string]string{fieldBody: "payload"}})

	if client.xackCalls != 0 {
		t.Fatalf("XAck calls = %d, want 0 when DLQ publish fails", client.xackCalls)
	}
}

func TestSubscriptionProcessSuccessAndCancellationBoundaries_BitsUT(t *testing.T) {
	client := &fakeRedisStreamClient{xackErr: errors.New("ack failed")}
	broker, err := New(client, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var seen mq.Message
	sub := &subscription{
		broker: broker,
		topic:  "orders",
		group:  "workers",
		cfg:    mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(0)),
		ctx:    context.Background(),
		handler: func(_ context.Context, msg mq.Message) error {
			seen = msg
			return nil
		},
	}
	sub.process(redis.StreamEntry{ID: "1-0", Fields: map[string]string{fieldID: "msg-1", fieldBody: "payload"}})
	if seen.Topic != "orders" || seen.ID != "msg-1" || seen.Attempts != 1 || string(seen.Body) != "payload" {
		t.Fatalf("handler saw %#v, want decoded message with attempts floor", seen)
	}
	if client.xackCalls != 1 || client.xackStream != "orders" || client.xackGroup != "workers" || !reflect.DeepEqual(client.xackIDs, []string{"1-0"}) {
		t.Fatalf("XAck = calls %d stream %q group %q ids %#v", client.xackCalls, client.xackStream, client.xackGroup, client.xackIDs)
	}
	if client.xaddStream != "" {
		t.Fatalf("success path XAdd stream = %q, want no DLQ", client.xaddStream)
	}

	canceledClient := &fakeRedisStreamClient{}
	canceledBroker, err := New(canceledClient, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceledSub := &subscription{
		broker: canceledBroker,
		topic:  "orders",
		group:  "workers",
		cfg:    mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(3), mq.WithDeadLetterTopic("orders.dlq")),
		ctx:    ctx,
		handler: func(context.Context, mq.Message) error {
			cancel()
			return errors.New("stopping")
		},
	}
	canceledSub.process(redis.StreamEntry{ID: "1-1", Fields: map[string]string{fieldBody: "payload"}})
	if canceledClient.xackCalls != 0 || canceledClient.xaddStream != "" {
		t.Fatalf("canceled process xack=%d xadd=%q, want no ack or DLQ", canceledClient.xackCalls, canceledClient.xaddStream)
	}
}

type fakeRedisStreamClient struct {
	mu sync.Mutex

	xaddStream    string
	xaddMaxLen    int64
	xaddFields    map[string]string
	xaddErr       error
	xaddSawNilCtx bool

	groupStream   string
	groupName     string
	groupStart    string
	groupMkStream bool
	groupErr      error

	xackCalls  int
	xackStream string
	xackGroup  string
	xackIDs    []string
	xackErr    error
}

func (f *fakeRedisStreamClient) XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.xaddSawNilCtx = ctx == nil
	f.xaddStream = stream
	f.xaddMaxLen = maxLen
	f.xaddFields = fields
	if f.xaddErr != nil {
		return "", f.xaddErr
	}
	return "1-0", nil
}

func (f *fakeRedisStreamClient) XGroupCreate(ctx context.Context, stream, group, start string, mkStream bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupStream = stream
	f.groupName = group
	f.groupStart = start
	f.groupMkStream = mkStream
	if f.groupErr != nil {
		return f.groupErr
	}
	return nil
}

func (f *fakeRedisStreamClient) XReadGroup(ctx context.Context, group, consumer, stream string, count int, block time.Duration) ([]redis.StreamEntry, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSleepContextBoundaries(t *testing.T) {
	if !sleepContext(context.Background(), 0) {
		t.Fatal("sleepContext zero = false, want true")
	}
	if !sleepContext(context.Background(), -time.Second) {
		t.Fatal("sleepContext negative = false, want true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepContext(ctx, time.Hour) {
		t.Fatal("sleepContext canceled = true, want false")
	}
	if !sleepContext(context.Background(), time.Nanosecond) {
		t.Fatal("sleepContext elapsed = false, want true")
	}
}

func TestSubscriptionStopNilContext(t *testing.T) {
	sub := &subscription{done: make(chan struct{})}
	sub.once.Do(func() {})
	close(sub.done)
	var nilCtx context.Context
	if err := sub.Stop(nilCtx); err != nil {
		t.Fatalf("Stop nil ctx error = %v, want nil", err)
	}
}

func TestSubscriptionStopCallerContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sub := &subscription{done: make(chan struct{})}
	sub.once.Do(func() {})
	if err := sub.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop canceled ctx error = %v, want context.Canceled", err)
	}
}

func BenchmarkEncode(b *testing.B) {
	msg := mq.Message{ID: "id-1", Key: "k", Body: []byte("body"), Headers: map[string]string{"trace": "abc"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = encode(msg)
	}
}

func BenchmarkDecode(b *testing.B) {
	entry := redis.StreamEntry{ID: "1-0", Fields: map[string]string{fieldID: "id-1", fieldKey: "k", fieldBody: "body", "h-trace": "abc"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = decode(entry)
	}
}

func (f *fakeRedisStreamClient) XAck(ctx context.Context, stream, group string, ids ...string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.xackCalls++
	f.xackStream = stream
	f.xackGroup = group
	f.xackIDs = append([]string(nil), ids...)
	if f.xackErr != nil {
		return 0, f.xackErr
	}
	return int64(len(ids)), nil
}
