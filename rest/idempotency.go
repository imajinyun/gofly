// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/kv"
)

// DefaultIdempotencyHeader is the default header name for idempotency keys.
const DefaultIdempotencyHeader = "Idempotency-Key"

var errIdempotencyBodyTooLarge = errors.New("idempotency request body exceeds maximum size")

// IdempotencyConfig controls idempotency middleware behaviour.
type IdempotencyConfig struct {
	HeaderName    string
	TTL           time.Duration
	MaxBodyBytes  int64
	StoreFailures bool
	// Store persists idempotent responses. When nil an in-process memory store
	// is used. Provide a shared backend (e.g. NewKVIdempotencyStore) to make
	// idempotency work across multiple replicas.
	Store IdempotencyStore
}

// StoredResponse is a captured HTTP response persisted for replay.
type StoredResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// IdempotencyStore persists idempotent responses keyed by request identity.
//
// Reserve claims a key for a new request or reports the status of an existing
// one. It returns the stored response (when available for replay) together with
// booleans indicating whether the caller should replay an existing response or
// reject the request as a conflict. Implementations may block until an in-flight
// request with the same fingerprint completes (the memory store does); otherwise
// an in-flight duplicate should be reported as a conflict to avoid re-executing
// the handler.
//
// Complete persists the captured response when keep is true, or releases the
// reservation when keep is false (e.g. for failures that should be retriable).
type IdempotencyStore interface {
	Reserve(ctx context.Context, key, fingerprint string, ttl time.Duration) (resp *StoredResponse, replay, conflict bool, err error)
	Complete(ctx context.Context, key, fingerprint string, resp StoredResponse, keep bool, ttl time.Duration) error
}

type captureResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func IdempotencyMiddleware(config IdempotencyConfig) Middleware {
	config = resolveIdempotencyConfig(config)
	store := config.Store
	if store == nil {
		store = NewMemoryIdempotencyStore()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := strings.TrimSpace(r.Header.Get(config.HeaderName))
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			body, err := readIdempotencyBody(r, config.MaxBodyBytes)
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, coreerrors.CodeResourceExhausted, err.Error())
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			fingerprint := idempotencyFingerprint(r, body)
			entryKey := r.Method + " " + r.URL.Path + " " + key
			resp, replay, conflict, err := store.Reserve(r.Context(), entryKey, fingerprint, config.TTL)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, "idempotency store is unavailable")
				return
			}
			if conflict {
				writeError(w, http.StatusConflict, coreerrors.CodeAlreadyExists, "idempotency key conflicts with a different request")
				return
			}
			if replay {
				if resp == nil {
					writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, "idempotent response is unavailable")
					return
				}
				replayResponse(w, *resp)
				return
			}
			rec := newCaptureResponseWriter()
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			keep := config.StoreFailures || rec.status < http.StatusInternalServerError
			stored := StoredResponse{
				Status: rec.status,
				Header: cloneHeader(rec.header),
				Body:   append([]byte(nil), rec.body.Bytes()...),
			}
			_ = store.Complete(r.Context(), entryKey, fingerprint, stored, keep, config.TTL)
			copyHeader(w.Header(), rec.header)
			w.WriteHeader(rec.status)
			_, _ = w.Write(rec.body.Bytes())
		})
	}
}

func resolveIdempotencyConfig(config IdempotencyConfig) IdempotencyConfig {
	if config.HeaderName == "" {
		config.HeaderName = DefaultIdempotencyHeader
	}
	if config.TTL <= 0 {
		config.TTL = 24 * time.Hour
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	return config
}

// memoryIdempotencyStore is the default in-process IdempotencyStore. It blocks
// concurrent duplicates with the same fingerprint until the first request
// completes, then replays the stored response.
type memoryIdempotencyStore struct {
	mu      sync.Mutex
	entries map[string]*memoryIdempotencyEntry
}

type memoryIdempotencyEntry struct {
	fingerprint string
	resp        StoredResponse
	expiresAt   time.Time
	done        chan struct{}
	ready       bool
}

// NewMemoryIdempotencyStore returns an in-process IdempotencyStore.
func NewMemoryIdempotencyStore() IdempotencyStore {
	return &memoryIdempotencyStore{entries: make(map[string]*memoryIdempotencyEntry)}
}

func (s *memoryIdempotencyStore) Reserve(_ context.Context, key, fingerprint string, ttl time.Duration) (*StoredResponse, bool, bool, error) {
	s.mu.Lock()
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.expiresAt) && e.ready {
			delete(s.entries, k)
		}
	}
	if e := s.entries[key]; e != nil {
		if e.fingerprint != fingerprint {
			s.mu.Unlock()
			return nil, false, true, nil
		}
		done := e.done
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		defer s.mu.Unlock()
		e = s.entries[key]
		if e == nil || !e.ready {
			return nil, true, false, nil
		}
		resp := e.resp
		return &resp, true, false, nil
	}
	s.entries[key] = &memoryIdempotencyEntry{
		fingerprint: fingerprint,
		expiresAt:   now.Add(ttl),
		done:        make(chan struct{}),
	}
	s.mu.Unlock()
	return nil, false, false, nil
}

