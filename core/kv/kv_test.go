package kv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMemoryStoreSetGetDeleteAndSnapshot(t *testing.T) {
	store := NewMemoryStore()
	value := []byte("value")
	if err := store.Set(context.Background(), "key", value, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	value[0] = 'X'
	got, err := store.Get(context.Background(), "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "value" {
		t.Fatalf("value = %q, want defensive copy", got)
	}
	got[0] = 'Y'
	again, err := store.Get(context.Background(), "key")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if string(again) != "value" {
		t.Fatalf("stored value mutated through returned slice: %q", again)
	}
	exists, err := store.Exists(context.Background(), "key")
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v; want true", exists, err)
	}
	deleted, err := store.Delete(context.Background(), "key")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v; want true", deleted, err)
	}
	if _, err := store.Get(context.Background(), "key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrNotFound", err)
	}
	if stats := store.Snapshot(); stats.Sets != 1 || stats.Hits != 2 || stats.Misses != 1 || stats.Deletes != 1 || stats.Entries != 0 {
		t.Fatalf("stats = %+v, want set/hit/miss/delete counters", stats)
	}
}

func TestMemoryStoreTTLAndSetNX(t *testing.T) {
	store := NewMemoryStore()
	created, err := store.SetNX(context.Background(), "once", []byte("first"), 20*time.Millisecond)
	if err != nil || !created {
		t.Fatalf("SetNX first = %v, %v; want true", created, err)
	}
	created, err = store.SetNX(context.Background(), "once", []byte("second"), 0)
	if err != nil || created {
		t.Fatalf("SetNX existing = %v, %v; want false", created, err)
	}
	ttl, err := store.TTL(context.Background(), "once")
	if err != nil || ttl <= 0 {
		t.Fatalf("TTL = %v, %v; want positive ttl", ttl, err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := store.Get(context.Background(), "once"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get expired error = %v, want ErrNotFound", err)
	}
	created, err = store.SetNX(context.Background(), "once", []byte("second"), 0)
	if err != nil || !created {
		t.Fatalf("SetNX after expiry = %v, %v; want true", created, err)
	}
	ttl, err = store.TTL(context.Background(), "once")
	if err != nil || ttl != 0 {
		t.Fatalf("TTL no expiry = %v, %v; want zero ttl", ttl, err)
	}
}

func TestMemoryStoreContextAndClose(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Set(ctx, "key", []byte("value"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set canceled error = %v, want context.Canceled", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := store.Get(context.Background(), "key"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get closed error = %v, want ErrClosed", err)
	}
}

func TestRedisStoreDelegates(t *testing.T) {
	client := &fakeRedisClient{store: NewMemoryStore()}
	store := NewRedisStore(client)
	if err := store.Set(context.Background(), "key", []byte("value"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(context.Background(), "key")
	if err != nil || string(got) != "value" {
		t.Fatalf("Get = %q, %v; want value", got, err)
	}
	created, err := store.SetNX(context.Background(), "key", []byte("next"), 0)
	if err != nil || created {
		t.Fatalf("SetNX existing = %v, %v; want false", created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !client.closed {
		t.Fatal("redis client should be closed")
	}
}

func TestRedisStoreNilGuards(t *testing.T) {
	var nilStore *RedisStore
	ctx := context.Background()
	if _, err := nilStore.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Get = %v, want ErrClosed", err)
	}
	if _, err := nilStore.SetNX(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil SetNX = %v, want ErrClosed", err)
	}
	if err := nilStore.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Set = %v, want ErrClosed", err)
	}
	if _, err := nilStore.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Delete = %v, want ErrClosed", err)
	}
	if _, err := nilStore.Exists(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Exists = %v, want ErrClosed", err)
	}
	if _, err := nilStore.TTL(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil TTL = %v, want ErrClosed", err)
	}
	if err := nilStore.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}

	empty := NewRedisStore(nil)
	if _, err := empty.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("empty Get = %v, want ErrClosed", err)
	}
	if err := empty.Close(); err != nil {
		t.Errorf("empty Close = %v, want nil", err)
	}
}

func TestMemoryStoreCleanupExpired(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Set(context.Background(), "alive", []byte("a"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "expired", []byte("e"), 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	// Trigger cleanup via Get on an existing key.
	if _, err := store.Get(context.Background(), "alive"); err != nil {
		t.Fatalf("Get alive: %v", err)
	}
	if _, err := store.Get(context.Background(), "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get expired = %v, want ErrNotFound", err)
	}
	stats := store.Snapshot()
	if stats.Expired != 1 {
		t.Fatalf("Expired = %d, want 1", stats.Expired)
	}
}

func TestMemoryStoreDeleteNotFound(t *testing.T) {
	store := NewMemoryStore()
	deleted, err := store.Delete(context.Background(), "missing")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted {
		t.Fatal("expected deleted=false for missing key")
	}
}

func TestMemoryStoreExistsContextCanceled(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Exists(ctx, "key")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Exists canceled error = %v, want context.Canceled", err)
	}
}

func TestMemoryStoreCleanupLockedNoExpired(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Set(context.Background(), "alive", []byte("a"), time.Hour); err != nil {
		t.Fatal(err)
	}
	stats := store.Snapshot()
	if stats.Expired != 0 || stats.Entries != 1 {
		t.Fatalf("stats = %+v, want 0 expired 1 entry", stats)
	}
}

func TestEtcdStoreExistsAndTTLAndClose(t *testing.T) {
	items := make(map[string][]byte)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/lease/grant":
			_, _ = w.Write([]byte(`{"ID":"lease-1"}`))
		case "/v3/kv/put":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			items[decodeTestBase64(t, body["key"])] = []byte(decodeTestBase64(t, body["value"]))
			_, _ = w.Write([]byte(`{}`))
		case "/v3/kv/range":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			key := decodeTestBase64(t, body["key"])
			if value, ok := items[key]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"kvs": []map[string]string{{"value": base64.StdEncoding.EncodeToString(value)}}})
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}

	// Exists on missing key
	exists, err := store.Exists(context.Background(), "missing")
	if err != nil || exists {
		t.Fatalf("Exists missing = %v, %v; want false", exists, err)
	}
	// TTL on missing key
	if _, err := store.TTL(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TTL missing = %v, want ErrNotFound", err)
	}

	if err := store.Set(context.Background(), "present", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	exists, err = store.Exists(context.Background(), "present")
	if err != nil || !exists {
		t.Fatalf("Exists present = %v, %v; want true", exists, err)
	}
	ttl, err := store.TTL(context.Background(), "present")
	if err != nil || ttl != 0 {
		t.Fatalf("TTL present = %v, %v; want 0", ttl, err)
	}

	// Close is no-op but should not panic
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestConsulStoreWithTokenExistsTTLAndClose(t *testing.T) {
	items := make(map[string][]byte)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		key := r.URL.Path[len("/v1/kv/"):]
		switch r.Method {
		case http.MethodGet:
			value, ok := items[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"Value": base64.StdEncoding.EncodeToString(value)}})
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			items[key] = data
			_, _ = w.Write([]byte(`true`))
		case http.MethodDelete:
			delete(items, key)
			_, _ = w.Write([]byte(`true`))
		}
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithToken("secret-token")
	if store.token != "secret-token" {
		t.Fatalf("token = %q, want secret-token", store.token)
	}

	// Exists / TTL on missing
	exists, err := store.Exists(context.Background(), "missing")
	if err != nil || exists {
		t.Fatalf("Exists missing = %v, %v; want false", exists, err)
	}
	if _, err := store.TTL(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TTL missing = %v, want ErrNotFound", err)
	}

	if err := store.Set(context.Background(), "present", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	exists, err = store.Exists(context.Background(), "present")
	if err != nil || !exists {
		t.Fatalf("Exists present = %v, %v; want true", exists, err)
	}
	ttl, err := store.TTL(context.Background(), "present")
	if err != nil || ttl != 0 {
		t.Fatalf("TTL present = %v, %v; want 0", ttl, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestEtcdStoreDelegatesToHTTPKVAPI(t *testing.T) {
	items := make(map[string][]byte)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/lease/grant":
			_, _ = w.Write([]byte(`{"ID":"lease-1"}`))
		case "/v3/kv/put":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode put: %v", err)
			}
			key := decodeTestBase64(t, body["key"])
			items[key] = []byte(decodeTestBase64(t, body["value"]))
			_, _ = w.Write([]byte(`{}`))
		case "/v3/kv/range":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode range: %v", err)
			}
			key := decodeTestBase64(t, body["key"])
			if value, ok := items[key]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"kvs": []map[string]string{{"value": base64.StdEncoding.EncodeToString(value)}}})
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case "/v3/kv/txn":
			var body struct {
				Compare []struct {
					Key string `json:"key"`
				} `json:"compare"`
				Success []struct {
					RequestPut map[string]string `json:"request_put"`
				} `json:"success"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode txn: %v", err)
			}
			key := decodeTestBase64(t, body.Compare[0].Key)
			if _, ok := items[key]; ok {
				_, _ = w.Write([]byte(`{"succeeded":false}`))
				return
			}
			items[key] = []byte(decodeTestBase64(t, body.Success[0].RequestPut["value"]))
			_, _ = w.Write([]byte(`{"succeeded":true}`))
		case "/v3/kv/deleterange":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode delete: %v", err)
			}
			key := decodeTestBase64(t, body["key"])
			_, ok := items[key]
			delete(items, key)
			if ok {
				_, _ = w.Write([]byte(`{"deleted":"1"}`))
				return
			}
			_, _ = w.Write([]byte(`{"deleted":"0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "gofly/rules", []byte("v1"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(context.Background(), "gofly/rules")
	if err != nil || string(got) != "v1" {
		t.Fatalf("Get = %q, %v; want v1", got, err)
	}
	created, err := store.SetNX(context.Background(), "gofly/rules", []byte("v2"), 0)
	if err != nil || created {
		t.Fatalf("SetNX existing = %v, %v; want false", created, err)
	}
	created, err = store.SetNX(context.Background(), "gofly/new", []byte("v2"), 0)
	if err != nil || !created {
		t.Fatalf("SetNX new = %v, %v; want true", created, err)
	}
	deleted, err := store.Delete(context.Background(), "gofly/rules")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v; want true", deleted, err)
	}
	if _, err := store.Get(context.Background(), "gofly/rules"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrNotFound", err)
	}
}

