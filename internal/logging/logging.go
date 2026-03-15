package logging

import (
	"io"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(verbose bool) (*zap.Logger, error) {
	return NewWithWriter(verbose, os.Stdout)
}

func NewWithWriter(verbose bool, out io.Writer) (*zap.Logger, error) {
	level := zapcore.InfoLevel
	if verbose {
		level = zapcore.DebugLevel
	}

	cfg := zap.NewProductionEncoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	enc := zapcore.NewConsoleEncoder(cfg)
	core := zapcore.NewCore(enc, zapcore.AddSync(out), level)
	return zap.New(core), nil
}
