// Package acceptor contains Bifrost connector admission interfaces and the
// static token-backed provider used by standalone deployments.
package acceptor

import (
	"context"
	"fmt"
	"strings"

	"github.com/tunely-eu/bifrost/internal/limits"
)

const (
	// TokenHeader is the normalized connector hello header containing the shared
	// token used by StaticProvider.
	TokenHeader = "x-bifrost-token"

	// PolicyRejectIfExists rejects a new session when an endpoint key is already
	// active.
	PolicyRejectIfExists = "reject_if_exists"
	// PolicyReplaceExisting closes the existing session and lets the new session
	// take ownership of the endpoint key.
	PolicyReplaceExisting = "replace_existing"
	// PolicyAllowParallel allows several active sessions for the same endpoint
	// key, bounded by ConnectionPolicy.MaxParallel.
	PolicyAllowParallel = "allow_parallel"
)

// Provider authorizes connector sessions and returns their endpoint ownership,
// label, and limit decisions.
type Provider interface {
	Accept(context.Context, Request) (Decision, error)
}

// Request describes a connector session after transport-level validation.
type Request struct {
	// RemoteAddr is the connector's remote network address as seen by the server.
	RemoteAddr string `json:"remote_addr"`

	// Headers contains normalized hello headers. Header values are opaque to the
	// tunnel runtime.
	Headers map[string]string `json:"headers"`

	// ProtocolVersion is the Bifrost hello protocol version.
	ProtocolVersion string `json:"protocol_version"`

	// Transport describes the negotiated transport stack.
	Transport string `json:"transport"`

	// ALPN is the TLS application protocol negotiated for this session.
	ALPN string `json:"alpn"`

	// Timestamp is the server-side admission timestamp in RFC3339 format.
	Timestamp string `json:"timestamp"`
}

// ConnectionPolicy controls how competing sessions for an endpoint key are
// handled.
type ConnectionPolicy struct {
	// Mode is one of reject_if_exists, replace_existing, or allow_parallel.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`

	// MaxParallel bounds active sessions when Mode is allow_parallel.
	MaxParallel int `json:"max_parallel,omitempty" yaml:"max_parallel,omitempty"`
}

// Decision is the admission result returned by a Provider.
type Decision struct {
	// Allow decides whether the connector session may proceed.
	Allow bool `json:"allow"`

	// Reason is a diagnostic string for denied sessions. It should not contain
	// secrets.
	Reason string `json:"reason,omitempty"`

	// EndpointKey is the stable endpoint identity used for ownership and stream
	// routing. It is required when Allow is true.
	EndpointKey string `json:"endpoint_key,omitempty"`

	// ConnectionPolicy defines reconnect and competing-session behavior.
	ConnectionPolicy ConnectionPolicy `json:"connection_policy,omitempty"`

	// Limits defines per-session stream, bandwidth, and idle-time limits.
	Limits limits.PlanLimits `json:"limits,omitempty"`

	// Labels carries optional provider metadata for logs and embedding products.
	Labels map[string]string `json:"labels,omitempty"`
}

