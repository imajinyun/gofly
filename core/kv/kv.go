// Package kv provides a key-value store abstraction for gofly services,
// supporting get, set, delete, and TTL operations.
package kv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	core "github.com/imajinyun/gofly/core"
)

var (
	// ErrNotFound is returned when a key does not exist.
	ErrNotFound = errors.New("kv key not found")
	// ErrClosed is returned when operating on a closed store.
	ErrClosed = errors.New("kv store is closed")
)

// Store is a key-value store abstraction.
type Store interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration) error
	SetNX(context.Context, string, []byte, time.Duration) (bool, error)
	Delete(context.Context, string) (bool, error)
	Exists(context.Context, string) (bool, error)
	TTL(context.Context, string) (time.Duration, error)
	Close() error
}

// Snapshot is a point-in-time view of store statistics.
type Snapshot struct {
	Entries int   `json:"entries"`
	Sets    int64 `json:"sets"`
	Hits    int64 `json:"hits"`
	Misses  int64 `json:"misses"`
	Deletes int64 `json:"deletes"`
	Expired int64 `json:"expired"`
}

type MemoryStore struct {
	mu     sync.Mutex
	items  map[string]entry
	closed bool
	stats  Snapshot
}

type entry struct {
	value     []byte
	expiresAt time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]entry)}
}

func (s *MemoryStore) Get(ctx context.Context, key string) ([]byte, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpenLocked(); err != nil {
		return nil, err
	}
	ent, ok := s.getLocked(key, time.Now())
	if !ok {
		s.stats.Misses++
		return nil, ErrNotFound
	}
	s.stats.Hits++
	return append([]byte(nil), ent.value...), nil
}

func (s *MemoryStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if s == nil {
		return ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpenLocked(); err != nil {
		return err
	}
	s.items[key] = entry{value: append([]byte(nil), value...), expiresAt: expiresAt(ttl)}
	s.stats.Sets++
	return nil
}

func (s *MemoryStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if err := checkContext(ctx); err != nil {
		return false, err
	}
	if s == nil {
		return false, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpenLocked(); err != nil {
		return false, err
	}
	now := time.Now()
	if _, ok := s.getLocked(key, now); ok {
		return false, nil
	}
	s.items[key] = entry{value: append([]byte(nil), value...), expiresAt: expiresAt(ttl)}
	s.stats.Sets++
	return true, nil
}

func (s *MemoryStore) Delete(ctx context.Context, key string) (bool, error) {
	if err := checkContext(ctx); err != nil {
		return false, err
	}
	if s == nil {
		return false, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpenLocked(); err != nil {
		return false, err
	}
	if _, ok := s.items[key]; !ok {
		return false, nil
	}
	delete(s.items, key)
	s.stats.Deletes++
	return true, nil
}

func (s *MemoryStore) Exists(ctx context.Context, key string) (bool, error) {
	if err := checkContext(ctx); err != nil {
		return false, err
	}
	if s == nil {
		return false, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpenLocked(); err != nil {
		return false, err
	}
	_, ok := s.getLocked(key, time.Now())
	return ok, nil
}

func (s *MemoryStore) TTL(ctx context.Context, key string) (time.Duration, error) {
	if err := checkContext(ctx); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOpenLocked(); err != nil {
		return 0, err
	}
	ent, ok := s.getLocked(key, time.Now())
	if !ok {
		return 0, ErrNotFound
	}
	if ent.expiresAt.IsZero() {
		return 0, nil
	}
	return time.Until(ent.expiresAt), nil
}

func (s *MemoryStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for key := range s.items {
		delete(s.items, key)
	}
	return nil
}

func (s *MemoryStore) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	snapshot := s.stats
	snapshot.Entries = len(s.items)
	return snapshot
}

func (s *MemoryStore) getLocked(key string, now time.Time) (entry, bool) {
	ent, ok := s.items[key]
	if !ok {
		return entry{}, false
	}
	if !ent.expiresAt.IsZero() && !now.Before(ent.expiresAt) {
		delete(s.items, key)
		s.stats.Expired++
		return entry{}, false
	}
	return ent, true
}

func (s *MemoryStore) cleanupLocked(now time.Time) {
	for key, ent := range s.items {
		if !ent.expiresAt.IsZero() && !now.Before(ent.expiresAt) {
			delete(s.items, key)
			s.stats.Expired++
		}
	}
}

func (s *MemoryStore) checkOpenLocked() error {
	if s == nil || s.closed {
		return ErrClosed
	}
	if s.items == nil {
		s.items = make(map[string]entry)
	}
	return nil
}

type RedisClient interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte, time.Duration) error
	SetNX(context.Context, string, []byte, time.Duration) (bool, error)
	Delete(context.Context, string) (bool, error)
	Exists(context.Context, string) (bool, error)
	TTL(context.Context, string) (time.Duration, error)
	Close() error
}

type RedisStore struct {
	client RedisClient
}

func NewRedisStore(client RedisClient) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, ErrClosed
	}
	return s.client.Get(ctx, key)
}

