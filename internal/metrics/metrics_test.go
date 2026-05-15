package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMemoryObserverSnapshot(t *testing.T) {
	rec := NewMemory()
	rec.ConnectionAttempted()
	rec.Ready(true)
	rec.SessionStarted("home")
	stream := rec.StreamStarted("home")
	stream.AddBytes(DirectionIngressToEndpoint, 10)
	stream.AddBytes(DirectionEndpointToIngress, 20)
	stream.End()
	rec.StreamRejected("home", RejectStreamLimit)
	rec.SessionEnded("home")

	snapshot := rec.Snapshot()
	assertSample(t, snapshot, "connection_attempts_total", nil, 1)
	assertSample(t, snapshot, "ready", nil, 1)
	assertSample(t, snapshot, "active_sessions", nil, 0)
	assertSample(t, snapshot, "endpoint_active_sessions", map[string]string{"endpoint_key": "home"}, 0)
	assertSample(t, snapshot, "endpoint_streams_started_total", map[string]string{"endpoint_key": "home"}, 1)
	assertSample(t, snapshot, "endpoint_streams_ended_total", map[string]string{"endpoint_key": "home"}, 1)
	assertSample(t, snapshot, "endpoint_stream_bytes_total", map[string]string{
		"endpoint_key": "home",
		"direction":    string(DirectionIngressToEndpoint),
	}, 10)
	assertSample(t, snapshot, "endpoint_stream_bytes_total", map[string]string{
		"endpoint_key": "home",
		"direction":    string(DirectionEndpointToIngress),
	}, 20)
	assertSample(t, snapshot, "endpoint_streams_rejected_total", map[string]string{
		"endpoint_key": "home",
		"reason":       RejectStreamLimit,
	}, 1)
}

func TestHandlerRendersLabelledMetrics(t *testing.T) {
	rec := NewMemory()
	stream := rec.StreamStarted("home")
	stream.AddBytes(DirectionIngressToEndpoint, 10)
	stream.End()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	Handler(rec).ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `bifrost_endpoint_stream_bytes_total{direction="ingress_to_endpoint",endpoint_key="home"} 10`) {
		t.Fatalf("metrics body = %s", body)
	}
}

func TestMultiSnapshotIncludesSnapshotObservers(t *testing.T) {
	rec := NewMemory()
	rec.ConnectionAttempted()

	multi := NewMulti(rec, Noop{})
	snapshotter, ok := multi.(Snapshotter)
	if !ok {
		t.Fatal("multi observer should expose snapshots when it wraps snapshot observers")
	}
	assertSample(t, snapshotter.Snapshot(), "connection_attempts_total", nil, 1)
}

func assertSample(t *testing.T, samples []Sample, name string, labels map[string]string, value float64) {
	t.Helper()
	for _, sample := range samples {
		if sample.Name != name || !labelsEqual(sample.Labels, labels) {
			continue
		}
		if sample.Value != value {
			t.Fatalf("%s%v = %v, want %v", name, labels, sample.Value, value)
		}
		return
	}
	t.Fatalf("missing sample %s%v in %#v", name, labels, samples)
}

func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
