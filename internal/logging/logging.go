// Package logging builds the application logger from configuration.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// New returns a slog.Logger writing to stderr using the requested level and
// format. Supported levels: debug, info, warn, error. Supported formats:
// text, json.
func New(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "text", "":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}

	return slog.New(handler), nil
}
