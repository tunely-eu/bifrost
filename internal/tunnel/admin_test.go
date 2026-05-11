package tunnel

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/tunely-eu/bifrost/internal/metrics"
)

func TestAdminHealthReadyMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := metrics.NewMemory()
	rec.Inc("connection_attempts_total")
	addr, err := RunAdmin(ctx, "127.0.0.1:0", func() bool { return true }, rec, nil)
	if err != nil {
		t.Fatalf("RunAdmin: %v", err)
	}
	base := "http://" + addr.String()
	assertHTTPStatus(t, base+"/healthz", http.StatusOK)
	assertHTTPStatus(t, base+"/readyz", http.StatusOK)
	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) == "" {
		t.Fatal("expected metrics body")
	}
}

func TestAdminReadyzUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := RunAdmin(ctx, "127.0.0.1:0", func() bool { return false }, metrics.NewMemory(), nil)
	if err != nil {
		t.Fatalf("RunAdmin: %v", err)
	}
	assertHTTPStatus(t, "http://"+addr.String()+"/readyz", http.StatusServiceUnavailable)
}

func assertHTTPStatus(t *testing.T, url string, status int) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("%s status = %d", url, resp.StatusCode)
	}
}