func TestConsulStoreDelegatesToHTTPKVAPI(t *testing.T) {
	items := make(map[string][]byte)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/kv/gofly/rules" && r.URL.Path != "/v1/kv/gofly/new" {
			http.NotFound(w, r)
			return
		}
		key := r.URL.Path[len("/v1/kv/"):]
		switch r.Method {
		case http.MethodGet:
			value, ok := items[key]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"Value": base64.StdEncoding.EncodeToString(value)}})
		case http.MethodPut:
			if r.URL.Query().Get("cas") == "0" {
				if _, ok := items[key]; ok {
					_, _ = w.Write([]byte(`false`))
					return
				}
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read consul put: %v", err)
			}
			items[key] = data
			_, _ = w.Write([]byte(`true`))
		case http.MethodDelete:
			_, ok := items[key]
			delete(items, key)
			if ok {
				_, _ = w.Write([]byte(`true`))
				return
			}
			_, _ = w.Write([]byte(`false`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "gofly/rules", []byte("v1"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(context.Background(), "gofly/rules")
	if err != nil || string(got) != "v1" {
		t.Fatalf("Get = %q, %v; want v1", got, err)
	}
	created, err := store.SetNX(context.Background(), "gofly/rules", []byte("v2"), 0)
	if err != nil || created {
		t.Fatalf("SetNX existing = %v, %v; want false", created, err)
	}
	created, err = store.SetNX(context.Background(), "gofly/new", []byte("v2"), 0)
	if err != nil || !created {
		t.Fatalf("SetNX new = %v, %v; want true", created, err)
	}
	deleted, err := store.Delete(context.Background(), "gofly/rules")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v; want true", deleted, err)
	}
	if _, err := store.Get(context.Background(), "gofly/rules"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrNotFound", err)
	}
}

func decodeTestBase64(t *testing.T, value string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode base64 %q: %v", value, err)
	}
	return string(data)
}

