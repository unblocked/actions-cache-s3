package main

import (
	"context"
	"errors"
	"log"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// getS3Client creates a new S3 client with the configured region and optional custom endpoint
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
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(customEndpoint)
			o.UsePathStyle = true // Required for most S3-compatible services
		}), nil
	}

	return s3.NewFromConfig(cfg), nil
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

// PutObject - Upload object to s3 bucket
func PutObject(key string, bucket string, s3Class string) error {
	session, err := getS3Client(context.TODO())
	if err != nil {
		return err
	}

	uploader := manager.NewUploader(session, func(u *manager.Uploader) {
		u.PartSize = 10 * 1024 * 1024 // 10 MiB
		u.Concurrency = 5
		u.MaxUploadParts = 50
	})

	file, err := os.Open(key)
	if err != nil {
		return err
	}
	defer file.Close()
	fileSize, err := file.Stat()
	if err != nil {
		return err
	}

	start := time.Now()
	log.Printf("Uploading %v worth of cache", getReadableBytes(fileSize.Size()))
	_, err = uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         file,
		StorageClass: types.StorageClass(s3Class),
	})
	if err == nil {
		elapsed := time.Since(start)
		log.Printf("Cache saved %s (%s) successfully in %s!", key, getReadableBytes(fileSize.Size()), elapsed)
	}

	return err
}

// GetObject - Get object from s3 bucket
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

	downloader := manager.NewDownloader(session)

	_, err = downloader.Download(context.TODO(), outFile, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, func(d *manager.Downloader) {
		// Set the number of workers and part size
		d.Concurrency = 5
		d.PartSize = 5 * 1024 * 1024
	})

	if err != nil {
		return err
	}

	fileSize, err := outFile.Stat()
	if err == nil {
		elapsed := time.Since(start)
		log.Printf("%s (%s) successfully downloaded in %s", key, getReadableBytes(fileSize.Size()), elapsed)
	}

	return err
}

// DeleteObject - Delete object from s3 bucket
func DeleteObject(key string, bucket string) error {
	session, err := getS3Client(context.TODO())
	if err != nil {
		return err
	}

	objProps, err := ObjectProperties(key, bucket)
	if err != nil || objProps == nil {
		log.Printf("Cannot delete %s because it likely does not exist", key)
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
		log.Printf("Cache deleted %s (%s) successfully", key, getReadableBytes(size))
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
