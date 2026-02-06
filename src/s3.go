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

	// Default download part size
	defaultDownloadPartSize = minPartSize

	// Maximum number of parts for multipart upload
	maxUploadParts = 10000
)

// TransferConfig holds configurable S3 transfer parameters.
// Zero values mean "use defaults".
type TransferConfig struct {
	UploadConcurrency   int   // 0 = defaultConcurrency
	DownloadConcurrency int   // 0 = defaultConcurrency
	UploadPartSize      int64 // 0 = auto-calculated from file size
	DownloadPartSize    int64 // 0 = defaultDownloadPartSize
}

func (tc TransferConfig) uploadConcurrency() int {
	if tc.UploadConcurrency > 0 {
		return tc.UploadConcurrency
	}
	return defaultConcurrency
}

func (tc TransferConfig) downloadConcurrency() int {
	if tc.DownloadConcurrency > 0 {
		return tc.DownloadConcurrency
	}
	return defaultConcurrency
}

func (tc TransferConfig) downloadPartSize() int64 {
	if tc.DownloadPartSize > 0 {
		return tc.DownloadPartSize
	}
	return defaultDownloadPartSize
}

// resolveUploadPartSize returns the part size for uploads.
// If a user-specified size is set, it is clamped to AWS limits.
// Otherwise, the optimal size is calculated from the file size.
func (tc TransferConfig) resolveUploadPartSize(fileSize int64) int64 {
	if tc.UploadPartSize > 0 {
		ps := tc.UploadPartSize
		if ps < minPartSize {
			ps = minPartSize
		}
		if ps > maxPartSize {
			ps = maxPartSize
		}
		return ps
	}
	return optimalPartSize(fileSize)
}

// resolveStreamUploadPartSize returns the part size for streaming uploads
// where the total size is unknown upfront.
func (tc TransferConfig) resolveStreamUploadPartSize() int64 {
	if tc.UploadPartSize > 0 {
		ps := tc.UploadPartSize
		if ps < minPartSize {
			ps = minPartSize
		}
		if ps > maxPartSize {
			ps = maxPartSize
		}
		return ps
	}
	return minPartSize
}

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

// PutObject uploads an object to S3 with optimized multipart upload.
// Transfer concurrency and part size are controlled via tc.
func PutObject(key string, bucket string, s3Class string, tc TransferConfig) error {
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

	partSize := tc.resolveUploadPartSize(fileSize)
	concurrency := tc.uploadConcurrency()

	uploader := manager.NewUploader(session, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = concurrency
	})

	start := time.Now()
	slog.Info("uploading cache",
		"size", getReadableBytes(fileSize),
		"part_size", getReadableBytes(partSize),
		"concurrency", concurrency,
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
func StreamUpload(ctx context.Context, reader io.Reader, key string, bucket string, s3Class string, tc TransferConfig) error {
	session, err := getS3Client(ctx)
	if err != nil {
		return err
	}

	partSize := tc.resolveStreamUploadPartSize()
	concurrency := tc.uploadConcurrency()

	uploader := manager.NewUploader(session, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = concurrency
	})

	start := time.Now()
	slog.Info("streaming upload to S3",
		"key", key,
		"bucket", bucket,
		"part_size", getReadableBytes(partSize),
		"concurrency", concurrency,
	)

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

// GetObject downloads an object from S3 with optimized multipart download.
// Transfer concurrency and part size are controlled via tc.
func GetObject(key string, bucket string, tc TransferConfig) error {
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

	partSize := tc.downloadPartSize()
	concurrency := tc.downloadConcurrency()

	downloader := manager.NewDownloader(session, func(d *manager.Downloader) {
		d.Concurrency = concurrency
		d.PartSize = partSize
	})

	slog.Info("downloading cache",
		"key", key,
		"part_size", getReadableBytes(partSize),
		"concurrency", concurrency,
	)

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
