package metrics

import "testing"

func TestMemoryRecorderSnapshot(t *testing.T) {
	rec := NewMemory()
	rec.Inc("connections_total")
	rec.Add("bytes_total", 10)
	rec.Set("active_sessions", 2)
	snapshot := rec.Snapshot()
	if snapshot["connections_total"] != 1 {
		t.Fatalf("connections_total = %v", snapshot["connections_total"])
	}
	if snapshot["bytes_total"] != 10 {
		t.Fatalf("bytes_total = %v", snapshot["bytes_total"])
	}
	if snapshot["active_sessions"] != 2 {
		t.Fatalf("active_sessions = %v", snapshot["active_sessions"])
	}
}
