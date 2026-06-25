package redis

import "github.com/imajinyun/gofly/core/kv"

// Compile-time assertion that *Client satisfies the kv.RedisClient contract so
// it can be wrapped by kv.NewRedisStore.
var _ kv.RedisClient = (*Client)(nil)
