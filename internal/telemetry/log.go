// Package telemetry provides local structured logging. It never makes external
// calls and never logs credentials or secret values (constitution Principle IV).
package telemetry

import (
	"io"
	"log/slog"
)

// New returns a structured logger writing to w (typically stderr or a file).
// The TUI owns stdout, so diagnostics must not go there.
func New(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
