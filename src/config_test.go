package main

import (
	"os"
	"testing"
)

func TestKeyExtension(t *testing.T) {
	tests := []struct {
		compression string
		expected    string
	}{
		{CompressionZstd, ".tar.zst"},
		{CompressionNone, ".tar"},
		{"unknown", ".tar.zst"}, // default falls back to zstd extension
	}
	for _, tt := range tests {
		result := keyExtension(tt.compression)
		if result != tt.expected {
			t.Errorf("keyExtension(%q) = %q, want %q", tt.compression, result, tt.expected)
		}
	}
}

func TestParseIntEnv(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected int
	}{
		{"empty", "", 0},
		{"valid", "42", 42},
		{"invalid", "abc", 0},
		{"zero", "0", 0},
		{"negative", "-1", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envKey := "TEST_PARSE_INT_ENV"
			if tt.envValue == "" {
				os.Unsetenv(envKey)
			} else {
				os.Setenv(envKey, tt.envValue)
			}
			defer os.Unsetenv(envKey)

			result := parseIntEnv(envKey)
			if result != tt.expected {
				t.Errorf("parseIntEnv(%q) with value %q = %d, want %d", envKey, tt.envValue, result, tt.expected)
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected int64
	}{
		{"empty", "", 0},
		{"plain_bytes", "1024", 1024},
		{"megabytes", "10MB", 10 * 1000 * 1000},
		{"mebibytes", "10MiB", 10 * 1024 * 1024},
		{"gigabytes", "2GB", 2 * 1000 * 1000 * 1000},
		{"gibibytes", "2GiB", 2 * 1024 * 1024 * 1024},
		{"case_insensitive_mb", "5mb", 5 * 1000 * 1000},
		{"case_insensitive_mib", "5mib", 5 * 1024 * 1024},
		{"invalid", "notanumber", 0},
		{"with_spaces", " 10MB ", 10 * 1000 * 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envKey := "TEST_PARSE_BYTE_SIZE"
			if tt.envValue == "" {
				os.Unsetenv(envKey)
			} else {
				os.Setenv(envKey, tt.envValue)
			}
			defer os.Unsetenv(envKey)

			result := parseByteSize(envKey)
			if result != tt.expected {
				t.Errorf("parseByteSize(%q) with value %q = %d, want %d", envKey, tt.envValue, result, tt.expected)
			}
		})
	}
}

