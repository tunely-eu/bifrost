package multiplex

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestYamuxAdapterSupportsParallelStreams(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	serverSession, err := Server(serverConn, 10)
	if err != nil {
		t.Fatalf("Server: %v", err)
	}
	defer serverSession.Close()
	clientSession, err := Client(clientConn, 10)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	defer clientSession.Close()

	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			stream, err := clientSession.Accept()
			if err != nil {
				done <- err
				return
			}
			defer stream.Close()
			_, err = io.Copy(stream, stream)
			done <- err
		}()
	}

	for i := 0; i < 4; i++ {
		stream, err := serverSession.Open()
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_ = stream.SetDeadline(time.Now().Add(time.Second))
		if _, err := stream.Write([]byte("ok")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got := make([]byte, 2)
		if _, err := io.ReadFull(stream, got); err != nil {
			t.Fatalf("ReadFull: %v", err)
		}
		if string(got) != "ok" {
			t.Fatalf("got %q", got)
		}
		_ = stream.Close()
	}
}
