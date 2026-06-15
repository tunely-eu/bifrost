// Package latency stores endpoint-keyed passive session latency observations.
package latency

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// State is the controlled state for a passive latency observation.
type State string

const (
	// StateOK means a fresh passive latency observation is available.
	StateOK State = "ok"
	// StateUnknown means no passive observation is available for the endpoint.
	StateUnknown State = "unknown"
	// StateStale means the latest passive observation is older than the store's
	// freshness window.
	StateStale State = "stale"
)

// Observation is the latest passive latency state for one endpoint.
//
// The payload intentionally contains only the endpoint key, latency value,
// observation time, and controlled state. It does not carry remote addresses,
// headers, tokens, application data, or participant identifiers.
type Observation struct {
	EndpointKey string     `json:"endpoint_key"`
	LatencyMS   *int64     `json:"latency_ms,omitempty"`
	ObservedAt  *time.Time `json:"observed_at,omitempty"`
	State       State      `json:"state"`
}

// Observer receives passive session latency observations.
type Observer interface {
	ObserveLatency(endpointKey string, latency time.Duration, observedAt time.Time)
}

// Multi fans passive latency observations out to several observers.
type Multi struct {
	observers []Observer
}

// NewMulti returns an Observer that calls every non-nil observer in order.
func NewMulti(observers ...Observer) Observer {
	filtered := make([]Observer, 0, len(observers))
	for _, observer := range observers {
		if observer != nil {
			filtered = append(filtered, observer)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return Multi{observers: filtered}
}

// ObserveLatency records one passive latency observation on every child
// observer.
func (m Multi) ObserveLatency(endpointKey string, value time.Duration, observedAt time.Time) {
	for _, observer := range m.observers {
		observer.ObserveLatency(endpointKey, value, observedAt)
	}
}

// Snapshotter exposes endpoint-keyed passive latency state.
type Snapshotter interface {
	LatencyObservation(endpointKey string, now time.Time) Observation
	LatencySnapshot(now time.Time) []Observation
}

type record struct {
	latencyMS  int64
	observedAt time.Time
}

// Store records latest passive latency observations by endpoint key.
type Store struct {
	mu         sync.RWMutex
	staleAfter time.Duration
	records    map[string]record
}

// NewStore returns an endpoint-keyed passive latency store. A non-positive
// staleAfter keeps observations in ok state until replaced or forgotten.
func NewStore(staleAfter time.Duration) *Store {
	return &Store{
		staleAfter: staleAfter,
		records:    make(map[string]record),
	}
}

// ObserveLatency records the latest passive latency for endpointKey.
func (s *Store) ObserveLatency(endpointKey string, value time.Duration, observedAt time.Time) {
	if s == nil {
		return
	}
	endpointKey = strings.TrimSpace(endpointKey)
	if endpointKey == "" || value < 0 {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	observedAt = observedAt.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[endpointKey] = record{
		latencyMS:  durationMillis(value),
		observedAt: observedAt,
	}
}

// LatencyObservation returns the latest passive latency state for endpointKey.
func (s *Store) LatencyObservation(endpointKey string, now time.Time) Observation {
	endpointKey = strings.TrimSpace(endpointKey)
	if s == nil || endpointKey == "" {
		return unknown(endpointKey)
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	s.mu.RLock()
	rec, ok := s.records[endpointKey]
	staleAfter := s.staleAfter
	s.mu.RUnlock()
	if !ok {
		return unknown(endpointKey)
	}
	state := StateOK
	if staleAfter > 0 && now.Sub(rec.observedAt) > staleAfter {
		state = StateStale
	}
	latencyMS := rec.latencyMS
	observedAt := rec.observedAt
	return Observation{
		EndpointKey: endpointKey,
		LatencyMS:   &latencyMS,
		ObservedAt:  &observedAt,
		State:       state,
	}
}

// LatencySnapshot returns latest passive latency state for every observed
// endpoint, sorted by endpoint key.
func (s *Store) LatencySnapshot(now time.Time) []Observation {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	keys := make([]string, 0, len(s.records))
	for endpointKey := range s.records {
		keys = append(keys, endpointKey)
	}
	s.mu.RUnlock()
	sort.Strings(keys)

	out := make([]Observation, 0, len(keys))
	for _, endpointKey := range keys {
		out = append(out, s.LatencyObservation(endpointKey, now))
	}
	return out
}

func unknown(endpointKey string) Observation {
	return Observation{
		EndpointKey: endpointKey,
		State:       StateUnknown,
	}
}

func durationMillis(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	ms := value / time.Millisecond
	if value%time.Millisecond != 0 {
		ms++
	}
	if ms == 0 {
		return 1
	}
	return int64(ms)
}