type fakeRedisClient struct {
	store  *MemoryStore
	closed bool
}

func (c *fakeRedisClient) Get(ctx context.Context, key string) ([]byte, error) {
	return c.store.Get(ctx, key)
}

func (c *fakeRedisClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.store.Set(ctx, key, value, ttl)
}

func (c *fakeRedisClient) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return c.store.SetNX(ctx, key, value, ttl)
}

func (c *fakeRedisClient) Delete(ctx context.Context, key string) (bool, error) {
	return c.store.Delete(ctx, key)
}

func (c *fakeRedisClient) Exists(ctx context.Context, key string) (bool, error) {
	return c.store.Exists(ctx, key)
}

func (c *fakeRedisClient) TTL(ctx context.Context, key string) (time.Duration, error) {
	return c.store.TTL(ctx, key)
}

func (c *fakeRedisClient) Close() error {
	c.closed = true
	return c.store.Close()
}

func TestMemoryStoreNilReceiver(t *testing.T) {
	var s *MemoryStore
	ctx := context.Background()
	if _, err := s.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Get = %v, want ErrClosed", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Set = %v, want ErrClosed", err)
	}
	if _, err := s.SetNX(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil SetNX = %v, want ErrClosed", err)
	}
	if _, err := s.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Delete = %v, want ErrClosed", err)
	}
	if _, err := s.Exists(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Exists = %v, want ErrClosed", err)
	}
	if _, err := s.TTL(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil TTL = %v, want ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
	if stats := s.Snapshot(); stats != (Snapshot{}) {
		t.Errorf("nil Snapshot = %+v, want zero", stats)
	}
}

