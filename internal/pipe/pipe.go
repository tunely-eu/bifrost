package pipe

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

type closeWriter interface {
	CloseWrite() error
}

type Limiter interface {
	Wait(context.Context, int) error
}

type Options struct {
	BufferSize  int
	IdleTimeout time.Duration
	Limiter     Limiter
	OnBytes     func(direction string, n int64)
}

func Proxy(a, b net.Conn) {
	ProxyWithOptions(context.Background(), a, b, Options{})
}

func ProxyWithOptions(ctx context.Context, a, b net.Conn, opts Options) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		copyConn(ctx, a, b, "b_to_a", opts)
		closeWrite(a)
	}()

	go func() {
		defer wg.Done()
		copyConn(ctx, b, a, "a_to_b", opts)
		closeWrite(b)
	}()

	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

func copyConn(ctx context.Context, dst, src net.Conn, direction string, opts Options) {
	bufferSize := opts.BufferSize
	if bufferSize <= 0 {
		bufferSize = 32 * 1024
	}
	buf := make([]byte, bufferSize)
	for {
		if opts.IdleTimeout > 0 {
			_ = src.SetReadDeadline(time.Now().Add(opts.IdleTimeout))
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if opts.Limiter != nil {
				if err := opts.Limiter.Wait(ctx, n); err != nil {
					return
				}
			}
			if err := writeFull(dst, buf[:n]); err != nil {
				return
			}
			if opts.OnBytes != nil {
				opts.OnBytes(direction, int64(n))
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func writeFull(dst net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func closeWrite(conn net.Conn) {
	if writer, ok := conn.(closeWriter); ok {
		_ = writer.CloseWrite()
		return
	}
	_ = conn.Close()
}