func (s *RedisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrClosed
	}
	return s.client.Set(ctx, key, append([]byte(nil), value...), ttl)
}

func (s *RedisStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	return s.client.SetNX(ctx, key, append([]byte(nil), value...), ttl)
}

func (s *RedisStore) Delete(ctx context.Context, key string) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	return s.client.Delete(ctx, key)
}

func (s *RedisStore) Exists(ctx context.Context, key string) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	return s.client.Exists(ctx, key)
}

func (s *RedisStore) TTL(ctx context.Context, key string) (time.Duration, error) {
	if s == nil || s.client == nil {
		return 0, ErrClosed
	}
	return s.client.TTL(ctx, key)
}

func (s *RedisStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

type EtcdStore struct {
	client  *http.Client
	baseURL string
}

func NewEtcdStore(baseURL string, client *http.Client) (*EtcdStore, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("etcd base url is required")
	}
	if client == nil {
		client = core.DefaultHTTPClient()
	}
	return &EtcdStore{client: client, baseURL: baseURL}, nil
}

func (s *EtcdStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, ErrClosed
	}
	var resp etcdRangeResponse
	if err := s.do(ctx, "/v3/kv/range", map[string]string{"key": encodeBase64(key)}, &resp); err != nil {
		return nil, err
	}
	if len(resp.KVs) == 0 {
		return nil, ErrNotFound
	}
	value, err := base64.StdEncoding.DecodeString(resp.KVs[0].Value)
	if err != nil {
		return nil, fmt.Errorf("decode etcd value: %w", err)
	}
	return value, nil
}

func (s *EtcdStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrClosed
	}
	body := map[string]any{"key": encodeBase64(key), "value": encodeBase64Bytes(value)}
	lease, err := s.grantLease(ctx, ttl)
	if err != nil {
		return err
	}
	if lease != "" {
		body["lease"] = lease
	}
	return s.do(ctx, "/v3/kv/put", body, nil)
}

func (s *EtcdStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	put := map[string]any{"key": encodeBase64(key), "value": encodeBase64Bytes(value)}
	lease, err := s.grantLease(ctx, ttl)
	if err != nil {
		return false, err
	}
	if lease != "" {
		put["lease"] = lease
	}
	var resp etcdTxnResponse
	if err := s.do(ctx, "/v3/kv/txn", map[string]any{
		"compare": []map[string]string{{"key": encodeBase64(key), "target": "VERSION", "result": "EQUAL", "version": "0"}},
		"success": []map[string]any{{"request_put": put}},
	}, &resp); err != nil {
		return false, err
	}
	return resp.Succeeded, nil
}

func (s *EtcdStore) Delete(ctx context.Context, key string) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	var resp etcdDeleteResponse
	if err := s.do(ctx, "/v3/kv/deleterange", map[string]string{"key": encodeBase64(key)}, &resp); err != nil {
		return false, err
	}
	return resp.Deleted != "" && resp.Deleted != "0", nil
}

