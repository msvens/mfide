package server

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// newLogger builds the service logger from log.format (console or json) and
// log.level (debug, info, warn, error). It logs to stderr, with stack traces
// only from Error up.
func newLogger(format, level string) (*zap.Logger, error) {
	if format != "console" && format != "json" {
		return nil, fmt.Errorf("log.format must be console or json, got %q", format)
	}
	lvl, err := zapcore.ParseLevel(level)
	if err != nil || level == "" || lvl > zapcore.ErrorLevel {
		return nil, fmt.Errorf("log.level must be debug, info, warn or error, got %q", level)
	}
	enc := zap.NewProductionEncoderConfig()
	enc.EncodeTime = zapcore.ISO8601TimeEncoder
	if format == "console" {
		enc.EncodeLevel = zapcore.CapitalLevelEncoder
	}
	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(lvl),
		Encoding:         format,
		EncoderConfig:    enc,
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}
	return cfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
}
