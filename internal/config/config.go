package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tunely-eu/bifrost/internal/acceptor"
	"github.com/tunely-eu/bifrost/internal/header"
	"github.com/tunely-eu/bifrost/internal/limits"
	"github.com/tunely-eu/bifrost/internal/listener"
)

type Duration struct {
	time.Duration
}

func NewDuration(d time.Duration) Duration {
	return Duration{Duration: d}
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Value == "" {
		return nil
	}
	if value.Tag == "!!int" {
		seconds, err := strconv.Atoi(value.Value)
		if err != nil {
			return err
		}
		d.Duration = time.Duration(seconds) * time.Second
		return nil
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", value.Value, err)
	}
	d.Duration = parsed
	return nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("parse duration %q: %w", text, err)
		}
		d.Duration = parsed
		return nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("parse duration seconds %q: %w", raw, err)
	}
	d.Duration = time.Duration(seconds) * time.Second
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.Duration.String(), nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

type ServerConfig struct {
	Server     ServerSection    `json:"server" yaml:"server"`
	Clients    []Client         `json:"clients,omitempty" yaml:"clients,omitempty"`
	Guardrails GuardrailsConfig `json:"guardrails,omitempty" yaml:"guardrails,omitempty"`
	Runtime    RuntimeConfig    `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Logging    Logging          `json:"logging" yaml:"logging"`
	Admin      Admin            `json:"admin" yaml:"admin"`
}

type ServerSection struct {
	Listen string          `json:"listen" yaml:"listen"`
	TLS    ServerTLSConfig `json:"tls" yaml:"tls"`
}

type ServerTLSConfig struct {
	CertFile string `json:"cert_file" yaml:"cert_file"`
	KeyFile  string `json:"key_file" yaml:"key_file"`
}

type Client struct {
	Token            string                    `json:"token" yaml:"token"`
	EndpointKey      string                    `json:"endpoint_key" yaml:"endpoint_key"`
	ConnectionPolicy acceptor.ConnectionPolicy `json:"connection_policy,omitempty" yaml:"connection_policy,omitempty"`
	Limits           limits.PlanLimits         `json:"limits,omitempty" yaml:"limits,omitempty"`
	Labels           map[string]string         `json:"labels,omitempty" yaml:"labels,omitempty"`
	Listener         listener.Spec             `json:"listener,omitempty" yaml:"listener,omitempty"`
}

type GuardrailsConfig struct {
	MaxSessions               int      `json:"max_sessions,omitempty" yaml:"max_sessions,omitempty"`
	MaxStreamsPerSession      int      `json:"max_streams_per_session,omitempty" yaml:"max_streams_per_session,omitempty"`
	MaxBandwidthBPSPerSession int64    `json:"max_bandwidth_bps_per_session,omitempty" yaml:"max_bandwidth_bps_per_session,omitempty"`
	MinStreamIdleTimeout      Duration `json:"min_stream_idle_timeout,omitempty" yaml:"min_stream_idle_timeout,omitempty"`
	MaxStreamIdleTimeout      Duration `json:"max_stream_idle_timeout,omitempty" yaml:"max_stream_idle_timeout,omitempty"`
	MaxHeaders                int      `json:"max_headers,omitempty" yaml:"max_headers,omitempty"`
	MaxHeaderBytes            int      `json:"max_header_bytes,omitempty" yaml:"max_header_bytes,omitempty"`
}

type RuntimeConfig struct {
	HandshakeTimeout        Duration `json:"handshake_timeout,omitempty" yaml:"handshake_timeout,omitempty"`
	StreamCopyBufferBytes   int      `json:"stream_copy_buffer_bytes,omitempty" yaml:"stream_copy_buffer_bytes,omitempty"`
	TunnelKeepAliveInterval Duration `json:"tunnel_keepalive_interval,omitempty" yaml:"tunnel_keepalive_interval,omitempty"`
	TunnelKeepAliveTimeout  Duration `json:"tunnel_keepalive_timeout,omitempty" yaml:"tunnel_keepalive_timeout,omitempty"`
}

type Logging struct {
	Level  string `json:"level,omitempty" yaml:"level,omitempty"`
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
}

type Admin struct {
	Listen string `json:"listen,omitempty" yaml:"listen,omitempty"`
}

type ClientConfig struct {
	Client  ClientSection `json:"client" yaml:"client"`
	Logging Logging       `json:"logging" yaml:"logging"`
	Admin   Admin         `json:"admin" yaml:"admin"`
}

type ClientSection struct {
	ServerURL string            `json:"server_url" yaml:"server_url"`
	Headers   map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Target    Target            `json:"target" yaml:"target"`
	TLS       ClientTLSConfig   `json:"tls" yaml:"tls"`
}

type Target struct {
	Type    string `json:"type" yaml:"type"`
	Address string `json:"address" yaml:"address"`
}

type ClientTLSConfig struct {
	CAFile             string `json:"ca_file,omitempty" yaml:"ca_file,omitempty"`
	ServerName         string `json:"server_name,omitempty" yaml:"server_name,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
}

