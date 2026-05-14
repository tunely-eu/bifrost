package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tunely-eu/bifrost/internal/acceptor"
	"github.com/tunely-eu/bifrost/internal/config"
	"github.com/tunely-eu/bifrost/internal/header"
	"github.com/tunely-eu/bifrost/internal/limits"
	"github.com/tunely-eu/bifrost/internal/listener"
	"github.com/tunely-eu/bifrost/internal/logging"
	"github.com/tunely-eu/bifrost/internal/metrics"
	"github.com/tunely-eu/bifrost/internal/multiplex"
	"github.com/tunely-eu/bifrost/internal/pipe"
	"github.com/tunely-eu/bifrost/internal/protocol"
	"github.com/tunely-eu/bifrost/internal/tunnel"
)

type Options struct {
	Logger         *slog.Logger
	Metrics        metrics.Recorder
	AcceptProvider acceptor.Provider
	TLSConfig      *tls.Config
	Ready          func(net.Addr)
	AdminReady     func(net.Addr)
	ListenerReady  func(endpointKey string, spec listener.Spec, addr net.Addr)
}

type Server struct {
	cfg     config.ServerConfig
	opts    Options
	logger  *slog.Logger
	metrics metrics.Recorder
	accept  acceptor.Provider

	mu       sync.Mutex
	wg       sync.WaitGroup
	nextID   uint64
	active   int
	sessions map[string][]*managedSession
	ready    atomic.Bool
}

type managedSession struct {
	id          uint64
	endpointKey string
	cancel      context.CancelFunc
	done        chan struct{}
	listener    *listener.Listener
	mu          sync.RWMutex
	mux         multiplex.Session
	muxReady    chan struct{}
	muxOnce     sync.Once
	limits      limits.PlanLimits
	limiter     *limits.RateLimiter
	streams     chan struct{}
}

func Run(ctx context.Context, cfg config.ServerConfig, opts Options) error {
	s, err := New(cfg, opts)
	if err != nil {
		return err
	}
	return s.Run(ctx)
}

func New(cfg config.ServerConfig, opts Options) (*Server, error) {
	cfg.ApplyDefaults()
	if err := cfg.ValidateWithOptions(config.ValidationOptions{
		ProviderConfigured:    opts.AcceptProvider != nil,
		ExternalTLSConfigured: opts.TLSConfig != nil,
	}); err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		var err error
		logger, err = logging.New(cfg.Logging.Format, cfg.Logging.Level, nil)
		if err != nil {
			return nil, err
		}
	}
	recorder := opts.Metrics
	if recorder == nil {
		recorder = metrics.NewMemory()
	}
	provider := opts.AcceptProvider
	if provider == nil {
		var err error
		provider, err = acceptor.NewStaticProvider(cfg.StaticClients())
		if err != nil {
			return nil, err
		}
	}
	return &Server{
		cfg:      cfg,
		opts:     opts,
		logger:   logger,
		metrics:  recorder,
		accept:   provider,
		sessions: make(map[string][]*managedSession),
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	adminAddr, err := tunnel.RunAdmin(ctx, s.cfg.Admin.Listen, s.ready.Load, s.metrics, s.logger)
	if err != nil {
		return err
	}
	if adminAddr != nil && s.opts.AdminReady != nil {
		s.opts.AdminReady(adminAddr)
	}

	tlsConfig, err := s.tlsConfig()
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", s.cfg.Server.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	s.ready.Store(true)
	s.metrics.Set("ready", 1)
	if s.opts.Ready != nil {
		s.opts.Ready(ln.Addr())
	}

	go func() {
		<-ctx.Done()
		s.ready.Store(false)
		s.metrics.Set("ready", 0)
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.wg.Wait()
				return nil
			default:
			}
			s.wg.Wait()
			return err
		}
		s.metrics.Inc("connection_attempts_total")
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleTunnel(ctx, conn, tlsConfig)
		}()
	}
}