func TestMemoryStoreNilItemsLazyInit(t *testing.T) {
	s := &MemoryStore{}
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("Get = %q, %v; want v", got, err)
	}
}

func TestMemoryStoreExpiredCleanupOnSnapshot(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Set(context.Background(), "exp", []byte("e"), 1*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	stats := store.Snapshot()
	if stats.Expired != 1 || stats.Entries != 0 {
		t.Fatalf("stats = %+v, want 1 expired 0 entries", stats)
	}
}

func TestEtcdStoreNilReceiver(t *testing.T) {
	var s *EtcdStore
	ctx := context.Background()
	if _, err := s.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Get = %v, want ErrClosed", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Set = %v, want ErrClosed", err)
	}
	if _, err := s.SetNX(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil SetNX = %v, want ErrClosed", err)
	}
	if _, err := s.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Delete = %v, want ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
}

func TestEtcdStoreNewEmptyURL(t *testing.T) {
	_, err := NewEtcdStore("", nil)
	if err == nil {
		t.Fatal("NewEtcdStore empty url: want error, got nil")
	}
}

func TestEtcdStoreNewWhitespaceURL(t *testing.T) {
	_, err := NewEtcdStore("   ", nil)
	if err == nil {
		t.Fatal("NewEtcdStore whitespace url: want error, got nil")
	}
}

func TestEtcdStoreContextCanceled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set canceled = %v, want context.Canceled", err)
	}
}

func TestEtcdStoreHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "k", []byte("v"), 0); err == nil {
		t.Fatal("Set bad request: want error, got nil")
	}
}

func TestEtcdStoreDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{bad json`))
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "k"); err == nil {
		t.Fatal("Get decode error: want error, got nil")
	}
}

func TestEtcdStoreGrantLeaseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/lease/grant" {
			http.Error(w, "fail", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "k", []byte("v"), time.Minute); err == nil {
		t.Fatal("Set with lease error: want error, got nil")
	}
}

func TestEtcdStoreDeleteNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"deleted":"0"}`))
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Delete(context.Background(), "missing")
	if err != nil || deleted {
		t.Fatalf("Delete = %v, %v; want false, nil", deleted, err)
	}
}

func TestEtcdStoreGetDecodeValueError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"kvs": []map[string]string{{"value": "!!!not-base64!!!"}}})
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "k"); err == nil {
		t.Fatal("Get bad base64: want error, got nil")
	}
}

