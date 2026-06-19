// Package core provides common utilities used across gofly subsystems.
package core

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DefaultHTTPClientTimeout is the fallback HTTP client timeout.
const DefaultHTTPClientTimeout = 30 * time.Second

// HTTPClientConfig controls the shared default HTTP client used by framework
// adapters when callers do not provide a client explicitly.
type HTTPClientConfig struct {
	Timeout             time.Duration
	TLSHandshakeTimeout time.Duration
	IdleConnTimeout     time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	Proxy               func(*http.Request) (*url.URL, error)
	TLSClientConfig     *tls.Config
}

// DefaultHTTPClient returns a client with bounded timeout, proxy support,
// sensible idle-connection reuse, and a cloned transport per call.
func DefaultHTTPClient() *http.Client {
	return NewHTTPClient(HTTPClientConfig{})
}

func NewHTTPClient(conf HTTPClientConfig) *http.Client {
	if conf.Timeout <= 0 {
		conf.Timeout = DefaultHTTPClientTimeout
	}
	if conf.TLSHandshakeTimeout <= 0 {
		conf.TLSHandshakeTimeout = 10 * time.Second
	}
	if conf.IdleConnTimeout <= 0 {
		conf.IdleConnTimeout = 90 * time.Second
	}
	if conf.MaxIdleConns <= 0 {
		conf.MaxIdleConns = 100
	}
	if conf.MaxIdleConnsPerHost <= 0 {
		conf.MaxIdleConnsPerHost = 10
	}
	proxy := conf.Proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout: conf.Timeout,
		Transport: &http.Transport{
			Proxy:                 proxy,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          conf.MaxIdleConns,
			MaxIdleConnsPerHost:   conf.MaxIdleConnsPerHost,
			IdleConnTimeout:       conf.IdleConnTimeout,
			TLSHandshakeTimeout:   conf.TLSHandshakeTimeout,
			ExpectContinueTimeout: time.Second,
			TLSClientConfig:       conf.TLSClientConfig,
		},
	}
}