func (s *Server) tlsConfig() (*tls.Config, error) {
	if s.opts.TLSConfig != nil {
		cfg := s.opts.TLSConfig.Clone()
		if cfg.MinVersion == 0 {
			cfg.MinVersion = tls.VersionTLS12
		}
		cfg.NextProtos = appendALPN(cfg.NextProtos, protocol.ALPN)
		return cfg, nil
	}
	cert, err := tls.LoadX509KeyPair(s.cfg.Server.TLS.CertFile, s.cfg.Server.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server tls certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{protocol.ALPN},
	}, nil
}

func appendALPN(nextProtos []string, proto string) []string {
	for _, existing := range nextProtos {
		if existing == proto {
			return nextProtos
		}
	}
	return append(nextProtos, proto)
}

func (s *Server) handleTunnel(parent context.Context, raw net.Conn, tlsConfig *tls.Config) {
	defer raw.Close()
	sessionCtx, sessionCancel := context.WithCancel(parent)
	defer sessionCancel()

	handshakeTimeout := s.cfg.Runtime.HandshakeTimeout.Duration
	if handshakeTimeout <= 0 {
		handshakeTimeout = 10 * time.Second
	}
	_ = raw.SetDeadline(time.Now().Add(handshakeTimeout))
	conn := tls.Server(raw, tlsConfig)
	if err := conn.HandshakeContext(sessionCtx); err != nil {
		s.logger.Warn("tls handshake failed", "remote_addr", raw.RemoteAddr().String(), "error", err)
		s.metrics.Inc("tls_handshake_errors_total")
		return
	}
	if conn.ConnectionState().NegotiatedProtocol != protocol.ALPN {
		s.logger.Warn("unsupported alpn", "remote_addr", raw.RemoteAddr().String(), "alpn", conn.ConnectionState().NegotiatedProtocol)
		s.metrics.Inc("alpn_rejections_total")
		return
	}

	var hello protocol.Hello
	if err := protocol.ReadJSONLine(conn, &hello, s.cfg.Guardrails.MaxHeaderBytes); err != nil {
		s.reject(conn, "invalid handshake")
		s.logger.Warn("read bifrost hello failed", "remote_addr", raw.RemoteAddr().String(), "error", err)
		return
	}
	if hello.ProtocolVersion != protocol.Version {
		s.reject(conn, "unsupported protocol version")
		return
	}
	headers, err := header.Normalize(hello.Headers, s.cfg.Guardrails.MaxHeaders, s.cfg.Guardrails.MaxHeaderBytes)
	if err != nil {
		s.reject(conn, err.Error())
		return
	}
	_ = conn.SetDeadline(time.Time{})

	decision, err := s.accept.Accept(sessionCtx, acceptor.Request{
		RemoteAddr:      raw.RemoteAddr().String(),
		Headers:         headers,
		ProtocolVersion: hello.ProtocolVersion,
		Transport:       "tls",
		ALPN:            protocol.ALPN,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.reject(conn, err.Error())
		return
	}
	decision, err = acceptor.ValidateDecision(decision, s.cfg.Guardrails.ToLimits())
	if err != nil {
		s.reject(conn, err.Error())
		return
	}
	if !decision.Allow {
		reason := decision.Reason
		if reason == "" {
			reason = "client rejected"
		}
		s.metrics.Inc("accept_rejections_total")
		s.reject(conn, reason)
		return
	}

	listenerSpec, hasListener := s.cfg.ListenerForEndpoint(decision.EndpointKey)
	managed := &managedSession{
		id:          s.nextSessionID(),
		endpointKey: decision.EndpointKey,
		done:        make(chan struct{}),
		muxReady:    make(chan struct{}),
		limits:      decision.Limits,
		limiter:     limits.NewRateLimiter(decision.Limits.MaxBandwidthBPS),
		streams:     make(chan struct{}, decision.Limits.MaxStreams),
	}
	managed.cancel = func() {
		sessionCancel()
		if managed.listener != nil {
			_ = managed.listener.Close()
		}
		managed.closeMux()
		_ = conn.Close()
	}

	previous, err := s.registerSession(managed, decision.ConnectionPolicy)
	if err != nil {
		s.reject(conn, err.Error())
		return
	}
	for _, prev := range previous {
		prev.cancel()
		waitDone(parent, prev.done)
	}
	defer func() {
		managed.cancel()
		s.unregisterSession(managed)
		close(managed.done)
	}()

	if hasListener {
		ln, err := listener.Listen(listenerSpec, s.listenerOptions())
		if err != nil {
			s.reject(conn, err.Error())
			return
		}
		managed.listener = ln
		s.metrics.Inc("listeners_opened_total")
		if s.opts.ListenerReady != nil {
			s.opts.ListenerReady(decision.EndpointKey, listenerSpec, ln.Addr())
		}
	}

	if err := protocol.WriteJSONLine(conn, protocol.Response{Accepted: true}, protocol.DefaultMaxLineBytes); err != nil {
		return
	}
	mux, err := multiplex.ServerWithConfig(conn, multiplex.Config{
		MaxStreams:        decision.Limits.MaxStreams,
		KeepAliveInterval: s.cfg.Runtime.TunnelKeepAliveInterval.Duration,
		KeepAliveTimeout:  s.cfg.Runtime.TunnelKeepAliveTimeout.Duration,
	})
	if err != nil {
		s.logger.Warn("start yamux server failed", "endpoint_key", decision.EndpointKey, "error", err)
		return
	}
	managed.setMux(mux)

	if hasListener {
		go s.acceptIngress(sessionCtx, managed)
	}
	select {
	case <-sessionCtx.Done():
	case <-mux.CloseChan():
	}
}

func (s *Server) reject(conn net.Conn, reason string) {
	_ = protocol.WriteJSONLine(conn, protocol.Response{Accepted: false, Reason: reason}, protocol.DefaultMaxLineBytes)
}

func (s *Server) nextSessionID() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return s.nextID
}

