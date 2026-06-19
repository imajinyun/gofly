package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errRedisMiss = errors.New("redis miss")

type fakeRedisModelClient struct {
	values  map[string][]byte
	deleted []string
	ttl     time.Duration
}

func newFakeRedisModelClient() *fakeRedisModelClient {
	return &fakeRedisModelClient{values: make(map[string][]byte)}
}

func (f *fakeRedisModelClient) Get(ctx context.Context, key string) ([]byte, error) {
	if value, ok := f.values[key]; ok {
		return value, nil
	}
	return nil, errRedisMiss
}

func (f *fakeRedisModelClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	f.ttl = ttl
	f.values[key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeRedisModelClient) Delete(ctx context.Context, key string) (bool, error) {
	f.deleted = append(f.deleted, key)
	_, ok := f.values[key]
	delete(f.values, key)
	return ok, nil
}

func TestRedisModelCacheLoadsAndStoresOnMiss(t *testing.T) {
	client := newFakeRedisModelClient()
	loads := 0
	type user struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	c := NewRedisModel(func(ctx context.Context, id int64) (*user, error) {
		loads++
		return &user{ID: id, Name: "ada"}, nil
	}, client,
		WithRedisModelNotFound[*user, int64](errRedisMiss),
		WithRedisModelKeyPrefix[*user, int64]("users"),
		WithRedisModelTTL[*user, int64](30*time.Second),
	)

	got, err := c.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != 42 || got.Name != "ada" {
		t.Fatalf("loaded user = %+v, want id 42 name ada", got)
	}
	if loads != 1 {
		t.Fatalf("loader calls = %d, want 1", loads)
	}
	if _, ok := client.values["users:42"]; !ok {
		t.Fatalf("redis value for users:42 was not set: %#v", client.values)
	}
	if client.ttl != 30*time.Second {
		t.Fatalf("redis ttl = %v, want 30s", client.ttl)
	}

	got, err = c.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("Get cached returned error: %v", err)
	}
	if got.ID != 42 || got.Name != "ada" {
		t.Fatalf("cached user = %+v, want id 42 name ada", got)
	}
	if loads != 1 {
		t.Fatalf("loader calls after cache hit = %d, want 1", loads)
	}
}

func TestRedisModelCacheInvalidateDeletesKey(t *testing.T) {
	client := newFakeRedisModelClient()
	c := NewRedisModel(func(ctx context.Context, id string) (string, error) {
		return "value", nil
	}, client,
		WithRedisModelNotFound[string, string](errRedisMiss),
		WithRedisModelKeyPrefix[string, string]("orders"),
	)
	if err := c.Set(context.Background(), "A1", "value"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if err := c.Invalidate(context.Background(), "A1"); err != nil {
		t.Fatalf("Invalidate returned error: %v", err)
	}
	if _, ok := client.values["orders:A1"]; ok {
		t.Fatalf("redis key orders:A1 still exists")
	}
	if len(client.deleted) != 1 || client.deleted[0] != "orders:A1" {
		t.Fatalf("deleted keys = %v, want orders:A1", client.deleted)
	}
}

func TestRedisModelCacheWithCustomKey(t *testing.T) {
	client := newFakeRedisModelClient()
	c := NewRedisModel(func(ctx context.Context, id int64) (string, error) {
		return "value", nil
	}, client,
		WithRedisModelNotFound[string, int64](errRedisMiss),
		WithRedisModelKey[string, int64](func(id int64) string { return "custom-key" }),
	)

	if err := c.Set(context.Background(), 42, "value"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if _, ok := client.values["custom-key"]; !ok {
		t.Fatalf("redis values = %#v, want custom-key", client.values)
	}
	if _, ok := client.values["42"]; ok {
		t.Fatalf("default key 42 was set despite custom key: %#v", client.values)
	}
}

func TestRedisModelCacheIgnoresNilCustomKey(t *testing.T) {
	client := newFakeRedisModelClient()
	c := NewRedisModel(func(ctx context.Context, id int64) (string, error) {
		return "value", nil
	}, client,
		WithRedisModelNotFound[string, int64](errRedisMiss),
		WithRedisModelKey[string, int64](nil),
	)

	if err := c.Set(context.Background(), 42, "value"); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if _, ok := client.values["42"]; !ok {
		t.Fatalf("redis values = %#v, want default key 42", client.values)
	}
}