func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Server: ServerSection{
			Listen: "127.0.0.1:8443",
		},
		Guardrails: GuardrailsConfig{
			MaxSessions:               1000,
			MaxStreamsPerSession:      512,
			MaxBandwidthBPSPerSession: 100_000_000,
			MinStreamIdleTimeout:      NewDuration(30 * time.Second),
			MaxStreamIdleTimeout:      NewDuration(time.Hour),
			MaxHeaders:                32,
			MaxHeaderBytes:            8192,
		},
		Runtime: RuntimeConfig{
			HandshakeTimeout:        NewDuration(10 * time.Second),
			StreamCopyBufferBytes:   32 * 1024,
			TunnelKeepAliveInterval: NewDuration(30 * time.Second),
			TunnelKeepAliveTimeout:  NewDuration(10 * time.Second),
		},
		Logging: Logging{
			Level:  "info",
			Format: "text",
		},
	}
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		Client: ClientSection{
			Headers: map[string]string{},
			Target:  Target{Type: "tcp"},
		},
		Logging: Logging{
			Level:  "info",
			Format: "text",
		},
	}
}

func LoadServerFile(path string) (ServerConfig, error) {
	cfg := DefaultServerConfig()
	if err := loadFile(path, &cfg); err != nil {
		return ServerConfig{}, err
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

func LoadClientFile(path string) (ClientConfig, error) {
	cfg := DefaultClientConfig()
	if err := loadFile(path, &cfg); err != nil {
		return ClientConfig{}, err
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

func (c *ServerConfig) ApplyDefaults() {
	defaults := DefaultServerConfig()
	if c.Server.Listen == "" {
		c.Server.Listen = defaults.Server.Listen
	}
	if c.Guardrails.MaxSessions <= 0 {
		c.Guardrails.MaxSessions = defaults.Guardrails.MaxSessions
	}
	if c.Guardrails.MaxStreamsPerSession <= 0 {
		c.Guardrails.MaxStreamsPerSession = defaults.Guardrails.MaxStreamsPerSession
	}
	if c.Guardrails.MaxBandwidthBPSPerSession <= 0 {
		c.Guardrails.MaxBandwidthBPSPerSession = defaults.Guardrails.MaxBandwidthBPSPerSession
	}
	if c.Guardrails.MinStreamIdleTimeout.Duration <= 0 {
		c.Guardrails.MinStreamIdleTimeout = defaults.Guardrails.MinStreamIdleTimeout
	}
	if c.Guardrails.MaxStreamIdleTimeout.Duration <= 0 {
		c.Guardrails.MaxStreamIdleTimeout = defaults.Guardrails.MaxStreamIdleTimeout
	}
	if c.Guardrails.MaxHeaders <= 0 {
		c.Guardrails.MaxHeaders = defaults.Guardrails.MaxHeaders
	}
	if c.Guardrails.MaxHeaderBytes <= 0 {
		c.Guardrails.MaxHeaderBytes = defaults.Guardrails.MaxHeaderBytes
	}
	if c.Runtime.HandshakeTimeout.Duration <= 0 {
		c.Runtime.HandshakeTimeout = defaults.Runtime.HandshakeTimeout
	}
	if c.Runtime.StreamCopyBufferBytes <= 0 {
		c.Runtime.StreamCopyBufferBytes = defaults.Runtime.StreamCopyBufferBytes
	}
	if c.Runtime.TunnelKeepAliveInterval.Duration <= 0 {
		c.Runtime.TunnelKeepAliveInterval = defaults.Runtime.TunnelKeepAliveInterval
	}
	if c.Runtime.TunnelKeepAliveTimeout.Duration <= 0 {
		c.Runtime.TunnelKeepAliveTimeout = defaults.Runtime.TunnelKeepAliveTimeout
	}
	if c.Logging.Level == "" {
		c.Logging.Level = defaults.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = defaults.Logging.Format
	}
}

func (c *ClientConfig) ApplyDefaults() {
	defaults := DefaultClientConfig()
	if c.Client.Headers == nil {
		c.Client.Headers = map[string]string{}
	}
	if c.Client.Target.Type == "" {
		c.Client.Target.Type = "tcp"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = defaults.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = defaults.Logging.Format
	}
}

func (c ServerConfig) Validate() error {
	return c.ValidateWithProvider(false)
}

func (c ServerConfig) ValidateWithProvider(providerConfigured bool) error {
	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Server.TLS.CertFile == "" {
		return fmt.Errorf("server.tls.cert_file is required")
	}
	if c.Server.TLS.KeyFile == "" {
		return fmt.Errorf("server.tls.key_file is required")
	}
	if c.Guardrails.MaxSessions <= 0 {
		return fmt.Errorf("guardrails.max_sessions must be positive")
	}
	if c.Guardrails.MaxStreamsPerSession <= 0 {
		return fmt.Errorf("guardrails.max_streams_per_session must be positive")
	}
	if c.Guardrails.MaxBandwidthBPSPerSession <= 0 {
		return fmt.Errorf("guardrails.max_bandwidth_bps_per_session must be positive")
	}
	if c.Guardrails.MinStreamIdleTimeout.Duration <= 0 {
		return fmt.Errorf("guardrails.min_stream_idle_timeout must be positive")
	}
	if c.Guardrails.MaxStreamIdleTimeout.Duration < c.Guardrails.MinStreamIdleTimeout.Duration {
		return fmt.Errorf("guardrails.max_stream_idle_timeout must be >= min_stream_idle_timeout")
	}
	if c.Guardrails.MaxHeaders <= 0 {
		return fmt.Errorf("guardrails.max_headers must be positive")
	}
	if c.Guardrails.MaxHeaderBytes <= 0 {
		return fmt.Errorf("guardrails.max_header_bytes must be positive")
	}
	if !providerConfigured && len(c.Clients) == 0 {
		return fmt.Errorf("clients is required")
	}
	if !providerConfigured {
		if _, err := acceptor.NewStaticProvider(c.StaticClients()); err != nil {
			return err
		}
	}
	for index, client := range c.Clients {
		if client.Listener.Type == "" {
			continue
		}
		if err := listener.Validate(client.Listener, listener.Options{
			AllowedUnixPrefixes: []string{"/"},
			AllowPublicTCP:      true,
		}); err != nil {
			return fmt.Errorf("clients[%d].listener: %w", index, err)
		}
	}
	if c.Runtime.HandshakeTimeout.Duration <= 0 {
		return fmt.Errorf("runtime.handshake_timeout must be positive")
	}
	if c.Runtime.StreamCopyBufferBytes <= 0 {
		return fmt.Errorf("runtime.stream_copy_buffer_bytes must be positive")
	}
	if c.Runtime.TunnelKeepAliveInterval.Duration <= 0 {
		return fmt.Errorf("runtime.tunnel_keepalive_interval must be positive")
	}
	if c.Runtime.TunnelKeepAliveTimeout.Duration <= 0 {
		return fmt.Errorf("runtime.tunnel_keepalive_timeout must be positive")
	}
	return nil
}

func (c ServerConfig) StaticClients() []acceptor.StaticClient {
	clients := make([]acceptor.StaticClient, 0, len(c.Clients))
	for _, client := range c.Clients {
		clients = append(clients, acceptor.StaticClient{
			Token:            client.Token,
			EndpointKey:      client.EndpointKey,
			ConnectionPolicy: client.ConnectionPolicy,
			Limits:           client.Limits,
			Labels:           client.Labels,
		})
	}
	return clients
}

func (c ServerConfig) ListenerForEndpoint(endpointKey string) (listener.Spec, bool) {
	for _, client := range c.Clients {
		if client.EndpointKey == endpointKey && client.Listener.Type != "" {
			return client.Listener, true
		}
	}
	return listener.Spec{}, false
}

func (c ClientConfig) Validate() error {
	if err := c.ValidateHandshake(); err != nil {
		return err
	}
	if c.Client.Target.Type != "tcp" {
		return fmt.Errorf("client.target.type must be tcp")
	}
	if c.Client.Target.Address == "" {
		return fmt.Errorf("client.target.address is required")
	}
	return nil
}

func (c ClientConfig) ValidateHandshake() error {
	if c.Client.ServerURL == "" {
		return fmt.Errorf("client.server_url is required")
	}
	if _, err := header.Normalize(c.Client.Headers, 32, 8192); err != nil {
		return err
	}
	return nil
}

func (c GuardrailsConfig) ToLimits() limits.Guardrails {
	return limits.Guardrails{
		MaxSessions:               c.MaxSessions,
		MaxStreamsPerSession:      c.MaxStreamsPerSession,
		MaxBandwidthBPSPerSession: c.MaxBandwidthBPSPerSession,
		MinStreamIdleTimeout:      c.MinStreamIdleTimeout.Duration,
		MaxStreamIdleTimeout:      c.MaxStreamIdleTimeout.Duration,
		MaxHeaders:                c.MaxHeaders,
		MaxHeaderBytes:            c.MaxHeaderBytes,
	}
}

func (c RuntimeConfig) ToLimits() limits.Runtime {
	return limits.Runtime{
		HandshakeTimeout:        c.HandshakeTimeout.Duration,
		StreamCopyBufferBytes:   c.StreamCopyBufferBytes,
		TunnelKeepAliveInterval: c.TunnelKeepAliveInterval.Duration,
		TunnelKeepAliveTimeout:  c.TunnelKeepAliveTimeout.Duration,
	}
}

func loadFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	default:
		if err := yaml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	return nil
}

const ExampleServerYAML = `server:
  listen: "127.0.0.1:8443"
  tls:
    cert_file: "./bifrost/certs/server.crt"
    key_file: "./bifrost/certs/server.key"

clients:
  - token: "dev-secret"
    endpoint_key: "dev"
    listener:
      type: "unix"
      path: "/tmp/bifrost/dev.sock"
      mode: "0600"
    connection_policy:
      mode: "replace_existing"
    limits:
      max_streams: 100

logging:
  level: "info"
  format: "text"
`

const ExampleClientYAML = `client:
  server_url: "127.0.0.1:8443"
  headers:
    X-Bifrost-Token: "dev-secret"
  target:
    type: "tcp"
    address: "127.0.0.1:8080"
  tls:
    ca_file: "./bifrost/certs/ca.crt"
    server_name: "localhost"

logging:
  level: "info"
  format: "text"
`
