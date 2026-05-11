package limits

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type PlanLimits struct {
	MaxStreams               int   `json:"max_streams,omitempty" yaml:"max_streams,omitempty"`
	MaxBandwidthBPS          int64 `json:"max_bandwidth_bps,omitempty" yaml:"max_bandwidth_bps,omitempty"`
	StreamIdleTimeoutSeconds int   `json:"stream_idle_timeout_seconds,omitempty" yaml:"stream_idle_timeout_seconds,omitempty"`
}

func DefaultPlanLimits() PlanLimits {
	return PlanLimits{
		MaxStreams:               100,
		MaxBandwidthBPS:          25_000_000,
		StreamIdleTimeoutSeconds: 300,
	}
}

func (v PlanLimits) WithDefaults(defaults PlanLimits) PlanLimits {
	if defaults == (PlanLimits{}) {
		defaults = DefaultPlanLimits()
	}
	if v.MaxStreams <= 0 {
		v.MaxStreams = defaults.MaxStreams
	}
	if v.MaxBandwidthBPS <= 0 {
		v.MaxBandwidthBPS = defaults.MaxBandwidthBPS
	}
	if v.StreamIdleTimeoutSeconds <= 0 {
		v.StreamIdleTimeoutSeconds = defaults.StreamIdleTimeoutSeconds
	}
	return v
}

func (v PlanLimits) Validate() error {
	if v.MaxStreams <= 0 {
		return fmt.Errorf("limits.max_streams must be positive")
	}
	if v.MaxBandwidthBPS <= 0 {
		return fmt.Errorf("limits.max_bandwidth_bps must be positive")
	}
	if v.StreamIdleTimeoutSeconds <= 0 {
		return fmt.Errorf("limits.stream_idle_timeout_seconds must be positive")
	}
	return nil
}

func (v PlanLimits) StreamIdleTimeout() time.Duration {
	if v.StreamIdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(v.StreamIdleTimeoutSeconds) * time.Second
}

func (v *PlanLimits) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	type alias PlanLimits
	var out alias
	for key := range raw {
		switch key {
		case "max_streams", "max_bandwidth_bps", "stream_idle_timeout_seconds":
		default:
			return fmt.Errorf("unsupported limits field %q", key)
		}
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*v = PlanLimits(out)
	return nil
}

type Guardrails struct {
	MaxSessions               int   `json:"max_sessions,omitempty" yaml:"max_sessions,omitempty"`
	MaxStreamsPerSession      int   `json:"max_streams_per_session,omitempty" yaml:"max_streams_per_session,omitempty"`
	MaxBandwidthBPSPerSession int64 `json:"max_bandwidth_bps_per_session,omitempty" yaml:"max_bandwidth_bps_per_session,omitempty"`
	MinStreamIdleTimeout      time.Duration
	MaxStreamIdleTimeout      time.Duration
	MaxHeaders                int `json:"max_headers,omitempty" yaml:"max_headers,omitempty"`
	MaxHeaderBytes            int `json:"max_header_bytes,omitempty" yaml:"max_header_bytes,omitempty"`
}

type Runtime struct {
	HandshakeTimeout        time.Duration
	StreamCopyBufferBytes   int
	TunnelKeepAliveInterval time.Duration
	TunnelKeepAliveTimeout  time.Duration
}

func EnforceGuardrails(plan PlanLimits, guardrails Guardrails) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if plan.MaxStreams > guardrails.MaxStreamsPerSession {
		return fmt.Errorf("limits.max_streams exceeds guardrails.max_streams_per_session")
	}
	if plan.MaxBandwidthBPS > guardrails.MaxBandwidthBPSPerSession {
		return fmt.Errorf("limits.max_bandwidth_bps exceeds guardrails.max_bandwidth_bps_per_session")
	}
	idle := plan.StreamIdleTimeout()
	if idle < guardrails.MinStreamIdleTimeout {
		return fmt.Errorf("limits.stream_idle_timeout_seconds is below guardrails.min_stream_idle_timeout")
	}
	if idle > guardrails.MaxStreamIdleTimeout {
		return fmt.Errorf("limits.stream_idle_timeout_seconds exceeds guardrails.max_stream_idle_timeout")
	}
	return nil
}

func BufferSize(bytes int) int {
	if bytes <= 0 || bytes > 32*1024 {
		return 32 * 1024
	}
	return bytes
}

type RateLimiter struct {
	rate     int64
	capacity float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func NewRateLimiter(bytesPerSecond int64) *RateLimiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	capacity := float64(bytesPerSecond)
	now := time.Now()
	return &RateLimiter{
		rate:     bytesPerSecond,
		capacity: capacity,
		tokens:   capacity,
		last:     now,
	}
}

func (l *RateLimiter) Wait(ctx context.Context, bytes int) error {
	if l == nil || bytes <= 0 {
		return nil
	}
	need := float64(bytes)
	for {
		l.mu.Lock()
		if need > l.capacity {
			l.capacity = need
		}
		now := time.Now()
		elapsed := now.Sub(l.last).Seconds()
		if elapsed > 0 {
			l.tokens += elapsed * float64(l.rate)
			if l.tokens > l.capacity {
				l.tokens = l.capacity
			}
			l.last = now
		}
		if l.tokens >= need {
			l.tokens -= need
			l.mu.Unlock()
			return nil
		}
		missing := need - l.tokens
		wait := time.Duration(missing / float64(l.rate) * float64(time.Second))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		l.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
