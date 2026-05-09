package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(format string, level string, out io.Writer) (*slog.Logger, error) {
	if out == nil {
		out = os.Stderr
	}
	var slogLevel slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		slogLevel = slog.LevelInfo
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}
	opts := &slog.HandlerOptions{Level: slogLevel}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return slog.New(slog.NewTextHandler(out, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(out, opts)), nil
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
}

func RedactHeaders(headers map[string]string) map[string]string {
	redacted := make(map[string]string, len(headers))
	for key := range headers {
		redacted[key] = "[redacted]"
	}
	return redacted
}
