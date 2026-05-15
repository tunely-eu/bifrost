package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/tunely-eu/bifrost/internal/config"
	"github.com/tunely-eu/bifrost/internal/header"
	"github.com/tunely-eu/bifrost/internal/logging"
	"github.com/tunely-eu/bifrost/internal/metrics"
	"github.com/tunely-eu/bifrost/internal/multiplex"
	"github.com/tunely-eu/bifrost/internal/pipe"
	"github.com/tunely-eu/bifrost/internal/protocol"
	"github.com/tunely-eu/bifrost/internal/tunnel"
)

type Options struct {
	Logger        *slog.Logger
	Observer      metrics.Observer
	StreamHandler func(context.Context, net.Conn)
	AdminReady    func(net.Addr)
}

const (
	reconnectInitialDelay = time.Second
	reconnectMaxDelay     = 60 * time.Second
)

func Run(ctx context.Context, cfg config.ClientConfig, opts Options) error {
	cfg.ApplyDefaults()
	if opts.StreamHandler == nil {
		if err := cfg.Validate(); err != nil {
			return err
		}
	} else if err := cfg.ValidateHandshake(); err != nil {
		return err
	}
	logger := opts.Logger
	if logger == nil {
		var err error
		logger, err = logging.New(cfg.Logging.Format, cfg.Logging.Level, nil)
		if err != nil {
			return err
		}
	}
	observer := opts.Observer
	if observer == nil {
		if cfg.Admin.Listen != "" {
			observer = metrics.NewMemory()
		} else {
			observer = metrics.Noop{}
		}
	} else if cfg.Admin.Listen != "" {
		observer = metrics.NewMulti(metrics.NewMemory(), observer)
	}
	observer.Ready(true)
	adminAddr, err := tunnel.RunAdmin(ctx, cfg.Admin.Listen, func() bool { return true }, observer, logger)
	if err != nil {
		return err
	}
	if adminAddr != nil && opts.AdminReady != nil {
		opts.AdminReady(adminAddr)
	}

	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return err
	}

	backoff := reconnectInitialDelay
	for {
		err := runOnce(ctx, cfg, tlsConfig, logger, observer, opts.StreamHandler)
		if ctx.Err() != nil {
			observer.Ready(false)
			return nil
		}
		logger.Warn("tunnel disconnected", "error", err)

		delay := jitter(backoff)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			observer.Ready(false)
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if backoff > reconnectMaxDelay {
			backoff = reconnectMaxDelay
		}
	}
}

func runOnce(ctx context.Context, cfg config.ClientConfig, tlsConfig *tls.Config, logger *slog.Logger, observer metrics.Observer, handler func(context.Context, net.Conn)) error {
	var dialer net.Dialer
	observer.ConnectionAttempted()
	raw, err := dialer.DialContext(ctx, "tcp", cfg.Client.ServerURL)
	if err != nil {
		observer.ConnectionRejected(metrics.RejectConnect)
		return err
	}
	defer raw.Close()
	rawDone := closeOnContext(ctx, raw)
	defer rawDone()

	conn := tls.Client(raw, tlsConfig)
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		observer.ConnectionRejected(metrics.RejectTLSHandshake)
		return fmt.Errorf("tls handshake: %w", err)
	}
	if conn.ConnectionState().NegotiatedProtocol != protocol.ALPN {
		observer.ConnectionRejected(metrics.RejectALPN)
		return fmt.Errorf("server negotiated unsupported alpn %q", conn.ConnectionState().NegotiatedProtocol)
	}

	headers, err := header.Normalize(cfg.Client.Headers, 32, 8192)
	if err != nil {
		observer.ConnectionRejected(metrics.RejectHeaders)
		return err
	}
	if err := protocol.WriteJSONLine(conn, protocol.Hello{
		ProtocolVersion: protocol.Version,
		Headers:         headers,
	}, protocol.DefaultMaxLineBytes); err != nil {
		observer.ConnectionRejected(metrics.RejectInvalidHello)
		return err
	}
	var response protocol.Response
	if err := protocol.ReadJSONLine(conn, &response, protocol.DefaultMaxLineBytes); err != nil {
		observer.ConnectionRejected(metrics.RejectInvalidHello)
		return err
	}
	if !response.Accepted {
		if response.Reason == "" {
			response.Reason = "server rejected client"
		}
		observer.ConnectionRejected(metrics.RejectDecision)
		return errors.New(response.Reason)
	}
	_ = conn.SetDeadline(time.Time{})

	session, err := multiplex.Client(conn, 0)
	if err != nil {
		return err
	}
	defer session.Close()

	for {
		stream, err := session.Accept()
		if err != nil {
			return err
		}
		streamObserver := observer.StreamStarted("")
		if streamObserver == nil {
			streamObserver = metrics.NoopStream{}
		}
		if handler != nil {
			go func() {
				defer streamObserver.End()
				handler(ctx, stream)
			}()
			continue
		}
		go forwardStream(ctx, cfg.Client.Target.Address, stream, logger, streamObserver)
	}
}

func forwardStream(ctx context.Context, targetAddr string, stream net.Conn, logger *slog.Logger, streamObserver metrics.StreamObserver) {
	if streamObserver == nil {
		streamObserver = metrics.NoopStream{}
	}
	defer streamObserver.End()

	var dialer net.Dialer
	target, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		logger.Warn("dial local target failed", "target", targetAddr, "error", err)
		_ = stream.Close()
		return
	}
	doneStream := closeOnContext(ctx, stream, target)
	defer doneStream()
	pipe.ProxyWithOptions(ctx, stream, target, pipe.Options{
		OnBytes: func(direction string, n int64) {
			streamObserver.AddBytes(mapClientDirection(direction), n)
		},
	})
}

func mapClientDirection(direction string) metrics.Direction {
	switch direction {
	case "a_to_b":
		return metrics.DirectionIngressToEndpoint
	case "b_to_a":
		return metrics.DirectionEndpointToIngress
	default:
		return metrics.Direction(direction)
	}
}

func closeOnContext(ctx context.Context, conns ...net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			for _, conn := range conns {
				_ = conn.Close()
			}
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func buildTLSConfig(cfg config.ClientConfig) (*tls.Config, error) {
	host, _, err := net.SplitHostPort(cfg.Client.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid client.server_url %q: %w", cfg.Client.ServerURL, err)
	}
	serverName := cfg.Client.TLS.ServerName
	if serverName == "" {
		serverName = host
	}
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		NextProtos:         []string{protocol.ALPN},
		ServerName:         serverName,
		InsecureSkipVerify: cfg.Client.TLS.InsecureSkipVerify,
	}
	if cfg.Client.TLS.CAFile != "" {
		pem, err := os.ReadFile(cfg.Client.TLS.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no CA certificates found in %s", cfg.Client.TLS.CAFile)
		}
		tlsConfig.RootCAs = pool
	}
	return tlsConfig, nil
}

func jitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	min := delay / 2
	extra := time.Duration(rand.Int63n(int64(delay - min + 1)))
	return min + extra
}
