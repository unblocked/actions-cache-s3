package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

func main() {
	InitLogger()

	action, err := ParseAction()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	tc := action.TransferConfig()
	slog.Info("configuration",
		"compression", action.Compression,
		"compression_level", action.CompressionLevel,
		"upload_concurrency", tc.uploadConcurrency(),
		"download_concurrency", tc.downloadConcurrency(),
	)

	switch action.Action {
	case PutAction:
		if err := runPut(action, tc); err != nil {
			slog.Error("put failed", "error", err)
			os.Exit(1)
		}
	case GetAction:
		if err := runGet(action, tc); err != nil {
			slog.Error("get failed", "error", err)
			os.Exit(1)
		}
	case DeleteAction:
		if err := runDelete(action); err != nil {
			slog.Error("delete failed", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("invalid action", "action", action.Action, "valid_options", []string{PutAction, DeleteAction, GetAction})
		os.Exit(1)
	}
}

func runPut(action Action, tc TransferConfig) error {
	if len(action.Artifacts) == 0 || len(action.Artifacts[0]) == 0 {
		return fmt.Errorf("no artifacts patterns provided")
	}

	shouldSkip, err := ObjectExists(action.Key, action.Bucket)
	if err != nil {
		return fmt.Errorf("failed to check if object exists: %w", err)
	}
	if shouldSkip {
		slog.Info("cache hit, skipping cache upload")
		return nil
	}
	slog.Info("cache miss")

	start := time.Now()
	slog.Info("starting streaming upload", "key", action.Key)

	reader, errChan := ZipStream(action.Artifacts, action.Compression, action.CompressionLevel)
	ctx := context.Background()

	uploadErr := StreamUpload(ctx, reader, action.Key, action.Bucket, action.S3Class, tc)
	if uploadErr != nil {
		reader.Close()
	}

	if compressErr := <-errChan; compressErr != nil {
		return fmt.Errorf("failed to compress artifacts: %w", compressErr)
	}
	if uploadErr != nil {
		return fmt.Errorf("failed to upload cache: %w", uploadErr)
	}

	slog.Info("cache saved successfully", "key", action.Key, "duration", time.Since(start))
	return nil
}

func runGet(action Action, tc TransferConfig) error {
	slog.Info("attempting to restore cache", "key", action.Key)

	exists, err := ObjectExists(action.Key, action.Bucket)
	if err != nil {
		return fmt.Errorf("failed to check if object exists: %w", err)
	}

	var filename string
	if exists {
		slog.Info("cache hit, starting download")
		filename = action.Key
	} else {
		slog.Info("no cache found for key, trying default", "key", action.Key, "default_key", action.DefaultKey)
		filename, err = GetLatestObject(action.DefaultKey, action.Bucket)
		if err != nil {
			slog.Warn("no cache found, skipping download", "error", err)
			return nil
		}
		slog.Info("defaulting to latest similar key", "filename", filename)
	}

	if err := GetObject(filename, action.Bucket, tc); err != nil {
		return fmt.Errorf("failed to download cache: %w", err)
	}

	if err := Unzip(filename, action.Compression); err != nil {
		return fmt.Errorf("failed to unzip cache: %w", err)
	}

	return nil
}

func runDelete(action Action) error {
	if err := DeleteObject(action.Key, action.Bucket); err != nil {
		return fmt.Errorf("failed to delete cache: %w", err)
	}
	return nil
}
