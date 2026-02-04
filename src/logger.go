package main

import (
	"log/slog"
	"os"
)

// logLevel is a dynamic log level that can be changed at runtime
var logLevel = new(slog.LevelVar)

// InitLogger initializes the global slog logger with configurable level
// Set DEBUG=1 or DEBUG=true environment variable to enable debug logging
func InitLogger() {
	// Default to text handler for human-readable output
	// Use JSON handler in production by setting LOG_FORMAT=json
	var handler slog.Handler

	opts := &slog.HandlerOptions{
		Level: logLevel,
		// Include source file and line number in debug mode
		AddSource: os.Getenv("DEBUG") != "",
	}

	if os.Getenv("LOG_FORMAT") == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	slog.SetDefault(slog.New(handler))

	// Set log level based on DEBUG environment variable
	if os.Getenv("DEBUG") != "" {
		logLevel.Set(slog.LevelDebug)
		slog.Debug("debug logging enabled")
	}
}

// SetLogLevel allows changing the log level at runtime
func SetLogLevel(level slog.Level) {
	logLevel.Set(level)
}