func (s *EtcdStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *EtcdStore) TTL(ctx context.Context, key string) (time.Duration, error) {
	ok, err := s.Exists(ctx, key)
	if err != nil || !ok {
		if err == nil {
			err = ErrNotFound
		}
		return 0, err
	}
	return 0, nil
}

func (s *EtcdStore) Close() error { return nil }

func (s *EtcdStore) grantLease(ctx context.Context, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", nil
	}
	seconds := int64((ttl + time.Second - 1) / time.Second)
	if seconds <= 0 {
		seconds = 1
	}
	var resp etcdLeaseGrantResponse
	if err := s.do(ctx, "/v3/lease/grant", map[string]string{"TTL": fmt.Sprintf("%d", seconds)}, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (s *EtcdStore) do(ctx context.Context, path string, body any, out any) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal etcd request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create etcd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("call etcd: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("call etcd: status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode etcd response: %w", err)
	}
	return nil
}

type ConsulStore struct {
	client  *http.Client
	baseURL string
	token   string
}

func NewConsulStore(baseURL string, client *http.Client) (*ConsulStore, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("consul base url is required")
	}
	if client == nil {
		client = core.DefaultHTTPClient()
	}
	return &ConsulStore{client: client, baseURL: baseURL}, nil
}

func (s *ConsulStore) WithToken(token string) *ConsulStore {
	if s != nil {
		s.token = token
	}
	return s
}

func (s *ConsulStore) Get(ctx context.Context, key string) ([]byte, error) {
	if s == nil || s.client == nil {
		return nil, ErrClosed
	}
	var values []consulKVPair
	if err := s.do(ctx, http.MethodGet, consulKVPath(key), nil, &values); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, ErrNotFound
	}
	value, err := base64.StdEncoding.DecodeString(values[0].Value)
	if err != nil {
		return nil, fmt.Errorf("decode consul value: %w", err)
	}
	return value, nil
}

func (s *ConsulStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrClosed
	}
	return s.do(ctx, http.MethodPut, consulKVPath(key), bytes.NewReader(append([]byte(nil), value...)), nil)
}

func (s *ConsulStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	var ok bool
	if err := s.do(ctx, http.MethodPut, consulKVPath(key)+"?cas=0", bytes.NewReader(append([]byte(nil), value...)), &ok); err != nil {
		return false, err
	}
	return ok, nil
}

func (s *ConsulStore) Delete(ctx context.Context, key string) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrClosed
	}
	var ok bool
	if err := s.do(ctx, http.MethodDelete, consulKVPath(key), nil, &ok); err != nil {
		return false, err
	}
	return ok, nil
}

func (s *ConsulStore) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *ConsulStore) TTL(ctx context.Context, key string) (time.Duration, error) {
	ok, err := s.Exists(ctx, key)
	if err != nil || !ok {
		if err == nil {
			err = ErrNotFound
		}
		return 0, err
	}
	return 0, nil
}

func (s *ConsulStore) Close() error { return nil }

func (s *ConsulStore) do(ctx context.Context, method string, path string, body io.Reader, out any) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create consul request: %w", err)
	}
	if s.token != "" {
		req.Header.Set("X-Consul-Token", s.token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("call consul: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("call consul: status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode consul response: %w", err)
	}
	return nil
}

type etcdRangeResponse struct {
	KVs []struct {
		Value string `json:"value"`
	} `json:"kvs"`
}

type etcdDeleteResponse struct {
	Deleted string `json:"deleted"`
}

type etcdTxnResponse struct {
	Succeeded bool `json:"succeeded"`
}

type etcdLeaseGrantResponse struct {
	ID string `json:"ID"`
}

type consulKVPair struct {
	Value string `json:"Value"`
}

func encodeBase64(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }

func encodeBase64Bytes(value []byte) string { return base64.StdEncoding.EncodeToString(value) }

func consulKVPath(key string) string {
	parts := strings.Split(strings.TrimLeft(key, "/"), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return "/v1/kv/" + strings.Join(parts, "/")
}

func expiresAt(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
