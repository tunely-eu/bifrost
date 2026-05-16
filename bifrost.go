// Package bifrost provides the embeddable runtime for Bifrost self-hosted TCP
// tunnels.
//
// A Bifrost deployment has two roles. A server runs on a reachable relay and
// accepts connector sessions. A client runs near a private TCP service and dials
// the server with an outbound TLS connection. Once a connector is admitted, the
// server can open streams to the private target through the tunnel.
//
// The package is intentionally focused on the tunnel data path. It does not
// perform HTTP routing, DNS management, browser-facing TLS termination, account
// management, or application authentication. Those concerns belong to the
// embedding product, reverse proxy, or target service.
//
// Most standalone deployments use the bifrost-server and bifrost-client
// binaries. Use this package when embedding the tunnel runtime into another Go
// program, Caddy module, control plane, or test harness.
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
	// TokenHeader is the normalized hello header name that carries the connector
	// token used by static admission providers.
	TokenHeader = acceptor.TokenHeader

	// ALPN is the TLS application protocol negotiated by Bifrost tunnel
	// connections.
	ALPN = protocol.ALPN

	// PolicyRejectIfExists rejects a new connector session when the endpoint key
	// already has an active session.
	PolicyRejectIfExists = acceptor.PolicyRejectIfExists
	// PolicyReplaceExisting closes an existing session and lets the new
	// connector session take ownership of the endpoint key.
	PolicyReplaceExisting = acceptor.PolicyReplaceExisting
	// PolicyAllowParallel allows multiple active connector sessions for the same
	// endpoint key up to ConnectionPolicy.MaxParallel.
	PolicyAllowParallel = acceptor.PolicyAllowParallel
)

// AcceptProvider decides whether an inbound connector session should be
// accepted and, if accepted, which endpoint, limits, and ownership policy it
// receives.
type AcceptProvider = acceptor.Provider

// AcceptRequest describes a connector session after TLS and protocol hello
// validation. Header names are normalized before the request reaches a provider.
type AcceptRequest = acceptor.Request

// AcceptDecision is the provider result for a connector session.
type AcceptDecision = acceptor.Decision

// StaticClient describes one token-backed connector accepted by
// NewStaticAcceptProvider.
type StaticClient = acceptor.StaticClient

// ConnectionPolicy controls how multiple sessions for the same endpoint key are
// handled.
type ConnectionPolicy = acceptor.ConnectionPolicy

// PlanLimits contains per-session stream, bandwidth, and idle-time limits.
type PlanLimits = limits.PlanLimits

// Observer receives tunnel lifecycle and byte-count events.
type Observer = metrics.Observer

// StreamObserver receives byte-count and end events for one proxied stream.
type StreamObserver = metrics.StreamObserver

// Direction describes the side of a proxied stream that produced bytes.
type Direction = metrics.Direction

// NoopObserver is an Observer implementation that ignores every event.
type NoopObserver = metrics.Noop

// NoopStreamObserver is a StreamObserver implementation that ignores every
// event.
type NoopStreamObserver = metrics.NoopStream

// Listener describes a server-side listener specification. Use ListenerSpec to
// build values without depending on the internal listener package path.
type Listener = listener.Spec

const (
	// DirectionIngressToEndpoint counts bytes flowing from the server-side
	// ingress connection toward the private endpoint.
	DirectionIngressToEndpoint = metrics.DirectionIngressToEndpoint
	// DirectionEndpointToIngress counts bytes flowing from the private endpoint
	// back to the server-side ingress connection.
	DirectionEndpointToIngress = metrics.DirectionEndpointToIngress
)

// NewMultiObserver returns one Observer that fans out events to every non-nil
// observer in order. It returns a no-op observer when no observers are supplied.
func NewMultiObserver(observers ...Observer) Observer {
	return metrics.NewMulti(observers...)
}

