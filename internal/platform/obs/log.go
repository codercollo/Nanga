// Package obs holds observability building blocks: structured logging and
// health endpoints. OpenTelemetry/Prometheus are added with the first service
// that needs them (see phases/phase-1/context.md).
package obs

import (
	"io"
	"log/slog"
)

// NewLogger builds a slog logger tagged with service and env.
func NewLogger(w io.Writer, level, format, service, env string) *slog.Logger {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h).With("service", service, "env", env)
}
