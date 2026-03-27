package telemetry

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config controls structured logger initialization.
type Config struct {
	ServiceName string
	Version     string
	Level       string
	Output      io.Writer
}

// Init configures the default slog logger for cloud-native workloads.
func Init(cfg Config) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}
	level := parseLevel(cfg.Level)
	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level}))
	if cfg.ServiceName != "" {
		logger = logger.With(
			slog.String("service", cfg.ServiceName),
			slog.String("version", cfg.Version),
		)
	}
	slog.SetDefault(logger)
	return logger
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
