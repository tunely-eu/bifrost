package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type Direction string

const (
	DirectionIngressToEndpoint Direction = "ingress_to_endpoint"
	DirectionEndpointToIngress Direction = "endpoint_to_ingress"
)

const (
	RejectTLSHandshake    = "tls_handshake"
	RejectALPN            = "alpn"
	RejectInvalidHello    = "invalid_hello"
	RejectConnect         = "connect"
	RejectProtocolVersion = "protocol_version"
	RejectHeaders         = "headers"
	RejectAcceptProvider  = "accept_provider"
	RejectDecision        = "decision"
	RejectListener        = "listener"
	RejectNoSession       = "no_active_session"
	RejectSessionNotReady = "session_not_ready"
	RejectStreamLimit     = "stream_limit"
	RejectStreamOpen      = "stream_open"
)

type Observer interface {
	Ready(bool)
	ConnectionAttempted()
	ConnectionRejected(reason string)
	SessionStarted(endpointKey string)
	SessionEnded(endpointKey string)
	StreamStarted(endpointKey string) StreamObserver
	StreamRejected(endpointKey string, reason string)
}

type StreamObserver interface {
	AddBytes(direction Direction, n int64)
	End()
}

type Noop struct{}

func (Noop) Ready(bool)                          {}
func (Noop) ConnectionAttempted()                {}
func (Noop) ConnectionRejected(string)           {}
func (Noop) SessionStarted(string)               {}
func (Noop) SessionEnded(string)                 {}
func (Noop) StreamStarted(string) StreamObserver { return NoopStream{} }
func (Noop) StreamRejected(string, string)       {}

type NoopStream struct{}

func (NoopStream) AddBytes(Direction, int64) {}
func (NoopStream) End()                      {}

type Multi struct {
	observers []Observer
}

func NewMulti(observers ...Observer) Observer {
	filtered := make([]Observer, 0, len(observers))
	for _, observer := range observers {
		if observer != nil {
			filtered = append(filtered, observer)
		}
	}
	if len(filtered) == 0 {
		return Noop{}
	}
	return Multi{observers: filtered}
}

func (m Multi) Ready(ready bool) {
	for _, observer := range m.observers {
		observer.Ready(ready)
	}
}

func (m Multi) ConnectionAttempted() {
	for _, observer := range m.observers {
		observer.ConnectionAttempted()
	}
}

func (m Multi) ConnectionRejected(reason string) {
	for _, observer := range m.observers {
		observer.ConnectionRejected(reason)
	}
}

func (m Multi) SessionStarted(endpointKey string) {
	for _, observer := range m.observers {
		observer.SessionStarted(endpointKey)
	}
}

func (m Multi) SessionEnded(endpointKey string) {
	for _, observer := range m.observers {
		observer.SessionEnded(endpointKey)
	}
}

func (m Multi) StreamStarted(endpointKey string) StreamObserver {
	streams := make([]StreamObserver, 0, len(m.observers))
	for _, observer := range m.observers {
		streams = append(streams, observer.StreamStarted(endpointKey))
	}
	return MultiStream{streams: streams}
}

func (m Multi) StreamRejected(endpointKey string, reason string) {
	for _, observer := range m.observers {
		observer.StreamRejected(endpointKey, reason)
	}
}

func (m Multi) Snapshot() []Sample {
	var out []Sample
	for _, observer := range m.observers {
		snapshotter, ok := observer.(Snapshotter)
		if !ok {
			continue
		}
		out = append(out, snapshotter.Snapshot()...)
	}
	sort.Slice(out, func(i, j int) bool {
		left := sampleSortKey(out[i])
		right := sampleSortKey(out[j])
		return left < right
	})
	return out
}

type MultiStream struct {
	streams []StreamObserver
}

func (m MultiStream) AddBytes(direction Direction, n int64) {
	for _, stream := range m.streams {
		if stream != nil {
			stream.AddBytes(direction, n)
		}
	}
}

func (m MultiStream) End() {
	for _, stream := range m.streams {
		if stream != nil {
			stream.End()
		}
	}
}

type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

type Snapshotter interface {
	Snapshot() []Sample
}

type Memory struct {
	mu     sync.Mutex
	values map[string]memorySample
}

type memorySample struct {
	name   string
	labels map[string]string
	value  float64
}

func NewMemory() *Memory {
	return &Memory{values: make(map[string]memorySample)}
}

func (m *Memory) Ready(ready bool) {
	if ready {
		m.set("ready", nil, 1)
		return
	}
	m.set("ready", nil, 0)
}

