package multiplex

import (
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

type Config struct {
	MaxStreams        int
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
	LatencyObserver   func(time.Duration, time.Time)
}

type Session interface {
	Open() (net.Conn, error)
	Accept() (net.Conn, error)
	Close() error
	CloseChan() <-chan struct{}
}

func Server(conn net.Conn, maxStreams int) (Session, error) {
	return ServerWithConfig(conn, Config{MaxStreams: maxStreams})
}

func Client(conn net.Conn, maxStreams int) (Session, error) {
	return ClientWithConfig(conn, Config{MaxStreams: maxStreams})
}

func ServerWithConfig(conn net.Conn, cfg Config) (Session, error) {
	session, err := yamux.Server(conn, yamuxConfig(cfg))
	if err != nil {
		return nil, err
	}
	startObservedKeepalive(session, cfg)
	return session, nil
}

func ClientWithConfig(conn net.Conn, cfg Config) (Session, error) {
	return yamux.Client(conn, yamuxConfig(cfg))
}

func yamuxConfig(options Config) *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	if options.MaxStreams > 0 {
		cfg.AcceptBacklog = options.MaxStreams
	}
	if options.KeepAliveInterval > 0 {
		cfg.EnableKeepAlive = true
		cfg.KeepAliveInterval = options.KeepAliveInterval
	}
	if options.KeepAliveTimeout > 0 {
		cfg.ConnectionWriteTimeout = options.KeepAliveTimeout
	}
	if options.LatencyObserver != nil {
		cfg.EnableKeepAlive = false
	}
	return cfg
}

func startObservedKeepalive(session *yamux.Session, cfg Config) {
	if cfg.LatencyObserver == nil {
		return
	}
	interval := cfg.KeepAliveInterval
	if interval <= 0 {
		interval = yamux.DefaultConfig().KeepAliveInterval
	}
	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				rtt, err := session.Ping()
				if err != nil {
					_ = session.Close()
					return
				}
				cfg.LatencyObserver(rtt, time.Now().UTC())
				timer.Reset(interval)
			case <-session.CloseChan():
				return
			}
		}
	}()
}
