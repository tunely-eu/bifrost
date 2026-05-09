package limits

import (
	"context"
	"testing"
	"time"
)

func TestDefaultsValidate(t *testing.T) {
	if err := DefaultPlanLimits().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWithDefaults(t *testing.T) {
	values := PlanLimits{MaxStreams: 2}.WithDefaults(DefaultPlanLimits())
	if values.MaxStreams != 2 {
		t.Fatalf("MaxStreams = %d", values.MaxStreams)
	}
	if values.MaxBandwidthBPS == 0 {
		t.Fatal("expected bandwidth default")
	}
}

func TestEnforceGuardrailsRejectsOutOfRangePlanLimits(t *testing.T) {
	guardrails := Guardrails{
		MaxSessions:               1000,
		MaxStreamsPerSession:      10,
		MaxBandwidthBPSPerSession: 1000,
		MinStreamIdleTimeout:      time.Second,
		MaxStreamIdleTimeout:      time.Minute,
		MaxHeaders:                32,
		MaxHeaderBytes:            8192,
	}
	if err := EnforceGuardrails(PlanLimits{
		MaxStreams:               11,
		MaxBandwidthBPS:          100,
		StreamIdleTimeoutSeconds: 10,
	}, guardrails); err == nil {
		t.Fatal("expected max_streams guardrail error")
	}
	if err := EnforceGuardrails(PlanLimits{
		MaxStreams:               10,
		MaxBandwidthBPS:          100,
		StreamIdleTimeoutSeconds: 10,
	}, guardrails); err != nil {
		t.Fatalf("EnforceGuardrails: %v", err)
	}
}

func TestRateLimiterSlowsTransfer(t *testing.T) {
	limiter := NewRateLimiter(100)
	start := time.Now()
	if err := limiter.Wait(context.Background(), 200); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("limiter did not slow transfer enough: %s", time.Since(start))
	}
}