// StaticClient configures one token-backed connector for StaticProvider.
type StaticClient struct {
	// Token is the shared connector secret matched against TokenHeader.
	Token string `json:"token" yaml:"token"`

	// EndpointKey is the stable identity assigned to sessions using Token.
	EndpointKey string `json:"endpoint_key" yaml:"endpoint_key"`

	// ConnectionPolicy controls reconnect behavior for EndpointKey.
	ConnectionPolicy ConnectionPolicy `json:"connection_policy,omitempty" yaml:"connection_policy,omitempty"`

	// Limits defines per-session stream, bandwidth, and idle-time limits.
	Limits limits.PlanLimits `json:"limits,omitempty" yaml:"limits,omitempty"`

	// Labels carries optional metadata for logs and embedding products.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// StaticProvider authorizes connectors by matching TokenHeader against a static
// in-memory client table.
type StaticProvider struct {
	clientsByToken map[string]StaticClient
}

// NewStaticProvider validates clients and returns a static token-based Provider.
func NewStaticProvider(clients []StaticClient) (*StaticProvider, error) {
	provider := &StaticProvider{clientsByToken: make(map[string]StaticClient, len(clients))}
	for index, client := range clients {
		client.Token = strings.TrimSpace(client.Token)
		client.EndpointKey = strings.TrimSpace(client.EndpointKey)
		if client.Token == "" {
			return nil, fmt.Errorf("clients[%d].token is required", index)
		}
		if client.EndpointKey == "" {
			return nil, fmt.Errorf("clients[%d].endpoint_key is required", index)
		}
		if _, exists := provider.clientsByToken[client.Token]; exists {
			return nil, fmt.Errorf("clients[%d].token duplicates an earlier client", index)
		}
		client.ConnectionPolicy = client.ConnectionPolicy.Normalized()
		if err := client.ConnectionPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("clients[%d].connection_policy: %w", index, err)
		}
		if client.Labels == nil {
			client.Labels = map[string]string{}
		}
		provider.clientsByToken[client.Token] = client
	}
	return provider, nil
}

// Accept returns an allow decision when req contains a known TokenHeader value.
func (p *StaticProvider) Accept(_ context.Context, req Request) (Decision, error) {
	if p == nil {
		return Decision{Allow: false, Reason: "accept provider is not configured"}, nil
	}
	token := req.Headers[TokenHeader]
	if token == "" {
		return Decision{Allow: false, Reason: "missing bifrost token"}, nil
	}
	client, ok := p.clientsByToken[token]
	if !ok {
		return Decision{Allow: false, Reason: "unknown bifrost token"}, nil
	}
	return Decision{
		Allow:            true,
		EndpointKey:      client.EndpointKey,
		ConnectionPolicy: client.ConnectionPolicy,
		Limits:           client.Limits,
		Labels:           cloneLabels(client.Labels),
	}, nil
}

// Normalized fills implicit defaults for a connection policy.
func (p ConnectionPolicy) Normalized() ConnectionPolicy {
	if strings.TrimSpace(p.Mode) == "" {
		p.Mode = PolicyRejectIfExists
	}
	if p.Mode == PolicyAllowParallel && p.MaxParallel <= 0 {
		p.MaxParallel = 1
	}
	return p
}

// Validate checks whether the connection policy uses a supported mode and
// required values for that mode.
func (p ConnectionPolicy) Validate() error {
	switch p.Normalized().Mode {
	case PolicyRejectIfExists, PolicyReplaceExisting:
		return nil
	case PolicyAllowParallel:
		if p.Normalized().MaxParallel <= 0 {
			return fmt.Errorf("connection_policy.max_parallel must be positive")
		}
		return nil
	default:
		return fmt.Errorf("unsupported connection_policy.mode %q", p.Mode)
	}
}

// ValidateDecision applies defaults and server guardrails to an allowed
// provider decision.
func ValidateDecision(decision Decision, guardrails limits.Guardrails) (Decision, error) {
	if !decision.Allow {
		return decision, nil
	}
	if strings.TrimSpace(decision.EndpointKey) == "" {
		return decision, fmt.Errorf("accept provider allowed without endpoint_key")
	}
	decision.EndpointKey = strings.TrimSpace(decision.EndpointKey)
	decision.ConnectionPolicy = decision.ConnectionPolicy.Normalized()
	if err := decision.ConnectionPolicy.Validate(); err != nil {
		return decision, err
	}
	decision.Limits = decision.Limits.WithDefaults(limits.DefaultPlanLimits())
	if err := limits.EnforceGuardrails(decision.Limits, guardrails); err != nil {
		return decision, err
	}
	if decision.Labels == nil {
		decision.Labels = map[string]string{}
	}
	return decision, nil
}

func cloneLabels(labels map[string]string) map[string]string {
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}