// ServerConfig configures a Bifrost relay runtime.
//
// TLSConfig can be supplied by embedders that already manage certificates. When
// TLSConfig is nil, TLSCertFile and TLSKeyFile are used. Clients configures the
// built-in static admission provider unless ServerOptions.AcceptProvider is set.
type ServerConfig struct {
	// Listen is the address used by the server when it creates its own connector
	// listener. It defaults to the internal server default when empty.
	Listen string

	// TLSCertFile is the certificate file presented to connector clients when
	// TLSConfig is nil.
	TLSCertFile string

	// TLSKeyFile is the private key file for TLSCertFile when TLSConfig is nil.
	TLSKeyFile string

	// TLSConfig is an optional externally managed TLS configuration. Embedders
	// should include ALPN in NextProtos or use a TLS policy that negotiates ALPN.
	TLSConfig *tls.Config

	// Clients defines token-backed static connector sessions. It is ignored when
	// ServerOptions.AcceptProvider is set.
	Clients []StaticClient

	// Guardrails sets server-wide ceilings enforced after every admission
	// decision.
	Guardrails Guardrails

	// Runtime tunes handshake, copy buffer, and tunnel keepalive behavior.
	Runtime Runtime

	// AdminListen enables the optional admin HTTP listener for readiness and
	// Prometheus-style metrics when non-empty.
	AdminListen string
}

// Guardrails sets server-wide ceilings for sessions and per-session decisions.
//
// Zero fields use the runtime defaults. Guardrails are enforced after provider
// defaults are applied, so a custom AcceptProvider cannot grant limits above
// these ceilings.
type Guardrails struct {
	// MaxSessions limits active connector sessions on the server.
	MaxSessions int

	// MaxStreamsPerSession is the upper bound for a session's concurrent stream
	// limit.
	MaxStreamsPerSession int

	// MaxBandwidthBPSPerSession is the upper bound for a session's byte-per-second
	// bandwidth limit.
	MaxBandwidthBPSPerSession int64

	// MinStreamIdleTimeout is the shortest idle timeout a provider decision may
	// request.
	MinStreamIdleTimeout time.Duration

	// MaxStreamIdleTimeout is the longest idle timeout a provider decision may
	// request.
	MaxStreamIdleTimeout time.Duration

	// MaxHeaders limits the number of hello headers accepted from a connector.
	MaxHeaders int

	// MaxHeaderBytes limits the combined size of hello header names and values.
	MaxHeaderBytes int
}

// Runtime contains low-level tunnel runtime tuning.
//
// Zero fields use the runtime defaults.
type Runtime struct {
	// HandshakeTimeout bounds TLS and Bifrost hello negotiation work.
	HandshakeTimeout time.Duration

	// StreamCopyBufferBytes sets the copy buffer size used for proxied streams.
	StreamCopyBufferBytes int

	// TunnelKeepAliveInterval is the yamux keepalive ping interval.
	TunnelKeepAliveInterval time.Duration

	// TunnelKeepAliveTimeout is the maximum wait for a keepalive response before
	// closing the tunnel session.
	TunnelKeepAliveTimeout time.Duration
}

// ServerOptions supplies dependencies and callbacks for an embedded server.
type ServerOptions struct {
	// Logger receives structured runtime logs. A nil logger uses the internal
	// default.
	Logger *slog.Logger

	// AcceptProvider overrides ServerConfig.Clients with custom admission logic.
	AcceptProvider AcceptProvider

	// Observer receives lifecycle and byte-count events. It may be nil.
	Observer Observer

	// Listener supplies an already-created connector listener. When set, Listen in
	// ServerConfig is informational and the server does not create a listener.
	Listener net.Listener

	// Ready is called with the connector listener address after the server is
	// accepting connector sessions.
	Ready func(net.Addr)

	// AdminReady is called with the admin listener address when AdminListen is
	// enabled and ready.
	AdminReady func(net.Addr)
}

// Server is an embedded Bifrost relay runtime.
type Server struct {
	inner *server.Server
}

// NewStaticAcceptProvider builds an AcceptProvider from static token-backed
// clients. The provider rejects missing, unknown, and duplicate tokens.
func NewStaticAcceptProvider(clients []StaticClient) (AcceptProvider, error) {
	return acceptor.NewStaticProvider(clients)
}

