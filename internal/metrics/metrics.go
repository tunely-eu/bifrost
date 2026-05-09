package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type Recorder interface {
	Inc(name string)
	Add(name string, value float64)
	Set(name string, value float64)
}

type Snapshotter interface {
	Snapshot() map[string]float64
}

type Noop struct{}

func (Noop) Inc(string)                   {}
func (Noop) Add(string, float64)          {}
func (Noop) Set(string, float64)          {}
func (Noop) Snapshot() map[string]float64 { return map[string]float64{} }

type Memory struct {
	mu     sync.Mutex
	values map[string]float64
}

func NewMemory() *Memory {
	return &Memory{values: make(map[string]float64)}
}

func (m *Memory) Inc(name string) {
	m.Add(name, 1)
}

func (m *Memory) Add(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[sanitize(name)] += value
}

func (m *Memory) Set(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[sanitize(name)] = value
}

func (m *Memory) Snapshot() map[string]float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]float64, len(m.values))
	for key, value := range m.values {
		out[key] = value
	}
	return out
}

func Handler(rec Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		snapshotter, ok := rec.(Snapshotter)
		if !ok {
			return
		}
		snapshot := snapshotter.Snapshot()
		names := make([]string, 0, len(snapshot))
		for name := range snapshot {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(w, "bifrost_%s %g\n", name, snapshot[name])
		}
	})
}

func sanitize(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	if name == "" {
		return "unknown"
	}
	return name
}
