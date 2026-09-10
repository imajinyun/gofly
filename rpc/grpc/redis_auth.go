package grpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/auth"

	redisv9 "github.com/redis/go-redis/v9"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// RedisHashReader is the minimal Redis hash contract required by the validator.
type RedisHashReader interface {
	HGet(context.Context, string, string) *redisv9.StringCmd
}

type redisTokenCacheEntry struct {
	tokenHash [sha256.Size]byte
	expiresAt time.Time
}

// RedisTokenValidator validates bearer tokens against fields in a Redis hash.
// The token subject is read from subjectMetadataKey and becomes the Principal
// subject. Backend failures fail closed in strict mode and fail open otherwise.
type RedisTokenValidator struct {
	client             RedisHashReader
	hashKey            string
	subjectMetadataKey string
	strict             bool
	cacheTTL           time.Duration
	now                func() time.Time
	mu                 sync.Mutex
	cache              map[string]redisTokenCacheEntry
}

// NewRedisTokenValidator creates a compatibility validator for go-zero-style
// app/token credentials stored in a Redis hash.
func NewRedisTokenValidator(client RedisHashReader, hashKey string, strict bool, cacheTTL time.Duration) (*RedisTokenValidator, error) {
	if client == nil {
		return nil, errors.New("redis token validator client is required")
	}
	if strings.TrimSpace(hashKey) == "" {
		return nil, errors.New("redis token validator hash key is required")
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	return &RedisTokenValidator{
		client: client, hashKey: strings.TrimSpace(hashKey), subjectMetadataKey: "app",
		strict: strict, cacheTTL: cacheTTL, now: time.Now, cache: make(map[string]redisTokenCacheEntry),
	}, nil
}

// Validator returns an auth.Validator that expects the app identity in gRPC
// metadata and the bearer token in the authorization metadata field.
func (v *RedisTokenValidator) Validator() auth.Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		if strings.TrimSpace(token) == "" {
			return ctx, auth.ErrMissingCredentials
		}
		subject := firstIncomingMetadata(ctx, v.subjectMetadataKey)
		if subject == "" {
			return ctx, auth.ErrMissingCredentials
		}
		expected, err := v.expectedTokenHash(ctx, subject)
		if err != nil {
			if !v.strict {
				return auth.NewContext(ctx, auth.Principal{Subject: subject}), nil
			}
			return ctx, err
		}
		presented := sha256.Sum256([]byte(token))
		if subtle.ConstantTimeCompare(presented[:], expected[:]) != 1 {
			return ctx, auth.ErrInvalidCredentials
		}
		return auth.NewContext(ctx, auth.Principal{Subject: subject}), nil
	}
}

func (v *RedisTokenValidator) expectedTokenHash(ctx context.Context, subject string) ([sha256.Size]byte, error) {
	now := v.now()
	v.mu.Lock()
	entry, ok := v.cache[subject]
	v.mu.Unlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.tokenHash, nil
	}
	token, err := v.client.HGet(ctx, v.hashKey, subject).Result()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	digest := sha256.Sum256([]byte(token))
	v.mu.Lock()
	for key, cached := range v.cache {
		if !now.Before(cached.expiresAt) {
			delete(v.cache, key)
		}
	}
	v.cache[subject] = redisTokenCacheEntry{tokenHash: digest, expiresAt: now.Add(v.cacheTTL)}
	v.mu.Unlock()
	return digest, nil
}

func firstIncomingMetadata(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

// AppTokenCredentials sends go-zero-compatible app/token metadata. TLS is
// required by default; AllowInsecure must be an explicit development opt-in.
type AppTokenCredentials struct {
	App           string
	Token         string
	AllowInsecure bool
}

func (c AppTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	if strings.TrimSpace(c.App) == "" || strings.TrimSpace(c.Token) == "" {
		return nil, auth.ErrMissingCredentials
	}
	return map[string]string{
		"app":            c.App,
		"token":          c.Token,
		auth.MetadataKey: auth.BearerPrefix + c.Token,
	}, nil
}

func (c AppTokenCredentials) RequireTransportSecurity() bool { return !c.AllowInsecure }

// WithAppTokenCredentials adds go-zero-compatible app/token credentials.
func WithAppTokenCredentials(credentials AppTokenCredentials) ClientOption {
	return withClientDialOptions(stdgrpc.WithPerRPCCredentials(credentials))
}
