package nacossource

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/gofly/gofly/core/config"
)

func TestConfigGroupDefault(t *testing.T) {
	if got := (Config{}).group(); got != "DEFAULT_GROUP" {
		t.Fatalf("default group = %q, want DEFAULT_GROUP", got)
	}
	if got := (Config{Group: "prod"}).group(); got != "prod" {
		t.Fatalf("custom group = %q, want prod", got)
	}
}

func TestNewValidation(t *testing.T) {
	_, err := New(Config{})
	if err == nil || !strings.Contains(err.Error(), "server") {
		t.Fatalf("New without servers error = %v, want server error", err)
	}
	_, err = New(Config{Servers: []ServerConfig{{IPAddr: "127.0.0.1", Port: 8848}}})
	if err == nil || !strings.Contains(err.Error(), "dataId is required") {
		t.Fatalf("New without dataId error = %v, want dataId required", err)
	}
}

func TestNewConstructsSourceWithClientConfig(t *testing.T) {
	src, err := New(Config{
		Servers: []ServerConfig{{IPAddr: "127.0.0.1", Port: 8848}},
		Group:   "prod",
		DataID:  "cfg/app.yaml",
	})
	if err != nil {
		t.Fatalf("New valid config error = %v", err)
	}
	if src == nil || src.client == nil {
		t.Fatalf("New valid config source = %#v, want source with client", src)
	}
	if src.group != "prod" || src.dataID != "cfg/app.yaml" {
		t.Fatalf("source group/dataID = %q/%q, want prod/cfg/app.yaml", src.group, src.dataID)
	}
}

func TestNewWithClientValidationAndDefaults(t *testing.T) {
	if _, err := NewWithClient(nil, "", "cfg"); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("NewWithClient(nil) error = %v, want client is nil", err)
	}
	client := &fakeConfigClient{content: "{\"ok\":true}"}
	if _, err := NewWithClient(client, "", ""); err == nil || !strings.Contains(err.Error(), "dataId is required") {
		t.Fatalf("NewWithClient(empty dataID) error = %v, want dataId required", err)
	}
	src, err := NewWithClient(client, "", "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient valid error = %v", err)
	}
	if src.group != "DEFAULT_GROUP" || src.dataID != "cfg/app" {
		t.Fatalf("source group/dataID = %q/%q, want DEFAULT_GROUP/cfg/app", src.group, src.dataID)
	}
}

func TestGetAndWatchWithFakeClient(t *testing.T) {
	client := &fakeConfigClient{content: "{\"version\":1}", listenReady: make(chan struct{})}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}

	got, err := src.Get(context.Background())
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.Key != "cfg" || string(got.Data) != client.content {
		t.Fatalf("Get = %#v, want key cfg and fake content", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes := make(chan config.RemoteValue, 1)
	done := make(chan error, 1)
	go func() { done <- src.Watch(ctx, func(v config.RemoteValue) { changes <- v }) }()

	select {
	case <-client.listenReady:
	case <-time.After(time.Second):
		t.Fatal("listener was not registered")
	}
	client.mu.Lock()
	onChange := client.onChange
	client.mu.Unlock()
	onChange("", "app", "cfg", "{\"version\":2}")
	select {
	case v := <-changes:
		if v.Key != "cfg" || string(v.Data) != "{\"version\":2}" {
			t.Fatalf("change = %#v", v)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for change")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch error = %v, want context.Canceled", err)
	}
	client.mu.Lock()
	cancelled := client.cancelled
	client.mu.Unlock()
	if !cancelled {
		t.Fatal("CancelListenConfig was not called")
	}
}

func TestSourceClose(t *testing.T) {
	client := &fakeConfigClient{content: "{}"}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestSourceGetNilContext(t *testing.T) {
	client := &fakeConfigClient{content: "{\"ok\":true}"}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	var nilCtx context.Context
	got, err := src.Get(nilCtx)
	if err != nil {
		t.Fatalf("Get(nil) error: %v", err)
	}
	if got.Key != "cfg" {
		t.Fatalf("key = %q, want cfg", got.Key)
	}
}

func TestWatchListenConfigError(t *testing.T) {
	client := &fakeConfigClient{listenErr: errors.New("listen failed")}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Watch(context.Background(), func(v config.RemoteValue) {}); err == nil || !strings.Contains(err.Error(), "listen failed") {
		t.Fatalf("Watch error = %v, want listen failed", err)
	}
}

func TestSourceGetContextCanceled(t *testing.T) {
	client := &fakeConfigClient{content: "{\"ok\":true}"}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = src.Get(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Get canceled error = %v, want context.Canceled", err)
	}
}

func TestSourceGetClientError(t *testing.T) {
	client := &fakeConfigClient{getErr: errors.New("get failed")}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	_, err = src.Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get failed") {
		t.Fatalf("Get error = %v, want get failed", err)
	}
}

func TestWatchOnChangeNil(t *testing.T) {
	client := &fakeConfigClient{listenReady: make(chan struct{})}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- src.Watch(ctx, nil) }()

	select {
	case <-client.listenReady:
	case <-time.After(time.Second):
		t.Fatal("listener was not registered")
	}
	client.mu.Lock()
	onChange := client.onChange
	client.mu.Unlock()
	onChange("", "app", "cfg", "data")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch error = %v, want context.Canceled", err)
	}
}

func TestWatchCancelListenErrorStillReturnsContextError(t *testing.T) {
	client := &fakeConfigClient{listenReady: make(chan struct{}), cancelErr: errors.New("cancel failed")}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- src.Watch(ctx, func(v config.RemoteValue) {}) }()

	select {
	case <-client.listenReady:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("listener was not registered")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch error = %v, want context.Canceled despite cancel-listen failure", err)
	}
	client.mu.Lock()
	cancelled := client.cancelled
	client.mu.Unlock()
	if !cancelled {
		t.Fatal("CancelListenConfig was not called")
	}
}

func TestWatchNilContextListenError(t *testing.T) {
	client := &fakeConfigClient{listenErr: errors.New("listen failed")}
	src, err := NewWithClient(client, "app", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // exercises the public nil-context compatibility branch.
	if err := src.Watch(nil, func(v config.RemoteValue) {}); err == nil || !strings.Contains(err.Error(), "listen failed") {
		t.Fatalf("Watch error = %v, want listen failed", err)
	}
}

type fakeConfigClient struct {
	mu          sync.Mutex
	listenOnce  sync.Once
	listenReady chan struct{}
	content     string
	onChange    func(namespace, group, dataID, data string)
	cancelled   bool
	listenErr   error
	cancelErr   error
	getErr      error
}

func (f *fakeConfigClient) GetConfig(param vo.ConfigParam) (string, error) {
	return f.content, f.getErr
}

func (f *fakeConfigClient) PublishConfig(param vo.ConfigParam) (bool, error) { return false, nil }
func (f *fakeConfigClient) DeleteConfig(param vo.ConfigParam) (bool, error)  { return false, nil }

func (f *fakeConfigClient) ListenConfig(param vo.ConfigParam) error {
	f.mu.Lock()
	f.onChange = param.OnChange
	f.mu.Unlock()
	if f.listenReady != nil {
		f.listenOnce.Do(func() { close(f.listenReady) })
	}
	return f.listenErr
}

func (f *fakeConfigClient) CancelListenConfig(param vo.ConfigParam) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = true
	return f.cancelErr
}

func (f *fakeConfigClient) SearchConfig(param vo.SearchConfigParam) (*model.ConfigPage, error) {
	return nil, nil
}

func (f *fakeConfigClient) CloseClient() {}
