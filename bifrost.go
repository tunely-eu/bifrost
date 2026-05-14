package bifrost

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"time"

	"github.com/tunely-eu/bifrost/internal/acceptor"
	"github.com/tunely-eu/bifrost/internal/client"
	"github.com/tunely-eu/bifrost/internal/config"
	"github.com/tunely-eu/bifrost/internal/limits"
	"github.com/tunely-eu/bifrost/internal/listener"
	"github.com/tunely-eu/bifrost/internal/metrics"
	"github.com/tunely-eu/bifrost/internal/pipe"
	"github.com/tunely-eu/bifrost/internal/protocol"
	"github.com/tunely-eu/bifrost/internal/server"
)

const (
	TokenHeader = acceptor.TokenHeader
	ALPN        = protocol.ALPN

	PolicyRejectIfExists  = acceptor.PolicyRejectIfExists
	PolicyReplaceExisting = acceptor.PolicyReplaceExisting
	PolicyAllowParallel   = acceptor.PolicyAllowParallel
)

type AcceptProvider = acceptor.Provider
type AcceptRequest = acceptor.Request
type AcceptDecision = acceptor.Decision
type StaticClient = acceptor.StaticClient
type ConnectionPolicy = acceptor.ConnectionPolicy
type PlanLimits = limits.PlanLimits

type ServerConfig struct {
	Listen      string
	TLSCertFile string
	TLSKeyFile  string
	TLSConfig   *tls.Config
	Clients     []StaticClient
	Guardrails  Guardrails
	Runtime     Runtime
	AdminListen string
}

type Guardrails struct {
	MaxSessions               int
	MaxStreamsPerSession      int
	MaxBandwidthBPSPerSession int64
	MinStreamIdleTimeout      time.Duration
	MaxStreamIdleTimeout      time.Duration
	MaxHeaders                int
	MaxHeaderBytes            int
}

type Runtime struct {
	HandshakeTimeout        time.Duration
	StreamCopyBufferBytes   int
	TunnelKeepAliveInterval time.Duration
	TunnelKeepAliveTimeout  time.Duration
}

type ServerOptions struct {
	Logger         *slog.Logger
	AcceptProvider AcceptProvider
	Listener       net.Listener
	Ready          func(net.Addr)
	AdminReady     func(net.Addr)
}

type Server struct {
	inner *server.Server
}

func NewStaticAcceptProvider(clients []StaticClient) (AcceptProvider, error) {
	return acceptor.NewStaticProvider(clients)
}

func NewServer(cfg ServerConfig, opts ServerOptions) (*Server, error) {
	inner, err := server.New(toInternalServerConfig(cfg), server.Options{
		Logger:         opts.Logger,
		Metrics:        metrics.NewMemory(),
		AcceptProvider: opts.AcceptProvider,
		TLSConfig:      cfg.TLSConfig,
		Listener:       opts.Listener,
		Ready:          opts.Ready,
		AdminReady:     opts.AdminReady,
	})
	if err != nil {
		return nil, err
	}
	return &Server{inner: inner}, nil
}

