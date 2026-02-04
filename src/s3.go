package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	// Default concurrency for uploads/downloads
	defaultConcurrency = 10

	// Part size limits
	minPartSize = 5 * 1024 * 1024   // 5 MiB minimum (AWS requirement)
	maxPartSize = 100 * 1024 * 1024 // 100 MiB maximum (practical limit)

	// Maximum number of parts for multipart upload
	maxUploadParts = 10000
)

// getS3Client creates a new S3 client with the configured region and optional custom endpoint
// Supports S3 Transfer Acceleration when S3_USE_ACCELERATE=true
func getS3Client(ctx context.Context) (*s3.Client, error) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}

	// Check for custom endpoint (useful for S3-compatible services like MinIO, LocalStack)
	customEndpoint := os.Getenv("AWS_S3_ENDPOINT")
	if customEndpoint != "" {
		slog.Debug("using custom S3 endpoint", "endpoint", customEndpoint)
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(customEndpoint)
			o.UsePathStyle = true // Required for most S3-compatible services
		}), nil
	}

	// Check for S3 Transfer Acceleration
	useAccelerate := os.Getenv("S3_USE_ACCELERATE") == "true"
	if useAccelerate {
		slog.Debug("S3 Transfer Acceleration enabled")
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.UseAccelerate = true
		}), nil
	}

	return s3.NewFromConfig(cfg), nil
}

// optimalPartSize calculates the optimal part size for multipart uploads
// based on file size. AWS allows max 10,000 parts per upload.
func optimalPartSize(fileSize int64) int64 {
	// Calculate minimum part size needed to fit within max parts limit
	partSize := fileSize / maxUploadParts

	// Apply minimum and maximum bounds
	if partSize < minPartSize {
		return minPartSize
	}
	if partSize > maxPartSize {
		return maxPartSize
	}

	// Round up to nearest MiB for cleaner boundaries
	mib := int64(1024 * 1024)
	return ((partSize + mib - 1) / mib) * mib
}

func GetLatestObject(key string, bucket string) (string, error) {
	session, err := getS3Client(context.TODO())
	if err != nil {
		return "", err
	}

	response, err := session.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(key),
	})
	if err != nil {
		return "", err
	}

	files := response.Contents

	if len(files) < 1 {
		return "", errors.New("failed to find any files matching default key")
	}

	sort.Slice(files, func(i, j int) bool {
		// Handle nil LastModified pointers safely
		if files[i].LastModified == nil {
			return false
		}
		if files[j].LastModified == nil {
			return true
		}
		return files[i].LastModified.After(*files[j].LastModified)
	})

	if files[0].Key == nil {
		return "", errors.New("latest file has nil key")
	}
	return *files[0].Key, nil
}

// PutObject - Upload object to s3 bucket with optimized multipart upload
func PutObject(key string, bucket string, s3Class string) error {
	session, err := getS3Client(context.TODO())
	if err != nil {
		return err
	}

	file, err := os.Open(key)
	if err != nil {
		return err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	fileSize := fileInfo.Size()

	// Calculate optimal part size based on file size
	partSize := optimalPartSize(fileSize)

	uploader := manager.NewUploader(session, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = defaultConcurrency
	})

	start := time.Now()
	slog.Info("uploading cache",
		"size", getReadableBytes(fileSize),
		"part_size", getReadableBytes(partSize),
		"concurrency", defaultConcurrency,
	)

	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         file,
		StorageClass: types.StorageClass(s3Class),
	})
	if err == nil {
		elapsed := time.Since(start)
		speed := float64(fileSize) / elapsed.Seconds() / 1024 / 1024 // MB/s
		slog.Info("cache saved successfully",
			"key", key,
			"size", getReadableBytes(fileSize),
			"duration", elapsed,
			"speed_mbps", speed,
		)
	}

	return err
}

// StreamUpload uploads data from an io.Reader directly to S3 without creating a temp file.
// This is useful for streaming compressed data directly to S3.
func StreamUpload(ctx context.Context, reader io.Reader, key string, bucket string, s3Class string) error {
	session, err := getS3Client(ctx)
	if err != nil {
		return err
	}

	uploader := manager.NewUploader(session, func(u *manager.Uploader) {
		// For streaming uploads, we don't know the file size upfront
		// Use a reasonable default part size
		u.PartSize = minPartSize
		u.Concurrency = defaultConcurrency
	})

	start := time.Now()
	slog.Info("streaming upload to S3", "key", key, "bucket", bucket)

	result, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         reader,
		StorageClass: types.StorageClass(s3Class),
	})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)
	slog.Info("streaming upload completed",
		"key", key,
		"location", result.Location,
		"duration", elapsed,
	)

	return nil
}

// GetObject - Get object from s3 bucket with optimized multipart download
func GetObject(key string, bucket string) error {
	start := time.Now()
	session, err := getS3Client(context.TODO())
	if err != nil {
		return err
	}

	outFile, err := os.Create(key)
	if err != nil {
		return err
	}
	defer outFile.Close()

	downloader := manager.NewDownloader(session, func(d *manager.Downloader) {
		d.Concurrency = defaultConcurrency
		d.PartSize = minPartSize // 5 MiB - good balance for downloads
	})

	bytesDownloaded, err := downloader.Download(context.TODO(), outFile, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return err
	}

	elapsed := time.Since(start)
	speed := float64(bytesDownloaded) / elapsed.Seconds() / 1024 / 1024 // MB/s
	slog.Info("cache downloaded successfully",
		"key", key,
		"size", getReadableBytes(bytesDownloaded),
		"duration", elapsed,
		"speed_mbps", speed,
	)

	return nil
}

// DeleteObject - Delete object from s3 bucket
func DeleteObject(key string, bucket string) error {
	session, err := getS3Client(context.TODO())
	if err != nil {
		return err
	}

	objProps, err := ObjectProperties(key, bucket)
	if err != nil || objProps == nil {
		slog.Warn("cannot delete cache, likely does not exist", "key", key)
		return err
	}

	i := &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	_, err = session.DeleteObject(context.TODO(), i)
	if err == nil {
		var size int64
		if objProps.ContentLength != nil {
			size = *objProps.ContentLength
		}
		slog.Info("cache deleted successfully", "key", key, "size", getReadableBytes(size))
	}

	return err
}

// ObjectProperties - Get object properties in s3
func ObjectProperties(key string, bucket string) (*s3.HeadObjectOutput, error) {
	session, err := getS3Client(context.TODO())
	if err != nil {
		return nil, err
	}

	i := &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	headObjectOutput, err := session.HeadObject(context.TODO(), i)
	return headObjectOutput, err
}

func ObjectExists(key string, bucket string) (bool, error) {
	headObjectOutput, err := ObjectProperties(key, bucket)
	if err != nil || headObjectOutput == nil {
		// Intentionally return error nil, this is just an exists/does not exist check
		return false, nil
	}
	return true, nil
}