func (s *Server) registerSession(session *managedSession, policy acceptor.ConnectionPolicy) ([]*managedSession, error) {
	policy = policy.Normalized()
	if err := policy.Validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := append([]*managedSession(nil), s.sessions[session.endpointKey]...)
	var replaced []*managedSession
	switch policy.Mode {
	case acceptor.PolicyRejectIfExists:
		if len(current) > 0 {
			return nil, fmt.Errorf("endpoint_key %q already has an active session", session.endpointKey)
		}
	case acceptor.PolicyReplaceExisting:
		if len(current) > 0 {
			delete(s.sessions, session.endpointKey)
			s.active -= len(current)
			replaced = current
		}
	case acceptor.PolicyAllowParallel:
		if len(current) >= policy.MaxParallel {
			return nil, fmt.Errorf("endpoint_key %q reached max_parallel %d", session.endpointKey, policy.MaxParallel)
		}
	}
	if s.active >= s.cfg.Guardrails.MaxSessions {
		return nil, fmt.Errorf("server reached max_sessions %d", s.cfg.Guardrails.MaxSessions)
	}
	s.sessions[session.endpointKey] = append(s.sessions[session.endpointKey], session)
	s.active++
	s.metrics.Set("active_sessions", float64(s.active))
	return replaced, nil
}

func (s *Server) unregisterSession(session *managedSession) {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.sessions[session.endpointKey]
	for i, item := range list {
		if item == session {
			list = append(list[:i], list[i+1:]...)
			if len(list) == 0 {
				delete(s.sessions, session.endpointKey)
			} else {
				s.sessions[session.endpointKey] = list
			}
			s.active--
			s.metrics.Set("active_sessions", float64(s.active))
			return
		}
	}
}

func (s *Server) acceptIngress(ctx context.Context, session *managedSession) {
	for {
		conn, err := session.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.logger.Warn("accept listener connection failed", "endpoint_key", session.endpointKey, "error", err)
			return
		}
		go s.forwardIngress(ctx, session, conn)
	}
}