func (m *Memory) ConnectionAttempted() {
	m.add("connection_attempts_total", nil, 1)
}

func (m *Memory) ConnectionRejected(reason string) {
	m.add("connection_rejections_total", map[string]string{"reason": safeLabelValue(reason)}, 1)
}

func (m *Memory) SessionStarted(endpointKey string) {
	m.add("active_sessions", nil, 1)
	if endpointKey != "" {
		m.add("endpoint_active_sessions", map[string]string{"endpoint_key": endpointKey}, 1)
	}
}

func (m *Memory) SessionEnded(endpointKey string) {
	m.add("active_sessions", nil, -1)
	if endpointKey != "" {
		m.add("endpoint_active_sessions", map[string]string{"endpoint_key": endpointKey}, -1)
	}
}

func (m *Memory) StreamStarted(endpointKey string) StreamObserver {
	m.add("streams_started_total", nil, 1)
	if endpointKey != "" {
		m.add("endpoint_streams_started_total", map[string]string{"endpoint_key": endpointKey}, 1)
	}
	return &memoryStream{memory: m, endpointKey: endpointKey}
}

func (m *Memory) StreamRejected(endpointKey string, reason string) {
	m.add("stream_rejections_total", map[string]string{"reason": safeLabelValue(reason)}, 1)
	if endpointKey != "" {
		m.add("endpoint_streams_rejected_total", map[string]string{
			"endpoint_key": endpointKey,
			"reason":       safeLabelValue(reason),
		}, 1)
	}
}

func (m *Memory) Snapshot() []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Sample, 0, len(m.values))
	for _, sample := range m.values {
		out = append(out, Sample{
			Name:   sample.name,
			Labels: cloneLabels(sample.labels),
			Value:  sample.value,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left := sampleSortKey(out[i])
		right := sampleSortKey(out[j])
		return left < right
	})
	return out
}

func (m *Memory) add(name string, labels map[string]string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, normalizedName, normalizedLabels := sampleKey(name, labels)
	sample := m.values[key]
	sample.name = normalizedName
	sample.labels = normalizedLabels
	sample.value += value
	m.values[key] = sample
}

func (m *Memory) set(name string, labels map[string]string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, normalizedName, normalizedLabels := sampleKey(name, labels)
	m.values[key] = memorySample{
		name:   normalizedName,
		labels: normalizedLabels,
		value:  value,
	}
}

type memoryStream struct {
	memory      *Memory
	endpointKey string
	once        sync.Once
}

func (s *memoryStream) AddBytes(direction Direction, n int64) {
	if s == nil || s.memory == nil || n <= 0 {
		return
	}
	s.memory.add("bytes_total", nil, float64(n))
	if s.endpointKey != "" {
		s.memory.add("endpoint_stream_bytes_total", map[string]string{
			"endpoint_key": s.endpointKey,
			"direction":    safeLabelValue(string(direction)),
		}, float64(n))
	}
}

func (s *memoryStream) End() {
	if s == nil || s.memory == nil {
		return
	}
	s.once.Do(func() {
		s.memory.add("streams_ended_total", nil, 1)
		if s.endpointKey != "" {
			s.memory.add("endpoint_streams_ended_total", map[string]string{"endpoint_key": s.endpointKey}, 1)
		}
	})
}

func Handler(observer Observer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		snapshotter, ok := observer.(Snapshotter)
		if !ok {
			return
		}
		for _, sample := range snapshotter.Snapshot() {
			fmt.Fprintf(w, "bifrost_%s%s %g\n", sample.Name, formatLabels(sample.Labels), sample.Value)
		}
	})
}

func sampleKey(name string, labels map[string]string) (string, string, map[string]string) {
	normalizedName := sanitizeName(name)
	normalizedLabels := normalizeLabels(labels)
	return sampleSortKey(Sample{Name: normalizedName, Labels: normalizedLabels}), normalizedName, normalizedLabels
}

func sampleSortKey(sample Sample) string {
	return sample.Name + formatLabels(sample.Labels)
}

func normalizeLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(labels))
	for key, value := range labels {
		key = sanitizeName(key)
		if key == "" {
			continue
		}
		normalized[key] = safeLabelValue(value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteString("=\"")
		b.WriteString(escapeLabelValue(labels[key]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	if name == "" {
		return "unknown"
	}
	return name
}

func safeLabelValue(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "unknown"
	}
	return value
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}
