// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DNSResolver struct {
	host          string
	port          int
	scheme        string
	lookupIP      func(context.Context, string) ([]net.IP, error)
	watchInterval time.Duration
	mu            sync.RWMutex
	endpoints     []string
	removed       []string
	err           error
	updates       int64
	lastUpdated   time.Time
}

type DNSResolverConfig struct {
	Host          string
	Port          int
	Scheme        string
	LookupIP      func(context.Context, string) ([]net.IP, error)
	WatchInterval time.Duration
}

func NewDNSResolver(conf DNSResolverConfig) (*DNSResolver, error) {
	conf.Host = strings.TrimSpace(conf.Host)
	if conf.Host == "" {
		return nil, errors.New("dns host is required")
	}
	if conf.Port <= 0 || conf.Port > 65535 {
		return nil, errors.New("dns port must be between 1 and 65535")
	}
	conf.Scheme = strings.TrimSpace(conf.Scheme)
	if conf.Scheme == "" {
		conf.Scheme = "http"
	}
	if conf.LookupIP == nil {
		resolver := net.DefaultResolver
		conf.LookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
			return resolver.LookupIP(ctx, "ip", host)
		}
	}
	if conf.WatchInterval <= 0 {
		conf.WatchInterval = 5 * time.Second
	}
	return &DNSResolver{
		host:          conf.Host,
		port:          conf.Port,
		scheme:        conf.Scheme,
		lookupIP:      conf.LookupIP,
		watchInterval: conf.WatchInterval,
	}, nil
}

func (r *DNSResolver) Resolve(ctx context.Context) ([]string, error) {
	if r == nil {
		return nil, errors.New("dns resolver is nil")
	}
	if err := ctx.Err(); err != nil {
		r.record(nil, err)
		return nil, err
	}
	ips, err := r.lookupIP(ctx, r.host)
	if err != nil {
		err = fmt.Errorf("lookup dns host %q: %w", r.host, err)
		r.record(nil, err)
		return nil, err
	}
	endpoints := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == nil || ip.IsUnspecified() {
			continue
		}
		host := ip.String()
		endpoints = append(endpoints, r.scheme+"://"+net.JoinHostPort(host, strconv.Itoa(r.port)))
	}
	endpoints = normalizeEndpoints(endpoints)
	sort.Strings(endpoints)
	if len(endpoints) == 0 {
		err := errors.New("no rpc endpoints resolved")
		r.record(nil, err)
		return nil, err
	}
	r.record(endpoints, nil)
	return endpoints, nil
}

func (r *DNSResolver) Watch(ctx context.Context) (<-chan []string, error) {
	if r == nil {
		return nil, errors.New("dns resolver is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(chan []string, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(r.watchInterval)
		defer ticker.Stop()
		var last []string
		emit := func() {
			endpoints, err := r.Resolve(ctx)
			if err != nil || sameEndpoints(last, endpoints) {
				return
			}
			last = append([]string(nil), endpoints...)
			select {
			case out <- endpoints:
			case <-ctx.Done():
			}
		}
		emit()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				emit()
			}
		}
	}()
	return out, nil
}

func (r *DNSResolver) Snapshot() ResolverSnapshot {
	if r == nil {
		return ResolverSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := ResolverSnapshot{
		Endpoints:   append([]string(nil), r.endpoints...),
		Removed:     append([]string(nil), r.removed...),
		Updates:     r.updates,
		LastUpdated: r.lastUpdated,
	}
	if r.err != nil {
		snapshot.Error = r.err.Error()
	}
	return snapshot
}

func (r *DNSResolver) record(endpoints []string, err error) {
	if r == nil {
		return
	}
	endpoints = normalizeEndpoints(endpoints)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.removed = removedEndpoints(r.endpoints, endpoints)
		r.endpoints = append([]string(nil), endpoints...)
	}
	r.err = err
	r.updates++
	r.lastUpdated = time.Now()
}