func (s *memoryIdempotencyStore) Complete(_ context.Context, key, _ string, resp StoredResponse, keep bool, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[key]
	if e == nil {
		return nil
	}
	if keep {
		e.resp = resp
		e.ready = true
		e.expiresAt = time.Now().Add(ttl)
	} else {
		delete(s.entries, key)
	}
	close(e.done)
	return nil
}

// kvIdempotencyStore persists idempotent responses in a shared kv.Store such as
// Redis, making idempotency effective across multiple replicas.
type kvIdempotencyStore struct {
	store kv.Store
}

type idempotencyRecord struct {
	Fingerprint string      `json:"fingerprint"`
	Ready       bool        `json:"ready"`
	Status      int         `json:"status,omitempty"`
	Header      http.Header `json:"header,omitempty"`
	Body        []byte      `json:"body,omitempty"`
}

// NewKVIdempotencyStore returns an IdempotencyStore backed by a shared kv.Store
// (e.g. kv.NewRedisStore(client)).
func NewKVIdempotencyStore(store kv.Store) IdempotencyStore {
	return &kvIdempotencyStore{store: store}
}

func (s *kvIdempotencyStore) Reserve(ctx context.Context, key, fingerprint string, ttl time.Duration) (*StoredResponse, bool, bool, error) {
	pending, err := json.Marshal(idempotencyRecord{Fingerprint: fingerprint})
	if err != nil {
		return nil, false, false, err
	}
	ok, err := s.store.SetNX(ctx, key, pending, ttl)
	if err != nil {
		return nil, false, false, err
	}
	if ok {
		return nil, false, false, nil
	}
	data, err := s.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, kv.ErrNotFound) {
			// The reservation expired between SetNX and Get; treat as a conflict
			// rather than risk a duplicate execution.
			return nil, false, true, nil
		}
		return nil, false, false, err
	}
	var stored idempotencyRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, false, false, err
	}
	if stored.Fingerprint != fingerprint {
		return nil, false, true, nil
	}
	if !stored.Ready {
		// Another replica is processing the same request; reject to avoid
		// re-executing the handler.
		return nil, false, true, nil
	}
	resp := StoredResponse{Status: stored.Status, Header: stored.Header, Body: stored.Body}
	return &resp, true, false, nil
}

func (s *kvIdempotencyStore) Complete(ctx context.Context, key, fingerprint string, resp StoredResponse, keep bool, ttl time.Duration) error {
	if !keep {
		if _, err := s.store.Delete(ctx, key); err != nil && !errors.Is(err, kv.ErrNotFound) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(idempotencyRecord{
		Fingerprint: fingerprint,
		Ready:       true,
		Status:      resp.Status,
		Header:      resp.Header,
		Body:        resp.Body,
	})
	if err != nil {
		return err
	}
	return s.store.Set(ctx, key, data, ttl)
}

func replayResponse(w http.ResponseWriter, resp StoredResponse) {
	copyHeader(w.Header(), resp.Header)
	w.Header().Set("X-Idempotency-Replayed", "true")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}

func readIdempotencyBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	reader := io.LimitReader(r.Body, max+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errIdempotencyBodyTooLarge
	}
	return body, nil
}

func idempotencyFingerprint(r *http.Request, body []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(r.Method))
	_, _ = h.Write([]byte("\n"))
	_, _ = h.Write([]byte(r.URL.Path))
	_, _ = h.Write([]byte("\n"))
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func newCaptureResponseWriter() *captureResponseWriter {
	return &captureResponseWriter{header: make(http.Header)}
}

func (w *captureResponseWriter) Header() http.Header { return w.header }

func (w *captureResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
}

func (w *captureResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func cloneHeader(header http.Header) http.Header {
	out := make(http.Header, len(header))
	copyHeader(out, header)
	return out
}

func copyHeader(dst, src http.Header) {
	for key := range dst {
		delete(dst, key)
	}
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}
