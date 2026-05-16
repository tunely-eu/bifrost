// Package limits defines the per-session limits, server guardrails, and small
// runtime helpers used by the Bifrost tunnel data path.
package limits

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// PlanLimits defines the per-session limits granted by an admission decision.
type PlanLimits struct {
	// MaxStreams is the maximum number of concurrent streams in one connector
	// session.
	MaxStreams int `json:"max_streams,omitempty" yaml:"max_streams,omitempty"`

	// MaxBandwidthBPS limits aggregate session bandwidth in bytes per second.
	MaxBandwidthBPS int64 `json:"max_bandwidth_bps,omitempty" yaml:"max_bandwidth_bps,omitempty"`

	// StreamIdleTimeoutSeconds closes a stream after this many idle seconds.
	StreamIdleTimeoutSeconds int `json:"stream_idle_timeout_seconds,omitempty" yaml:"stream_idle_timeout_seconds,omitempty"`
}

// DefaultPlanLimits returns the default per-session limits used when a decision
// omits explicit values.
func DefaultPlanLimits() PlanLimits {
	return PlanLimits{
		MaxStreams:               100,
		MaxBandwidthBPS:          25_000_000,
		StreamIdleTimeoutSeconds: 300,
	}
}

// WithDefaults fills zero-valued fields from defaults and returns the result.
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

// Validate checks that all limit fields are positive after defaults have been
// applied.
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

// StreamIdleTimeout returns StreamIdleTimeoutSeconds as a time.Duration.
func (v PlanLimits) StreamIdleTimeout() time.Duration {
	if v.StreamIdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(v.StreamIdleTimeoutSeconds) * time.Second
}

// UnmarshalJSON rejects unknown limit fields so misspelled configuration does
// not silently fall back to defaults.
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

// Guardrails defines server-wide ceilings for accepted session limits and hello
// metadata.
type Guardrails struct {
	// MaxSessions limits the number of active connector sessions on the server.
	MaxSessions int `json:"max_sessions,omitempty" yaml:"max_sessions,omitempty"`

	// MaxStreamsPerSession is the highest MaxStreams value a decision may grant.
	MaxStreamsPerSession int `json:"max_streams_per_session,omitempty" yaml:"max_streams_per_session,omitempty"`

	// MaxBandwidthBPSPerSession is the highest MaxBandwidthBPS value a decision
	// may grant.
	MaxBandwidthBPSPerSession int64 `json:"max_bandwidth_bps_per_session,omitempty" yaml:"max_bandwidth_bps_per_session,omitempty"`

	// MinStreamIdleTimeout is the shortest stream idle timeout a decision may
	// grant.
	MinStreamIdleTimeout time.Duration

	// MaxStreamIdleTimeout is the longest stream idle timeout a decision may
	// grant.
	MaxStreamIdleTimeout time.Duration

	// MaxHeaders limits the number of hello headers accepted from a connector.
	MaxHeaders int `json:"max_headers,omitempty" yaml:"max_headers,omitempty"`

	// MaxHeaderBytes limits the combined hello header name and value bytes.
	MaxHeaderBytes int `json:"max_header_bytes,omitempty" yaml:"max_header_bytes,omitempty"`
}

// Runtime contains low-level transport tuning values.
type Runtime struct {
	// HandshakeTimeout bounds TLS and protocol hello negotiation.
	HandshakeTimeout time.Duration

	// StreamCopyBufferBytes sets the copy buffer size used for stream proxying.
	StreamCopyBufferBytes int

	// TunnelKeepAliveInterval controls yamux keepalive frequency.
	TunnelKeepAliveInterval time.Duration

	// TunnelKeepAliveTimeout closes a tunnel when keepalive responses stop.
	TunnelKeepAliveTimeout time.Duration
}

// EnforceGuardrails rejects plan values outside configured server guardrails.
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

// BufferSize normalizes a requested stream copy buffer size.
func BufferSize(bytes int) int {
	if bytes <= 0 || bytes > 32*1024 {
		return 32 * 1024
	}
	return bytes
}

// RateLimiter is a simple byte-oriented token bucket.
type RateLimiter struct {
	rate     int64
	capacity float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a token bucket that allows bytesPerSecond aggregate
// throughput. It returns nil when bytesPerSecond is not positive.
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

// Wait blocks until bytes may be consumed or ctx is canceled.
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
