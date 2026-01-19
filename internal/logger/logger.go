package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct {
	*zap.Logger
}

type Config struct {
	Level  string
	Format string
	Output string
}

func New(level string) *zap.Logger {
	config := &Config{
		Level:  level,
		Format: getEnv("LOG_FORMAT", "console"),
		Output: getEnv("LOG_OUTPUT", "stdout"),
	}
	return NewWithConfig(config)
}

func NewWithConfig(config *Config) *zap.Logger {
	// Parse log level
	level := parseLogLevel(config.Level)

	// Encoder config
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// For JSON format, remove color encoding
	if config.Format == "json" {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	}

	// Create encoder
	var encoder zapcore.Encoder
	if config.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}
	// Output writer
	var writer zapcore.WriteSyncer
	if config.Output == "stdout" || config.Output == "" {
		writer = zapcore.AddSync(os.Stdout)
	} else if config.Output == "stderr" {
		writer = zapcore.AddSync(os.Stderr)
	} else {
		// File output
		file, err := os.OpenFile(config.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			// Fallback to stdout
			writer = zapcore.AddSync(os.Stdout)
		} else {
			writer = zapcore.AddSync(file)
		}
	}
	// Create core
	core := zapcore.NewCore(encoder, writer, level)

	// Create logger
	logger := zap.New(core,
		zap.AddCaller(),
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
	return logger
}

func parseLogLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

// WithFields creates a logger with predefined fields
func WithFields(logger *zap.Logger, fields ...zap.Field) *zap.Logger {
	return logger.With(fields...)
}

// Named creates a named logger
func Named(logger *zap.Logger, name string) *zap.Logger {
	return logger.Named(name)
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