func TestParseAction(t *testing.T) {
	// Save and restore all env vars
	envVars := []string{
		"ACTION", "BUCKET", "S3_CLASS", "KEY", "DEFAULT_KEY", "ARTIFACTS",
		"COMPRESSION", "COMPRESSION_LEVEL",
		"UPLOAD_CONCURRENCY", "DOWNLOAD_CONCURRENCY",
		"UPLOAD_PART_SIZE", "DOWNLOAD_PART_SIZE",
	}
	saved := make(map[string]string)
	for _, k := range envVars {
		saved[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	t.Run("defaults", func(t *testing.T) {
		for _, k := range envVars {
			os.Unsetenv(k)
		}
		os.Setenv("ACTION", "put")
		os.Setenv("BUCKET", "my-bucket")
		os.Setenv("KEY", "my-key")

		action, err := ParseAction()
		if err != nil {
			t.Fatalf("ParseAction failed: %v", err)
		}
		if action.Compression != CompressionZstd {
			t.Errorf("expected default compression %q, got %q", CompressionZstd, action.Compression)
		}
		if action.Key != "my-key.tar.zst" {
			t.Errorf("expected key %q, got %q", "my-key.tar.zst", action.Key)
		}
	})

	t.Run("compression_none", func(t *testing.T) {
		for _, k := range envVars {
			os.Unsetenv(k)
		}
		os.Setenv("ACTION", "get")
		os.Setenv("BUCKET", "b")
		os.Setenv("KEY", "k")
		os.Setenv("COMPRESSION", "none")

		action, err := ParseAction()
		if err != nil {
			t.Fatalf("ParseAction failed: %v", err)
		}
		if action.Compression != CompressionNone {
			t.Errorf("expected compression %q, got %q", CompressionNone, action.Compression)
		}
		if action.Key != "k.tar" {
			t.Errorf("expected key %q, got %q", "k.tar", action.Key)
		}
	})

	t.Run("invalid_compression", func(t *testing.T) {
		for _, k := range envVars {
			os.Unsetenv(k)
		}
		os.Setenv("COMPRESSION", "lz4")

		_, err := ParseAction()
		if err == nil {
			t.Fatal("expected error for invalid compression, got nil")
		}
	})

	t.Run("transfer_settings", func(t *testing.T) {
		for _, k := range envVars {
			os.Unsetenv(k)
		}
		os.Setenv("ACTION", "put")
		os.Setenv("BUCKET", "b")
		os.Setenv("KEY", "k")
		os.Setenv("UPLOAD_CONCURRENCY", "20")
		os.Setenv("DOWNLOAD_CONCURRENCY", "5")
		os.Setenv("UPLOAD_PART_SIZE", "10MB")
		os.Setenv("DOWNLOAD_PART_SIZE", "8MiB")
		os.Setenv("COMPRESSION_LEVEL", "3")

		action, err := ParseAction()
		if err != nil {
			t.Fatalf("ParseAction failed: %v", err)
		}
		if action.UploadConcurrency != 20 {
			t.Errorf("UploadConcurrency = %d, want 20", action.UploadConcurrency)
		}
		if action.DownloadConcurrency != 5 {
			t.Errorf("DownloadConcurrency = %d, want 5", action.DownloadConcurrency)
		}
		if action.UploadPartSize != 10*1000*1000 {
			t.Errorf("UploadPartSize = %d, want %d", action.UploadPartSize, 10*1000*1000)
		}
		if action.DownloadPartSize != 8*1024*1024 {
			t.Errorf("DownloadPartSize = %d, want %d", action.DownloadPartSize, 8*1024*1024)
		}
		if action.CompressionLevel != 3 {
			t.Errorf("CompressionLevel = %d, want 3", action.CompressionLevel)
		}
	})
}

func TestActionTransferConfig(t *testing.T) {
	action := Action{
		UploadConcurrency:   15,
		DownloadConcurrency: 8,
		UploadPartSize:      10 * 1024 * 1024,
		DownloadPartSize:    20 * 1024 * 1024,
	}

	tc := action.TransferConfig()

	if tc.UploadConcurrency != 15 {
		t.Errorf("TransferConfig.UploadConcurrency = %d, want 15", tc.UploadConcurrency)
	}
	if tc.DownloadConcurrency != 8 {
		t.Errorf("TransferConfig.DownloadConcurrency = %d, want 8", tc.DownloadConcurrency)
	}
	if tc.UploadPartSize != 10*1024*1024 {
		t.Errorf("TransferConfig.UploadPartSize = %d, want %d", tc.UploadPartSize, 10*1024*1024)
	}
	if tc.DownloadPartSize != 20*1024*1024 {
		t.Errorf("TransferConfig.DownloadPartSize = %d, want %d", tc.DownloadPartSize, 20*1024*1024)
	}
}

func TestActionTransferConfigZeroValues(t *testing.T) {
	action := Action{}
	tc := action.TransferConfig()

	if tc.UploadConcurrency != 0 {
		t.Errorf("expected zero UploadConcurrency, got %d", tc.UploadConcurrency)
	}
	if tc.DownloadConcurrency != 0 {
		t.Errorf("expected zero DownloadConcurrency, got %d", tc.DownloadConcurrency)
	}
	if tc.UploadPartSize != 0 {
		t.Errorf("expected zero UploadPartSize, got %d", tc.UploadPartSize)
	}
	if tc.DownloadPartSize != 0 {
		t.Errorf("expected zero DownloadPartSize, got %d", tc.DownloadPartSize)
	}
}