func RunServer(ctx context.Context, cfg ServerConfig, opts ServerOptions) error {
	srv, err := NewServer(cfg, opts)
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

func (s *Server) Run(ctx context.Context) error {
	return s.inner.Run(ctx)
}

func (s *Server) OpenStream(ctx context.Context, endpointKey string) (net.Conn, error) {
	return s.inner.OpenStream(ctx, endpointKey)
}

func (s *Server) ProxyStream(ctx context.Context, endpointKey string, ingressConn net.Conn) error {
	return s.inner.ProxyStream(ctx, endpointKey, ingressConn)
}

type ClientConfig struct {
	ServerURL             string
	Headers               map[string]string
	TargetAddress         string
	TLSCAFile             string
	TLSServerName         string
	TLSInsecureSkipVerify bool
	AdminListen           string
}

type ClientOptions struct {
	Logger        *slog.Logger
	StreamHandler func(context.Context, net.Conn)
	AdminReady    func(net.Addr)
}

func RunClient(ctx context.Context, cfg ClientConfig, opts ClientOptions) error {
	return client.Run(ctx, toInternalClientConfig(cfg), client.Options{
		Logger:        opts.Logger,
		Metrics:       metrics.NewMemory(),
		StreamHandler: opts.StreamHandler,
		AdminReady:    opts.AdminReady,
	})
}

type CopyOptions struct {
	BufferSize  int
	IdleTimeout time.Duration
	OnBytes     func(direction string, n int64)
}

func Copy(ctx context.Context, a net.Conn, b net.Conn, opts CopyOptions) {
	pipe.ProxyWithOptions(ctx, a, b, pipe.Options{
		BufferSize:  opts.BufferSize,
		IdleTimeout: opts.IdleTimeout,
		OnBytes:     opts.OnBytes,
	})
}

func toInternalServerConfig(cfg ServerConfig) config.ServerConfig {
	internal := config.DefaultServerConfig()
	internal.Server.Listen = cfg.Listen
	internal.Server.TLS.CertFile = cfg.TLSCertFile
	internal.Server.TLS.KeyFile = cfg.TLSKeyFile
	internal.Clients = make([]config.Client, 0, len(cfg.Clients))
	for _, client := range cfg.Clients {
		internal.Clients = append(internal.Clients, config.Client{
			Token:            client.Token,
			EndpointKey:      client.EndpointKey,
			ConnectionPolicy: client.ConnectionPolicy,
			Limits:           client.Limits,
			Labels:           client.Labels,
		})
	}
	internal.Admin.Listen = cfg.AdminListen
	applyGuardrails(&internal, cfg.Guardrails)
	applyRuntime(&internal, cfg.Runtime)
	internal.ApplyDefaults()
	return internal
}

func toInternalClientConfig(cfg ClientConfig) config.ClientConfig {
	internal := config.DefaultClientConfig()
	internal.Client.ServerURL = cfg.ServerURL
	internal.Client.Headers = cfg.Headers
	internal.Client.Target = config.Target{Type: "tcp", Address: cfg.TargetAddress}
	internal.Client.TLS.CAFile = cfg.TLSCAFile
	internal.Client.TLS.ServerName = cfg.TLSServerName
	internal.Client.TLS.InsecureSkipVerify = cfg.TLSInsecureSkipVerify
	internal.Admin.Listen = cfg.AdminListen
	internal.ApplyDefaults()
	return internal
}

func applyGuardrails(cfg *config.ServerConfig, guardrails Guardrails) {
	if guardrails.MaxSessions > 0 {
		cfg.Guardrails.MaxSessions = guardrails.MaxSessions
	}
	if guardrails.MaxStreamsPerSession > 0 {
		cfg.Guardrails.MaxStreamsPerSession = guardrails.MaxStreamsPerSession
	}
	if guardrails.MaxBandwidthBPSPerSession > 0 {
		cfg.Guardrails.MaxBandwidthBPSPerSession = guardrails.MaxBandwidthBPSPerSession
	}
	if guardrails.MinStreamIdleTimeout > 0 {
		cfg.Guardrails.MinStreamIdleTimeout = config.NewDuration(guardrails.MinStreamIdleTimeout)
	}
	if guardrails.MaxStreamIdleTimeout > 0 {
		cfg.Guardrails.MaxStreamIdleTimeout = config.NewDuration(guardrails.MaxStreamIdleTimeout)
	}
	if guardrails.MaxHeaders > 0 {
		cfg.Guardrails.MaxHeaders = guardrails.MaxHeaders
	}
	if guardrails.MaxHeaderBytes > 0 {
		cfg.Guardrails.MaxHeaderBytes = guardrails.MaxHeaderBytes
	}
}

func applyRuntime(cfg *config.ServerConfig, runtime Runtime) {
	if runtime.HandshakeTimeout > 0 {
		cfg.Runtime.HandshakeTimeout = config.NewDuration(runtime.HandshakeTimeout)
	}
	if runtime.StreamCopyBufferBytes > 0 {
		cfg.Runtime.StreamCopyBufferBytes = runtime.StreamCopyBufferBytes
	}
	if runtime.TunnelKeepAliveInterval > 0 {
		cfg.Runtime.TunnelKeepAliveInterval = config.NewDuration(runtime.TunnelKeepAliveInterval)
	}
	if runtime.TunnelKeepAliveTimeout > 0 {
		cfg.Runtime.TunnelKeepAliveTimeout = config.NewDuration(runtime.TunnelKeepAliveTimeout)
	}
}

func ListenerSpec(network string, address string) listener.Spec {
	switch network {
	case "unix":
		return listener.Spec{Type: "unix", Path: address}
	case "tcp":
		return listener.Spec{Type: "tcp", Address: address}
	default:
		return listener.Spec{Type: network, Address: address}
	}
}