// NewServer validates the server configuration and returns a relay runtime ready
// to be started with Server.Run.
func NewServer(cfg ServerConfig, opts ServerOptions) (*Server, error) {
	inner, err := server.New(toInternalServerConfig(cfg), server.Options{
		Logger:         opts.Logger,
		Observer:       opts.Observer,
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

// RunServer constructs and runs a Server until ctx is canceled or the server
// returns an error.
func RunServer(ctx context.Context, cfg ServerConfig, opts ServerOptions) error {
	srv, err := NewServer(cfg, opts)
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

// Run starts the server and blocks until ctx is canceled or the server stops
// with an error.
func (s *Server) Run(ctx context.Context) error {
	return s.inner.Run(ctx)
}

// OpenStream opens a new stream to an active endpoint and returns it as a
// net.Conn. The endpoint must have an admitted connector session.
func (s *Server) OpenStream(ctx context.Context, endpointKey string) (net.Conn, error) {
	return s.inner.OpenStream(ctx, endpointKey)
}

// ProxyStream attaches ingressConn to a new stream for endpointKey and copies
// bytes in both directions until either side closes or ctx is canceled.
func (s *Server) ProxyStream(ctx context.Context, endpointKey string, ingressConn net.Conn) error {
	return s.inner.ProxyStream(ctx, endpointKey, ingressConn)
}

// ClientConfig configures a Bifrost connector runtime.
type ClientConfig struct {
	// ServerURL is the relay address in host:port form.
	ServerURL string

	// Headers contains additional hello headers sent to the relay. TokenHeader is
	// managed by the CLI configuration path and should not be duplicated here.
	Headers map[string]string

	// TargetAddress is the private TCP target reached for every accepted stream.
	TargetAddress string

	// TLSCAFile optionally points at a CA bundle used to verify the relay
	// certificate. When empty, the system trust store is used.
	TLSCAFile string

	// TLSServerName overrides the TLS server name used to verify the relay
	// certificate.
	TLSServerName string

	// TLSInsecureSkipVerify disables relay certificate verification. It is
	// development-only.
	TLSInsecureSkipVerify bool

	// AdminListen enables the optional admin HTTP listener for readiness and
	// metrics when non-empty.
	AdminListen string
}

// ClientOptions supplies dependencies and callbacks for an embedded connector.
type ClientOptions struct {
	// Logger receives structured runtime logs. A nil logger uses the internal
	// default.
	Logger *slog.Logger

	// Observer receives lifecycle and byte-count events. It may be nil.
	Observer Observer

	// StreamHandler overrides the default TCP target dialer. Embedders can use it
	// to handle accepted streams in-process instead of dialing TargetAddress.
	StreamHandler func(context.Context, net.Conn)

	// AdminReady is called with the admin listener address when AdminListen is
	// enabled and ready.
	AdminReady func(net.Addr)
}

// RunClient runs a connector until ctx is canceled. The connector maintains an
// outbound session to ServerURL and forwards each accepted stream to
// TargetAddress, unless StreamHandler is set in opts.
func RunClient(ctx context.Context, cfg ClientConfig, opts ClientOptions) error {
	return client.Run(ctx, toInternalClientConfig(cfg), client.Options{
		Logger:        opts.Logger,
		Observer:      opts.Observer,
		StreamHandler: opts.StreamHandler,
		AdminReady:    opts.AdminReady,
	})
}

// CopyOptions tunes Copy.
type CopyOptions struct {
	// BufferSize sets the copy buffer size. A zero or invalid value uses the
	// runtime default.
	BufferSize int

	// IdleTimeout closes the proxy after both directions have been idle for the
	// configured duration. Zero disables idle timeout handling.
	IdleTimeout time.Duration

	// OnBytes is called with the copy direction and byte count for each successful
	// copy chunk.
	OnBytes func(direction string, n int64)
}

// Copy proxies bytes between a and b until one side closes, ctx is canceled, or
// the idle timeout is reached. Copy closes both connections before returning.
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

// ListenerSpec returns a listener specification for the supplied network and
// address. Supported networks are "unix" and "tcp"; unknown networks are passed
// through as a listener type with address set.
func ListenerSpec(network string, address string) Listener {
	switch network {
	case "unix":
		return listener.Spec{Type: "unix", Path: address}
	case "tcp":
		return listener.Spec{Type: "tcp", Address: address}
	default:
		return listener.Spec{Type: network, Address: address}
	}
}
