package acceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"bifrost/internal/config"
	"bifrost/internal/limits"
	"bifrost/internal/listener"
	"bifrost/internal/metrics"
)

const (
	PolicyRejectIfExists  = "reject_if_exists"
	PolicyReplaceExisting = "replace_existing"
	PolicyAllowParallel   = "allow_parallel"
)

type Request struct {
	RemoteAddr      string            `json:"remote_addr"`
	Headers         map[string]string `json:"headers"`
	ProtocolVersion string            `json:"protocol_version"`
	Transport       string            `json:"transport"`
	ALPN            string            `json:"alpn"`
	Timestamp       string            `json:"timestamp"`
}

type ConnectionPolicy struct {
	Mode        string `json:"mode,omitempty"`
	MaxParallel int    `json:"max_parallel,omitempty"`
}

type Decision struct {
	Allow            bool              `json:"allow"`
	Reason           string            `json:"reason,omitempty"`
	EndpointKey      string            `json:"endpoint_key,omitempty"`
	Listener         listener.Spec     `json:"listener,omitempty"`
	ConnectionPolicy ConnectionPolicy  `json:"connection_policy,omitempty"`
	Limits           limits.PlanLimits `json:"limits,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
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

func ValidateDecision(decision Decision, listenerOptions listener.Options, guardrails limits.Guardrails) (Decision, error) {
	if !decision.Allow {
		return decision, nil
	}
	if strings.TrimSpace(decision.EndpointKey) == "" {
		return decision, fmt.Errorf("accept hook allowed without endpoint_key")
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
	if err := listener.Validate(decision.Listener, listenerOptions); err != nil {
		return decision, err
	}
	if decision.Labels == nil {
		decision.Labels = map[string]string{}
	}
	return decision, nil
}

func Run(ctx context.Context, cfg config.HookConfig, runtime config.RuntimeConfig, request Request, logger *slog.Logger, recorder metrics.Recorder) (Decision, error) {
	timeout := runtime.HookTimeout.Duration
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(request)
	if err != nil {
		return Decision{}, err
	}
	payload = append(payload, '\n')

	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Stdin = bytes.NewReader(payload)

	stdout := &limitedBuffer{limit: runtime.HookMaxStdoutBytes}
	stderr := &limitedBuffer{limit: 64 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err = cmd.Run()
	if recorder != nil {
		recorder.Add("hook_latency_seconds_total", time.Since(start).Seconds())
	}
	if stderr.Len() > 0 && logger != nil {
		logger.Warn("accept hook stderr", "stderr", strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		if recorder != nil {
			recorder.Inc("hook_errors_total")
		}
		if ctx.Err() != nil {
			return Decision{}, fmt.Errorf("accept hook timed out after %s", timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return Decision{}, fmt.Errorf("accept hook failed: %s", msg)
	}

	var decision Decision
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &decision); err != nil {
		if recorder != nil {
			recorder.Inc("hook_errors_total")
		}
		return Decision{}, fmt.Errorf("decode accept hook output: %w", err)
	}
	return decision, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit > 0 && int64(b.Len()+len(p)) > b.limit {
		remaining := int(b.limit) - b.Len()
		if remaining > 0 {
			_, _ = b.Buffer.Write(p[:remaining])
		}
		return 0, fmt.Errorf("buffer exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(p)
}

var _ io.Writer = (*limitedBuffer)(nil)
