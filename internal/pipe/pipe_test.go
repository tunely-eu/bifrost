package pipe

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestProxyWithOptionsOneWayActivityPreventsIdleTimeout(t *testing.T) {
	left, proxyLeft := net.Pipe()
	proxyRight, right := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ProxyWithOptions(ctx, proxyLeft, proxyRight, Options{IdleTimeout: 200 * time.Millisecond})
	}()
	t.Cleanup(func() {
		cancel()
		_ = left.Close()
		_ = right.Close()
		<-done
	})

	end := time.Now().Add(600 * time.Millisecond)
	for sequence := byte(0); time.Now().Before(end); sequence++ {
		writeAndRead(t, left, right, []byte{sequence})
	}

	select {
	case <-done:
		t.Fatal("proxy closed while bytes were still flowing in one direction")
	default:
	}
	writeAndRead(t, left, right, []byte("still-open"))
}

func TestProxyWithOptionsClosesAfterBothDirectionsAreIdle(t *testing.T) {
	left, proxyLeft := net.Pipe()
	proxyRight, right := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ProxyWithOptions(context.Background(), proxyLeft, proxyRight, Options{IdleTimeout: 50 * time.Millisecond})
	}()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("proxy remained open after both directions were idle")
	}
}

func TestProxyWithOptionsContextCancellationClosesIdleConnections(t *testing.T) {
	left, proxyLeft := net.Pipe()
	proxyRight, right := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ProxyWithOptions(ctx, proxyLeft, proxyRight, Options{})
	}()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("proxy remained open after context cancellation")
	}
}

func writeAndRead(t *testing.T, writer, reader net.Conn, payload []byte) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	if err := writer.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("set write deadline: %v", err)
	}
	if err := reader.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}
