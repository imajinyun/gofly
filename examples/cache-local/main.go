// Command cache-local demonstrates gofly's typed local cache and tiered cache
// capabilities with a deterministic machine-readable report.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/imajinyun/gofly/cache"
	"github.com/imajinyun/gofly/core/bloom"
	"github.com/imajinyun/gofly/core/kv"
)

const reportSchema = "gofly.cache_local.v1"

func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type report struct {
	Schema       string         `json:"schema"`
	Local        localSummary   `json:"local"`
	Negative     negativeReport `json:"negative"`
	Bloom        bloomReport    `json:"bloom"`
	Tiered       tieredReport   `json:"tiered"`
	Disabled     disabledReport `json:"disabled"`
	Capabilities []string       `json:"capabilities"`
	Governance   []string       `json:"governance"`
}

type localSummary struct {
	FirstLoad    string      `json:"firstLoad"`
	SecondLoad   string      `json:"secondLoad"`
	LoaderCalls  int         `json:"loaderCalls"`
	Stats        cache.Stats `json:"stats"`
	PrometheusOK bool        `json:"prometheusOK"`
}

type negativeReport struct {
	Error       string      `json:"error"`
	LoaderCalls int         `json:"loaderCalls"`
	Stats       cache.Stats `json:"stats"`
}

type bloomReport struct {
	GhostError   string      `json:"ghostError"`
	AllowedValue string      `json:"allowedValue"`
	LoaderCalls  int         `json:"loaderCalls"`
	Stats        cache.Stats `json:"stats"`
}

type tieredReport struct {
	FirstLoad        profile     `json:"firstLoad"`
	SecondLoad       profile     `json:"secondLoad"`
	AfterL1Clear     profile     `json:"afterL1Clear"`
	LoaderCalls      int         `json:"loaderCalls"`
	L1Stats          cache.Stats `json:"l1Stats"`
	L2Stats          kv.Snapshot `json:"l2Stats"`
	NamespacedRemote bool        `json:"namespacedRemote"`
}

type disabledReport struct {
	Values      []string    `json:"values"`
	LoaderCalls int         `json:"loaderCalls"`
	Stats       cache.Stats `json:"stats"`
}

type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func main() {
	out, err := buildReport(context.Background())
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(err)
	}
}

func buildReport(ctx context.Context) (report, error) {
	local, err := runLocal(ctx)
	if err != nil {
		return report{}, err
	}
	negative, err := runNegative(ctx)
	if err != nil {
		return report{}, err
	}
	bloomSummary, err := runBloom(ctx)
	if err != nil {
		return report{}, err
	}
	tiered, err := runTiered(ctx)
	if err != nil {
		return report{}, err
	}
	disabled, err := runDisabled(ctx)
	if err != nil {
		return report{}, err
	}
	return report{
		Schema:   reportSchema,
		Local:    local,
		Negative: negative,
		Bloom:    bloomSummary,
		Tiered:   tiered,
		Disabled: disabled,
		Capabilities: sorted([]string{
			"typed-local-cache",
			"loader-fill",
			"negative-cache",
			"bloom-protection",
			"tiered-l1-l2-cache",
			"cache-disabled-mode",
			"stats-and-prometheus",
		}),
		Governance: []string{
			"standalone example module keeps cache demo dependencies out of the root module",
			"tiered cache uses an in-memory kv.Store so the demo has no external service dependency",
			"GOFLY_CACHE_DISABLED or explicit options can bypass cache state for governance runs",
		},
	}, nil
}

func runLocal(ctx context.Context) (localSummary, error) {
	calls := 0
	c := cache.New[string](
		cache.WithName[string]("profiles"),
		cache.WithDefaultTTL[string](time.Minute),
		cache.WithLoader[string](func(_ context.Context, key string) (string, error) {
			calls++
			return "profile:" + key, nil
		}),
	)
	first, err := c.GetOrLoad(ctx, "u:42")
	if err != nil {
		return localSummary{}, err
	}
	second, err := c.GetOrLoad(ctx, "u:42")
	if err != nil {
		return localSummary{}, err
	}
	var sink discardWriter
	if err := c.WritePrometheus(sink); err != nil {
		return localSummary{}, err
	}
	return localSummary{
		FirstLoad:    first,
		SecondLoad:   second,
		LoaderCalls:  calls,
		Stats:        c.Snapshot(),
		PrometheusOK: true,
	}, nil
}

