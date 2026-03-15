package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(verbose bool) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if verbose {
		level = zapcore.DebugLevel
	}

	cfg := zap.NewProductionEncoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	enc := zapcore.NewConsoleEncoder(cfg)
	core := zapcore.NewCore(enc, zapcore.AddSync(os.Stdout), level)
	return zap.New(core), nil
}
