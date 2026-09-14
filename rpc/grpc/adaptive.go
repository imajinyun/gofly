package grpc

import (
	"errors"
	"math"
	"time"

	"github.com/imajinyun/gofly/core/limit"
)

// AdaptiveLimitConfig configures process-local adaptive RPC admission.
type AdaptiveLimitConfig struct {
	Enabled              bool          `json:"enabled"`
	MinLimit             int           `json:"minLimit,omitempty"`
	MaxLimit             int           `json:"maxLimit,omitempty"`
	InitialLimit         int           `json:"initialLimit,omitempty"`
	CPUThresholdPermille int           `json:"cpuThresholdPermille,omitempty"`
	Window               time.Duration `json:"window,omitempty"`
	TargetLatency        time.Duration `json:"targetLatency,omitempty"`
	TargetErrorRatio     float64       `json:"targetErrorRatio,omitempty"`
	MinSamples           int64         `json:"minSamples,omitempty"`
}

func DefaultAdaptiveLimitConfig() AdaptiveLimitConfig {
	return AdaptiveLimitConfig{Enabled: true, MinLimit: 16, MaxLimit: 256, InitialLimit: 64, CPUThresholdPermille: 800, Window: 10 * time.Second, TargetLatency: 100 * time.Millisecond, TargetErrorRatio: 0.05, MinSamples: 20}
}

func (c AdaptiveLimitConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.MinLimit < 1 || c.InitialLimit < c.MinLimit || c.MaxLimit < c.InitialLimit {
		return errors.New("grpc adaptive limit requires 1 <= minLimit <= initialLimit <= maxLimit")
	}
	if c.CPUThresholdPermille < 1 || c.CPUThresholdPermille > 1000 {
		return errors.New("grpc adaptive CPU threshold must be between 1 and 1000 permille")
	}
	if c.Window <= 0 || c.TargetLatency <= 0 || c.MinSamples <= 0 {
		return errors.New("grpc adaptive durations and minSamples must be positive")
	}
	if math.IsNaN(c.TargetErrorRatio) || math.IsInf(c.TargetErrorRatio, 0) || c.TargetErrorRatio < 0 || c.TargetErrorRatio > 1 {
		return errors.New("grpc adaptive targetErrorRatio must be between 0 and 1")
	}
	return nil
}

func (c AdaptiveLimitConfig) NewLimiter(cpuReader func() int) (*limit.AdaptiveLimiter, error) {
	if !c.Enabled {
		return nil, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	opts := []limit.AdaptiveLimiterOption{
		limit.WithAdaptiveLimits(c.MinLimit, c.MaxLimit),
		limit.WithAdaptiveInitialLimit(c.InitialLimit),
		limit.WithAdaptiveCPUThreshold(c.CPUThresholdPermille),
		limit.WithAdaptiveLimitWindow(c.Window),
		limit.WithAdaptiveTargetLatency(c.TargetLatency),
		limit.WithAdaptiveTargetErrorRatio(c.TargetErrorRatio),
		limit.WithAdaptiveMinSamples(c.MinSamples),
	}
	if cpuReader != nil {
		opts = append(opts, limit.WithAdaptiveCPUReader(cpuReader))
	}
	return limit.NewAdaptiveLimiter(opts...), nil
}
