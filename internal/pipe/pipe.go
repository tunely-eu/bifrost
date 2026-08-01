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
	var activity *idleActivity
	if opts.IdleTimeout > 0 {
		activity = newIdleActivity()
	}

	watchDone := make(chan struct{})
	watchStopped := make(chan struct{})
	go func() {
		defer close(watchStopped)
		watchProxy(ctx, a, b, opts.IdleTimeout, activity, watchDone)
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		copyConn(ctx, a, b, "b_to_a", opts, activity)
		closeWrite(a)
	}()

	go func() {
		defer wg.Done()
		copyConn(ctx, b, a, "a_to_b", opts, activity)
		closeWrite(b)
	}()

	wg.Wait()
	close(watchDone)
	<-watchStopped
	_ = a.Close()
	_ = b.Close()
}

type idleActivity struct {
	mu           sync.Mutex
	lastActivity time.Time
	wake         chan struct{}
}

func newIdleActivity() *idleActivity {
	return &idleActivity{
		lastActivity: time.Now(),
		wake:         make(chan struct{}, 1),
	}
}

func (a *idleActivity) touch() {
	a.mu.Lock()
	a.lastActivity = time.Now()
	a.mu.Unlock()

	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *idleActivity) remaining(timeout time.Duration) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return timeout - time.Since(a.lastActivity)
}

func watchProxy(ctx context.Context, a, b net.Conn, idleTimeout time.Duration, activity *idleActivity, done <-chan struct{}) {
	var activityWake <-chan struct{}
	var idleTimer *time.Timer
	var idleTimerC <-chan time.Time
	if activity != nil {
		activityWake = activity.wake
		idleTimer = time.NewTimer(idleTimeout)
		idleTimerC = idleTimer.C
		defer idleTimer.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			closeBoth(a, b)
			return
		case <-done:
			return
		case <-activityWake:
			resetTimer(idleTimer, activity.remaining(idleTimeout))
		case <-idleTimerC:
			remaining := activity.remaining(idleTimeout)
			if remaining <= 0 {
				closeBoth(a, b)
				return
			}
			resetTimer(idleTimer, remaining)
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if duration <= 0 {
		duration = time.Nanosecond
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func closeBoth(a, b net.Conn) {
	_ = a.Close()
	_ = b.Close()
}

func copyConn(ctx context.Context, dst, src net.Conn, direction string, opts Options, activity *idleActivity) {
	bufferSize := opts.BufferSize
	if bufferSize <= 0 {
		bufferSize = 32 * 1024
	}
	buf := make([]byte, bufferSize)
	for {
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
			if activity != nil {
				activity.touch()
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