func runNegative(ctx context.Context) (negativeReport, error) {
	calls := 0
	c := cache.New[string](
		cache.WithName[string]("negative"),
		cache.WithNegativeCache[string](time.Minute, nil),
	)
	for i := 0; i < 2; i++ {
		_, err := c.GetOrLoad(ctx, "missing", func(context.Context, string) (string, error) {
			calls++
			return "", cache.ErrNotFound
		})
		if !errors.Is(err, cache.ErrNotFound) {
			return negativeReport{}, err
		}
	}
	return negativeReport{
		Error:       cache.ErrNotFound.Error(),
		LoaderCalls: calls,
		Stats:       c.Snapshot(),
	}, nil
}

func runBloom(ctx context.Context) (bloomReport, error) {
	filter := bloom.New(1000, 0.01)
	filter.AddString("allowed")
	calls := 0
	c := cache.New[string](
		cache.WithName[string]("bloom"),
		cache.WithBloomFilter[string](filter),
	)
	if _, err := c.GetOrLoad(ctx, "ghost", func(context.Context, string) (string, error) {
		calls++
		return "unexpected", nil
	}); !errors.Is(err, cache.ErrNotFound) {
		return bloomReport{}, err
	}
	allowed, err := c.GetOrLoad(ctx, "allowed", func(_ context.Context, key string) (string, error) {
		calls++
		return "value:" + key, nil
	})
	if err != nil {
		return bloomReport{}, err
	}
	return bloomReport{
		GhostError:   cache.ErrNotFound.Error(),
		AllowedValue: allowed,
		LoaderCalls:  calls,
		Stats:        c.Snapshot(),
	}, nil
}

func runTiered(ctx context.Context) (tieredReport, error) {
	l2 := kv.NewMemoryStore()
	calls := 0
	c, err := cache.NewTiered[profile](
		l2,
		cache.WithNamespace[profile]("profiles"),
		cache.WithL2TTL[profile](time.Minute),
		cache.WithTieredLoader[profile](func(_ context.Context, key string) (profile, error) {
			calls++
			return profile{ID: key, Name: "Ada"}, nil
		}),
	)
	if err != nil {
		return tieredReport{}, err
	}
	first, err := c.GetOrLoad(ctx, "u:42")
	if err != nil {
		return tieredReport{}, err
	}
	second, err := c.GetOrLoad(ctx, "u:42")
	if err != nil {
		return tieredReport{}, err
	}
	c.L1().Delete("u:42")
	afterL1Clear, err := c.GetOrLoad(ctx, "u:42")
	if err != nil {
		return tieredReport{}, err
	}
	exists, err := l2.Exists(ctx, "profiles:u:42")
	if err != nil {
		return tieredReport{}, err
	}
	return tieredReport{
		FirstLoad:        first,
		SecondLoad:       second,
		AfterL1Clear:     afterL1Clear,
		LoaderCalls:      calls,
		L1Stats:          c.L1().Snapshot(),
		L2Stats:          l2.Snapshot(),
		NamespacedRemote: exists,
	}, nil
}

func runDisabled(ctx context.Context) (disabledReport, error) {
	calls := 0
	c := cache.New[string](
		cache.WithName[string]("disabled"),
		cache.WithDisabled[string](true),
	)
	values := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		value, err := c.GetOrLoad(ctx, "u:42", func(context.Context, string) (string, error) {
			calls++
			return "load-" + string(rune('0'+calls)), nil
		})
		if err != nil {
			return disabledReport{}, err
		}
		values = append(values, value)
	}
	return disabledReport{
		Values:      values,
		LoaderCalls: calls,
		Stats:       c.Snapshot(),
	}, nil
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
