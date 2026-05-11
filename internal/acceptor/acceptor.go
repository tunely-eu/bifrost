package acceptor

import (
	"context"
	"fmt"
	"strings"

	"github.com/tunely-eu/bifrost/internal/limits"
)

const (
	TokenHeader = "x-bifrost-token"

	PolicyRejectIfExists  = "reject_if_exists"
	PolicyReplaceExisting = "replace_existing"
	PolicyAllowParallel   = "allow_parallel"
)

type Provider interface {
	Accept(context.Context, Request) (Decision, error)
}

type Request struct {
	RemoteAddr      string            `json:"remote_addr"`
	Headers         map[string]string `json:"headers"`
	ProtocolVersion string            `json:"protocol_version"`
	Transport       string            `json:"transport"`
	ALPN            string            `json:"alpn"`
	Timestamp       string            `json:"timestamp"`
}

type ConnectionPolicy struct {
	Mode        string `json:"mode,omitempty" yaml:"mode,omitempty"`
	MaxParallel int    `json:"max_parallel,omitempty" yaml:"max_parallel,omitempty"`
}

type Decision struct {
	Allow            bool              `json:"allow"`
	Reason           string            `json:"reason,omitempty"`
	EndpointKey      string            `json:"endpoint_key,omitempty"`
	ConnectionPolicy ConnectionPolicy  `json:"connection_policy,omitempty"`
	Limits           limits.PlanLimits `json:"limits,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
}

type StaticClient struct {
	Token            string            `json:"token" yaml:"token"`
	EndpointKey      string            `json:"endpoint_key" yaml:"endpoint_key"`
	ConnectionPolicy ConnectionPolicy  `json:"connection_policy,omitempty" yaml:"connection_policy,omitempty"`
	Limits           limits.PlanLimits `json:"limits,omitempty" yaml:"limits,omitempty"`
	Labels           map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

type StaticProvider struct {
	clientsByToken map[string]StaticClient
}

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

func (p ConnectionPolicy) Normalized() ConnectionPolicy {
	if strings.TrimSpace(p.Mode) == "" {
		p.Mode = PolicyRejectIfExists
	}
	if p.Mode == PolicyAllowParallel && p.MaxParallel <= 0 {
		p.MaxParallel = 1
	}
	return p
}

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
