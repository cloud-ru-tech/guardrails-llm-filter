// Package logging configures the process-wide slog default logger and
// provides thin context-aware helpers used across the service.
//
// The helpers mirror log/slog with one addition: Error and Fatal take the
// error as a positional argument, keeping call sites compact.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Setup installs the default slog logger.
// level: debug | info | warn | error (default info).
// format: json (default) | text — text is convenient for local development.
func Setup(level, format string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl, AddSource: true}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
}

// Debug logs at debug level with alternating key-value pairs.
func Debug(ctx context.Context, msg string, kv ...any) {
	slog.DebugContext(ctx, msg, kv...)
}

// Info logs at info level with alternating key-value pairs.
func Info(ctx context.Context, msg string, kv ...any) {
	slog.InfoContext(ctx, msg, kv...)
}

// Warn logs at warn level with alternating key-value pairs.
func Warn(ctx context.Context, msg string, kv ...any) {
	slog.WarnContext(ctx, msg, kv...)
}

// Error logs at error level. err may be nil (e.g. a fallback taken without
// a concrete error); it is then omitted from the attributes.
func Error(ctx context.Context, msg string, err error, kv ...any) {
	if err != nil {
		kv = append([]any{"error", err}, kv...)
	}
	slog.ErrorContext(ctx, msg, kv...)
}

// Fatal logs at error level and exits the process. Use only from main/boot
// paths.
func Fatal(ctx context.Context, msg string, err error, kv ...any) {
	Error(ctx, msg, err, kv...)
	os.Exit(1)
}
