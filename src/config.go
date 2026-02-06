package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// ParseAction reads all configuration from environment variables,
// validates it, and returns a fully populated Action.
func ParseAction() (Action, error) {
	compression := os.Getenv("COMPRESSION")
	if compression == "" {
		compression = CompressionZstd
	}
	if compression != CompressionZstd && compression != CompressionNone {
		return Action{}, fmt.Errorf("invalid compression mode %q, valid options: %s, %s",
			compression, CompressionZstd, CompressionNone)
	}

	action := Action{
		Action:              os.Getenv("ACTION"),
		Bucket:              os.Getenv("BUCKET"),
		S3Class:             os.Getenv("S3_CLASS"),
		Key:                 os.Getenv("KEY") + keyExtension(compression),
		DefaultKey:          os.Getenv("DEFAULT_KEY"),
		Artifacts:           strings.Split(strings.TrimSpace(os.Getenv("ARTIFACTS")), "\n"),
		Compression:         compression,
		CompressionLevel:    parseIntEnv("COMPRESSION_LEVEL"),
		UploadConcurrency:   parseIntEnv("UPLOAD_CONCURRENCY"),
		DownloadConcurrency: parseIntEnv("DOWNLOAD_CONCURRENCY"),
		UploadPartSize:      parseByteSize("UPLOAD_PART_SIZE"),
		DownloadPartSize:    parseByteSize("DOWNLOAD_PART_SIZE"),
	}

	return action, nil
}

// keyExtension returns the file extension for the given compression mode.
func keyExtension(compression string) string {
	switch compression {
	case CompressionNone:
		return ".tar"
	default:
		return ".tar.zst"
	}
}

// parseIntEnv reads an environment variable as an integer.
// Returns 0 (meaning "use default") if the variable is empty.
func parseIntEnv(name string) int {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid integer for env var, using default", "var", name, "value", v)
		return 0
	}
	return n
}

// parseByteSize parses a human-readable byte size string (e.g. "10MB", "5MiB", "100")
// into bytes. Supported suffixes: MB, MiB, GB, GiB (case-insensitive).
// A plain number is treated as bytes. Returns 0 if empty.
func parseByteSize(name string) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0
	}
	upper := strings.ToUpper(v)

	multiplier := int64(1)
	numStr := v
	switch {
	case strings.HasSuffix(upper, "GIB"):
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSpace(v[:len(v)-3])
	case strings.HasSuffix(upper, "GB"):
		multiplier = 1000 * 1000 * 1000
		numStr = strings.TrimSpace(v[:len(v)-2])
	case strings.HasSuffix(upper, "MIB"):
		multiplier = 1024 * 1024
		numStr = strings.TrimSpace(v[:len(v)-3])
	case strings.HasSuffix(upper, "MB"):
		multiplier = 1000 * 1000
		numStr = strings.TrimSpace(v[:len(v)-2])
	}

	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		slog.Warn("invalid byte size for env var, using default", "var", name, "value", v)
		return 0
	}
	return n * multiplier
}
