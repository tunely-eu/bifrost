package acceptor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunely-eu/bifrost/internal/limits"
)

func TestStaticProviderAcceptsKnownToken(t *testing.T) {
	provider, err := NewStaticProvider([]StaticClient{
		{
			Token:       "secret",
			EndpointKey: "home",
			ConnectionPolicy: ConnectionPolicy{
				Mode: PolicyReplaceExisting,
			},
			Limits: limits.PlanLimits{
				MaxStreams:               10,
				MaxBandwidthBPS:          1000,
				StreamIdleTimeoutSeconds: 60,
			},
			Labels: map[string]string{"user": "dev"},
		},
	})
	if err != nil {
		t.Fatalf("NewStaticProvider: %v", err)
	}

	decision, err := provider.Accept(context.Background(), Request{
		Headers: map[string]string{TokenHeader: "secret"},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !decision.Allow {
		t.Fatalf("expected allow, got reason %q", decision.Reason)
	}
	if decision.EndpointKey != "home" {
		t.Fatalf("endpoint = %q", decision.EndpointKey)
	}
	if decision.ConnectionPolicy.Mode != PolicyReplaceExisting {
		t.Fatalf("policy = %q", decision.ConnectionPolicy.Mode)
	}
	if decision.Labels["user"] != "dev" {
		t.Fatalf("labels = %#v", decision.Labels)
	}
}

func TestStaticProviderRejectsUnknownToken(t *testing.T) {
	provider, err := NewStaticProvider([]StaticClient{{Token: "secret", EndpointKey: "home"}})
	if err != nil {
		t.Fatalf("NewStaticProvider: %v", err)
	}
	decision, err := provider.Accept(context.Background(), Request{
		Headers: map[string]string{TokenHeader: "other"},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if decision.Allow {
		t.Fatal("expected reject")
	}
	if !strings.Contains(decision.Reason, "unknown") {
		t.Fatalf("reason = %q", decision.Reason)
	}
}

func TestStaticProviderValidatesClients(t *testing.T) {
	if _, err := NewStaticProvider([]StaticClient{{EndpointKey: "home"}}); err == nil {
		t.Fatal("expected missing token error")
	}
	if _, err := NewStaticProvider([]StaticClient{{Token: "secret"}}); err == nil {
		t.Fatal("expected missing endpoint_key error")
	}
	if _, err := NewStaticProvider([]StaticClient{
		{Token: "secret", EndpointKey: "home"},
		{Token: "secret", EndpointKey: "files"},
	}); err == nil {
		t.Fatal("expected duplicate token error")
	}
}

func TestValidateDecisionRequiresEndpointKey(t *testing.T) {
	_, err := ValidateDecision(Decision{Allow: true}, testGuardrails())
	if err == nil {
		t.Fatal("expected missing endpoint_key error")
	}
}

func TestValidateDecisionDefaultsPolicyAndLimits(t *testing.T) {
	decision, err := ValidateDecision(Decision{
		Allow:       true,
		EndpointKey: "dev",
	}, testGuardrails())
	if err != nil {
		t.Fatalf("ValidateDecision: %v", err)
	}
	if decision.ConnectionPolicy.Mode != PolicyRejectIfExists {
		t.Fatalf("policy mode = %q", decision.ConnectionPolicy.Mode)
	}
	if decision.Limits.MaxStreams == 0 {
		t.Fatal("expected limit defaults")
	}
}

func TestValidateDecisionRejectsLimitsOutsideGuardrails(t *testing.T) {
	_, err := ValidateDecision(Decision{
		Allow:       true,
		EndpointKey: "dev",
		Limits: limits.PlanLimits{
			MaxStreams:               999,
			MaxBandwidthBPS:          100,
			StreamIdleTimeoutSeconds: 300,
		},
	}, testGuardrails())
	if err == nil {
		t.Fatal("expected guardrail rejection")
	}
}

func TestDecisionRejectsUnsupportedLimitFields(t *testing.T) {
	var decision Decision
	err := json.Unmarshal([]byte(`{
		"allow": true,
		"endpoint_key": "dev",
		"limits": {
			"max_streams": 10,
			"max_bandwidth_bps": 1000,
			"idle_timeout_seconds": 300
		}
	}`), &decision)
	if err == nil {
		t.Fatal("expected unsupported legacy limit field error")
	}
}

func testGuardrails() limits.Guardrails {
	return limits.Guardrails{
		MaxSessions:               10,
		MaxStreamsPerSession:      100,
		MaxBandwidthBPSPerSession: 100_000_000,
		MinStreamIdleTimeout:      time.Second,
		MaxStreamIdleTimeout:      time.Hour,
	}
}
