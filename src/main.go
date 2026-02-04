package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

func main() {
	InitLogger()

	action := Action{
		Action:     os.Getenv("ACTION"),
		Bucket:     os.Getenv("BUCKET"),
		S3Class:    os.Getenv("S3_CLASS"),
		Key:        fmt.Sprintf("%s.tar.gz", os.Getenv("KEY")),
		DefaultKey: os.Getenv("DEFAULT_KEY"),
		Artifacts:  strings.Split(strings.TrimSpace(os.Getenv("ARTIFACTS")), "\n"),
	}

	switch act := action.Action; act {
	case PutAction:
		if len(action.Artifacts) == 0 || len(action.Artifacts[0]) == 0 {
			slog.Error("no artifacts patterns provided")
			os.Exit(1)
		}

		shouldSkip, err := ObjectExists(action.Key, action.Bucket)
		if err != nil {
			slog.Error("failed to check if object exists", "error", err)
			os.Exit(1)
		}
		if shouldSkip {
			slog.Info("cache hit, skipping cache upload")
			return
		} else {
			slog.Info("cache miss")
		}

		// Stream compression directly to S3 without creating a temp file
		start := time.Now()
		slog.Info("starting streaming upload", "key", action.Key)

		reader, errChan := ZipStream(action.Artifacts)
		ctx := context.Background()

		uploadErr := StreamUpload(ctx, reader, action.Key, action.Bucket, action.S3Class)

		// Check for compression errors
		compressErr := <-errChan

		if compressErr != nil {
			slog.Error("failed to compress artifacts", "error", compressErr)
			os.Exit(1)
		}
		if uploadErr != nil {
			slog.Error("failed to upload cache", "error", uploadErr)
			os.Exit(1)
		}

		elapsed := time.Since(start)
		slog.Info("cache saved successfully", "key", action.Key, "duration", elapsed)
	case GetAction:
		slog.Info("attempting to restore cache", "key", action.Key)
		exists, err := ObjectExists(action.Key, action.Bucket)
		if err != nil {
			slog.Error("failed to check if object exists", "error", err)
			os.Exit(1)
		}
		// Get and and unzip
		var filename string
		if exists {
			slog.Info("cache hit, starting download")
			filename = action.Key
		} else {
			slog.Info("no cache found for key, trying default", "key", action.Key, "default_key", action.DefaultKey)
			filename, err = GetLatestObject(action.DefaultKey, action.Bucket)
			if err != nil {
				slog.Warn("no cache found, skipping download", "error", err)
				return
			}
			slog.Info("defaulting to latest similar key", "filename", filename)
		}
		err = GetObject(filename, action.Bucket)
		if err != nil {
			slog.Error("failed to download cache", "error", err)
			os.Exit(1)
		}

		if err := Unzip(filename); err != nil {
			slog.Error("failed to unzip cache", "filename", filename, "error", err)
			os.Exit(1)
		}
	case DeleteAction:
		if err := DeleteObject(action.Key, action.Bucket); err != nil {
			slog.Error("failed to delete cache", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("invalid action", "action", act, "valid_options", []string{PutAction, DeleteAction, GetAction})
		os.Exit(1)
	}
}
