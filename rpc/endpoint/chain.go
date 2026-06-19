// Package endpoint provides RPC client middleware primitives: chaining,
// hedging, timeouts and retries.
package endpoint

// Chain composes middlewares in declaration order (first = outermost).
//
// Unlike the classic reverse-range loop, this implementation uses recursive
// composition. The two forms are semantically identical; the recursive variant
// is used here for explicitness: Chain(a, b, c)(next) = a(b(c(next))).
func Chain(mws ...Middleware) Middleware {
	return func(next Endpoint) Endpoint {
		if len(mws) == 0 {
			return next
		}
		return mws[0](Chain(mws[1:]...)(next))
	}
}
