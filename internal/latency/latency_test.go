package latency

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStoreReturnsFreshUnknownAndStaleObservations(t *testing.T) {
	store := NewStore(90 * time.Second)
	observedAt := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	unknown := store.LatencyObservation("home", observedAt)
	if unknown.State != StateUnknown {
		t.Fatalf("unknown state = %q", unknown.State)
	}
	if unknown.LatencyMS != nil || unknown.ObservedAt != nil {
		t.Fatalf("unknown observation carried value: %#v", unknown)
	}

	store.ObserveLatency("home", 18*time.Millisecond, observedAt)

	fresh := store.LatencyObservation("home", observedAt.Add(30*time.Second))
	if fresh.State != StateOK {
		t.Fatalf("fresh state = %q", fresh.State)
	}
	if fresh.LatencyMS == nil || *fresh.LatencyMS != 18 {
		t.Fatalf("fresh latency_ms = %#v", fresh.LatencyMS)
	}
	if fresh.ObservedAt == nil || !fresh.ObservedAt.Equal(observedAt) {
		t.Fatalf("fresh observed_at = %#v", fresh.ObservedAt)
	}

	stale := store.LatencyObservation("home", observedAt.Add(2*time.Minute))
	if stale.State != StateStale {
		t.Fatalf("stale state = %q", stale.State)
	}
	if stale.LatencyMS == nil || *stale.LatencyMS != 18 {
		t.Fatalf("stale latency_ms = %#v", stale.LatencyMS)
	}
}

func TestStoreScopesObservationsByEndpoint(t *testing.T) {
	store := NewStore(time.Minute)
	observedAt := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store.ObserveLatency("home", time.Millisecond, observedAt)
	store.ObserveLatency("files", 2*time.Millisecond, observedAt)

	home := store.LatencyObservation("home", observedAt)
	files := store.LatencyObservation("files", observedAt)
	if home.LatencyMS == nil || files.LatencyMS == nil {
		t.Fatalf("missing latency values: home=%#v files=%#v", home, files)
	}
	if *home.LatencyMS == *files.LatencyMS {
		t.Fatalf("expected endpoint-specific values, got home=%d files=%d", *home.LatencyMS, *files.LatencyMS)
	}

	snapshot := store.LatencySnapshot(observedAt)
	if len(snapshot) != 2 {
		t.Fatalf("snapshot len = %d", len(snapshot))
	}
	if snapshot[0].EndpointKey != "files" || snapshot[1].EndpointKey != "home" {
		t.Fatalf("snapshot order = %#v", snapshot)
	}
}

func TestObservationDoesNotExposeForbiddenFields(t *testing.T) {
	store := NewStore(time.Minute)
	store.ObserveLatency("home", time.Millisecond, time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC))

	body, err := json.Marshal(store.LatencyObservation("home", time.Now()))
	if err != nil {
		t.Fatalf("marshal observation: %v", err)
	}
	payload := string(body)
	for _, forbidden := range []string{
		"token",
		"authorization",
		"cookie",
		"header",
		"body",
		"payload",
		"private_key",
		"remote",
		"ip",
		"sni",
		"hostname",
		"participant",
		"path",
		"content_type",
	} {
		if strings.Contains(strings.ToLower(payload), forbidden) {
			t.Fatalf("payload %s contains forbidden field marker %q", payload, forbidden)
		}
	}
}
