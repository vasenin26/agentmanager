package logger

import "go.uber.org/zap"

// New creates a development zap logger. Caller should call Sync() when done.
func New() (*zap.Logger, error) {
	cfg := zap.NewDevelopmentConfig()
	return cfg.Build()
}
