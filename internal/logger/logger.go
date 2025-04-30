package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// Log is the global logger instance
	Log *zap.Logger
)

// Init initializes the logger with the given log level
func Init(level string) error {
	// Parse the log level
	var zapLevel zapcore.Level
	err := zapLevel.UnmarshalText([]byte(level))
	if err != nil {
		return err
	}

	// Create the logger configuration
	config := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	// Disable stack traces
	config.EncoderConfig.StacktraceKey = ""

	// Create the logger
	logger, err := config.Build()
	if err != nil {
		return err
	}

	Log = logger
	return nil
}

// GetLogger returns the global logger instance
func GetLogger() *zap.Logger {
	if Log == nil {
		// If logger is not initialized, create a default production logger
		var err error
		Log, err = zap.NewProduction(zap.WithCaller(false))
		if err != nil {
			panic(err)
		}
	}
	return Log
}

// Sync flushes any buffered log entries
func Sync() error {
	return Log.Sync()
}
