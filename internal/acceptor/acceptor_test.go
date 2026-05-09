package acceptor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bifrost/internal/config"
	"bifrost/internal/limits"
	"bifrost/internal/listener"
)

func TestRunAcceptPassesHeaders(t *testing.T) {
	dir := t.TempDir()
	capturePath := filepath.Join(dir, "input.json")
	scriptPath := filepath.Join(dir, "accept.sh")
	script := "#!/bin/sh\n" +
		"cat > '" + strings.ReplaceAll(capturePath, "'", "'\\''") + "'\n" +
		"printf '{\"allow\":false,\"reason\":\"captured\"}\\n'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	decision, err := Run(context.Background(), config.HookConfig{
		Command: scriptPath,
	}, config.RuntimeConfig{
		HookTimeout:        config.NewDuration(time.Second),
		HookMaxStdoutBytes: 1024,
	}, Request{
		RemoteAddr:      "203.0.113.10:49152",
		ProtocolVersion: "1",
		Headers:         map[string]string{"x-bifrost-token": "secret"},
		Transport:       "tls",
		ALPN:            "bifrost/1",
	}, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if decision.Allow {
		t.Fatal("expected rejection")
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if !strings.Contains(string(captured), `"x-bifrost-token":"secret"`) {
		t.Fatalf("captured request = %s", captured)
	}
}

func TestRunAcceptTimeout(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "accept.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	_, err := Run(context.Background(), config.HookConfig{
		Command: scriptPath,
	}, config.RuntimeConfig{
		HookTimeout:        config.NewDuration(10 * time.Millisecond),
		HookMaxStdoutBytes: 1024,
	}, Request{}, nil, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestValidateDecisionRequiresEndpointKey(t *testing.T) {
	_, err := ValidateDecision(Decision{
		Allow: true,
		Listener: listener.Spec{
			Type:    "tcp",
			Address: "127.0.0.1:0",
		},
	}, listener.Options{}, testGuardrails())
	if err == nil {
		t.Fatal("expected missing endpoint_key error")
	}
}

func TestValidateDecisionDefaultsPolicyAndLimits(t *testing.T) {
	decision, err := ValidateDecision(Decision{
		Allow:       true,
		EndpointKey: "dev",
		Listener: listener.Spec{
			Type:    "tcp",
			Address: "127.0.0.1:0",
		},
	}, listener.Options{}, testGuardrails())
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
		Listener: listener.Spec{
			Type:    "tcp",
			Address: "127.0.0.1:0",
		},
		Limits: limits.PlanLimits{
			MaxStreams:               999,
			MaxBandwidthBPS:          100,
			StreamIdleTimeoutSeconds: 300,
		},
	}, listener.Options{}, testGuardrails())
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
		MaxSessions:               1000,
		MaxStreamsPerSession:      512,
		MaxBandwidthBPSPerSession: 100_000_000,
		MinStreamIdleTimeout:      30 * time.Second,
		MaxStreamIdleTimeout:      time.Hour,
		MaxHeaders:                32,
		MaxHeaderBytes:            8192,
	}
}
