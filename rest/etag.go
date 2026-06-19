// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

// ETagConfig controls ETag generation behaviour.
type ETagConfig struct {
	Weak         bool
	MaxBodyBytes int64
	Methods      []string
}

// ETagMiddleware returns middleware that generates SHA-256 ETag headers.
func ETagMiddleware(config ETagConfig) Middleware {
	config = resolveETagConfig(config)
	methods := make(map[string]struct{}, len(config.Methods))
	for _, method := range config.Methods {
		methods[strings.ToUpper(method)] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := methods[r.Method]; !ok {
				next.ServeHTTP(w, r)
				return
			}
			rec := newCaptureResponseWriter()
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			if rec.status < http.StatusOK || rec.status >= http.StatusMultipleChoices || int64(rec.body.Len()) > config.MaxBodyBytes {
				copyHeader(w.Header(), rec.header)
				w.WriteHeader(rec.status)
				_, _ = w.Write(rec.body.Bytes())
				return
			}
			etag := rec.header.Get("ETag")
			if etag == "" {
				etag = buildETag(rec.body.Bytes(), config.Weak)
				rec.header.Set("ETag", etag)
			}
			if ifNoneMatch(r.Header.Get("If-None-Match"), etag) {
				copyHeader(w.Header(), rec.header)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			copyHeader(w.Header(), rec.header)
			w.WriteHeader(rec.status)
			if r.Method != http.MethodHead {
				_, _ = w.Write(rec.body.Bytes())
			}
		})
	}
}

func resolveETagConfig(config ETagConfig) ETagConfig {
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	if len(config.Methods) == 0 {
		config.Methods = []string{http.MethodGet, http.MethodHead}
	}
	return config
}

func buildETag(body []byte, weak bool) string {
	sum := sha256.Sum256(body)
	value := `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	if weak {
		return "W/" + value
	}
	return value
}

func ifNoneMatch(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}