func TestEtcdStoreSetNXLeaseError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/lease/grant" {
			http.Error(w, "fail", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetNX(context.Background(), "k", []byte("v"), time.Minute); err == nil {
		t.Fatal("SetNX with lease error: want error, got nil")
	}
}

func TestConsulStoreNilReceiver(t *testing.T) {
	var s *ConsulStore
	ctx := context.Background()
	if _, err := s.Get(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Get = %v, want ErrClosed", err)
	}
	if err := s.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Set = %v, want ErrClosed", err)
	}
	if _, err := s.SetNX(ctx, "k", []byte("v"), 0); !errors.Is(err, ErrClosed) {
		t.Errorf("nil SetNX = %v, want ErrClosed", err)
	}
	if _, err := s.Delete(ctx, "k"); !errors.Is(err, ErrClosed) {
		t.Errorf("nil Delete = %v, want ErrClosed", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close = %v, want nil", err)
	}
}

func TestConsulStoreNewEmptyURL(t *testing.T) {
	_, err := NewConsulStore("", nil)
	if err == nil {
		t.Fatal("NewConsulStore empty url: want error, got nil")
	}
}

func TestConsulStoreContextCanceled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Set(ctx, "k", []byte("v"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set canceled = %v, want context.Canceled", err)
	}
}

func TestConsulStoreHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), "k", []byte("v"), 0); err == nil {
		t.Fatal("Set bad request: want error, got nil")
	}
}

func TestConsulStoreDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{bad json`))
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "k"); err == nil {
		t.Fatal("Get decode error: want error, got nil")
	}
}

func TestConsulStoreGetDecodeValueError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{{"Value": "!!!not-base64!!!"}})
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "k"); err == nil {
		t.Fatal("Get bad base64: want error, got nil")
	}
}

func TestConsulStoreDeleteNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`false`))
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Delete(context.Background(), "missing")
	if err != nil || deleted {
		t.Fatalf("Delete = %v, %v; want false, nil", deleted, err)
	}
}

func TestConsulStoreSetNXDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{bad json`))
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetNX(context.Background(), "k", []byte("v"), 0); err == nil {
		t.Fatal("SetNX decode error: want error, got nil")
	}
}

func TestConsulStoreDeleteDecodeError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{bad json`))
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Delete(context.Background(), "k"); err == nil {
		t.Fatal("Delete decode error: want error, got nil")
	}
}

func TestConsulStoreWithTokenNil(t *testing.T) {
	var s *ConsulStore
	if s.WithToken("t") != nil {
		t.Fatal("WithToken nil: want nil")
	}
}

func TestConsulStoreNotFoundStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func TestConsulKVPathEscaping(t *testing.T) {
	if got := consulKVPath("a/b c"); got != "/v1/kv/a/b%20c" {
		t.Fatalf("consulKVPath = %q, want /v1/kv/a/b%%20c", got)
	}
	if got := consulKVPath("/a/b"); got != "/v1/kv/a/b" {
		t.Fatalf("consulKVPath leading slash = %q, want /v1/kv/a/b", got)
	}
}

func TestExpiresAtZero(t *testing.T) {
	if !expiresAt(0).IsZero() {
		t.Fatal("expiresAt(0) should be zero")
	}
}

func TestCheckContextNil(t *testing.T) {
	//nolint:staticcheck // verifies checkContext accepts nil as a no-op context.
	if err := checkContext(nil); err != nil {
		t.Fatalf("checkContext nil = %v, want nil", err)
	}
}

func TestCheckContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("checkContext canceled = %v, want context.Canceled", err)
	}
}

func TestEtcdStoreExistsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusBadRequest)
	}))
	defer ts.Close()

	store, err := NewEtcdStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Exists(context.Background(), "k")
	if err == nil {
		t.Fatal("Exists error: want error, got nil")
	}
}

func TestConsulStoreExistsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusBadRequest)
	}))
	defer ts.Close()

	store, err := NewConsulStore(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Exists(context.Background(), "k")
	if err == nil {
		t.Fatal("Exists error: want error, got nil")
	}
}