func (s *Server) forwardIngress(ctx context.Context, session *managedSession, ingressConn net.Conn) {
	if err := s.proxySessionStream(ctx, session, ingressConn); err != nil {
		s.logger.Warn("proxy ingress stream failed", "endpoint_key", session.endpointKey, "error", err)
	}
}

func (s *Server) ProxyStream(ctx context.Context, endpointKey string, ingressConn net.Conn) error {
	session, err := s.sessionForEndpoint(endpointKey)
	if err != nil {
		_ = ingressConn.Close()
		return err
	}
	return s.proxySessionStream(ctx, session, ingressConn)
}

func (s *Server) proxySessionStream(ctx context.Context, session *managedSession, ingressConn net.Conn) error {
	stream, err := s.openStream(ctx, session)
	if err != nil {
		_ = ingressConn.Close()
		return err
	}
	pipe.ProxyWithOptions(ctx, ingressConn, stream, pipe.Options{
		BufferSize:  limits.BufferSize(s.cfg.Runtime.StreamCopyBufferBytes),
		IdleTimeout: session.limits.StreamIdleTimeout(),
		Limiter:     session.limiter,
		OnBytes: func(direction string, n int64) {
			s.metrics.Add("bytes_total", float64(n))
		},
	})
	s.metrics.Inc("streams_ended_total")
	return nil
}

func (s *Server) OpenStream(ctx context.Context, endpointKey string) (net.Conn, error) {
	session, err := s.sessionForEndpoint(endpointKey)
	if err != nil {
		return nil, err
	}
	return s.openStream(ctx, session)
}

func (s *Server) sessionForEndpoint(endpointKey string) (*managedSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions := s.sessions[endpointKey]
	if len(sessions) == 0 {
		return nil, fmt.Errorf("endpoint_key %q has no active session", endpointKey)
	}
	return sessions[len(sessions)-1], nil
}

func (s *Server) openStream(ctx context.Context, session *managedSession) (net.Conn, error) {
	mux, err := session.waitMux(ctx)
	if err != nil {
		return nil, err
	}
	if !session.acquireStream() {
		s.metrics.Inc("limit_violations_total")
		return nil, fmt.Errorf("endpoint_key %q reached stream limit", session.endpointKey)
	}
	stream, err := mux.Open()
	if err != nil {
		session.releaseStream()
		return nil, err
	}
	s.metrics.Inc("streams_started_total")
	return &managedStream{
		Conn:    stream,
		release: session.releaseStream,
	}, nil
}

type managedStream struct {
	net.Conn
	once    sync.Once
	release func()
}

func (s *managedStream) Close() error {
	err := s.Conn.Close()
	s.once.Do(s.release)
	return err
}

func (s *Server) listenerOptions() listener.Options {
	return listener.Options{
		AllowedUnixPrefixes: []string{"/"},
		AllowPublicTCP:      true,
		CreateParentDirs:    true,
	}
}

func (session *managedSession) acquireStream() bool {
	select {
	case session.streams <- struct{}{}:
		return true
	default:
		return false
	}
}

func (session *managedSession) releaseStream() {
	select {
	case <-session.streams:
	default:
	}
}

func (session *managedSession) setMux(mux multiplex.Session) {
	session.mu.Lock()
	session.mux = mux
	session.mu.Unlock()
	session.muxOnce.Do(func() {
		close(session.muxReady)
	})
}

func (session *managedSession) waitMux(ctx context.Context) (multiplex.Session, error) {
	select {
	case <-session.muxReady:
	case <-session.done:
		return nil, fmt.Errorf("endpoint_key %q session closed before it was ready", session.endpointKey)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	if session.mux == nil {
		return nil, fmt.Errorf("endpoint_key %q session is not ready", session.endpointKey)
	}
	return session.mux, nil
}

func (session *managedSession) closeMux() {
	session.mu.RLock()
	mux := session.mux
	session.mu.RUnlock()
	if mux != nil {
		_ = mux.Close()
	}
}

func waitDone(ctx context.Context, done <-chan struct{}) {
	select {
	case <-done:
	case <-ctx.Done():
	}
}
