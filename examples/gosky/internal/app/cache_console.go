package app

import (
	"context"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/runtimecache"
)

// CacheConsoleService backs the unmodified support/cache console.
type CacheConsoleService struct {
	registry   *runtimecache.Registry
	authorizer RBACAuthorizer
}

// NewCacheConsoleService wires the cache console to the named registry.
func NewCacheConsoleService(registry *runtimecache.Registry, authorizer RBACAuthorizer) *CacheConsoleService {
	return &CacheConsoleService{registry: registry, authorizer: authorizer}
}

// Names returns registered cache names; key inspection requires the read
// capability in the Java reference, and names power the same page.
func (s *CacheConsoleService) Names(ctx context.Context) ([]string, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportCacheRead); err != nil {
		return nil, err
	}
	return s.registry.Names(), nil
}

// Keys returns a snapshot of one cache's keys.
func (s *CacheConsoleService) Keys(ctx context.Context, name string) ([]string, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportCacheRead); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	cache, ok := s.registry.Get(name)
	if !ok {
		// Unknown cache behaves like an empty cache in the Java client.
		return []string{}, nil
	}
	keys, storeErr := cache.Keys()
	if storeErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "read cache keys", storeErr)
	}
	return keys, nil
}

// Remove clears one named cache.
func (s *CacheConsoleService) Remove(ctx context.Context, name string) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportCacheManage); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "缓存名称不能为空")
	}
	cache, ok := s.registry.Get(name)
	if !ok {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "不能从系统中找到对应的缓存："+name)
	}
	if err := cache.Clear(); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "clear cache", err)
	}
	return nil
}
