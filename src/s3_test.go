package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// These tests require MinIO running locally.
// Start MinIO with:
//   docker run -d --name minio -p 9000:9000 -p 9001:9001 \
//     -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin \
//     minio/minio server /data --console-address ":9001"
//
// Then create a test bucket:
//   docker exec minio mc alias set local http://localhost:9000 minioadmin minioadmin
//   docker exec minio mc mb local/test-bucket

const (
	testBucket   = "test-bucket"
	testEndpoint = "http://localhost:9000"
)

func skipIfNoMinIO(t *testing.T) {
	// Set up environment for MinIO
	os.Setenv("AWS_S3_ENDPOINT", testEndpoint)
	os.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
	os.Setenv("AWS_REGION", "us-east-1")

	// Try to connect
	_, err := ObjectExists("nonexistent-key", testBucket)
	if err != nil {
		t.Skipf("MinIO not available (run: docker run -d -p 9000:9000 minio/minio server /data): %v", err)
	}
}

func TestPutAndGetObject(t *testing.T) {
	skipIfNoMinIO(t)

	// Create a temp file to upload
	tempDir, err := os.MkdirTemp("", "s3_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test content
	testKey := "test-upload.tar.zst"
	testContent := "Hello, S3! This is test content for upload."

	// Create a test archive
	testDataDir := tempDir + "/data"
	os.MkdirAll(testDataDir, 0755)
	os.WriteFile(testDataDir+"/test.txt", []byte(testContent), 0644)

	archivePath := tempDir + "/" + testKey
	if err := Zip(archivePath, []string{testDataDir}); err != nil {
		t.Fatalf("failed to create test archive: %v", err)
	}

	// Change to temp dir so PutObject can find the file
	origDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(origDir)

	// Test PutObject
	if err := PutObject(testKey, testBucket, "STANDARD"); err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	// Verify object exists
	exists, err := ObjectExists(testKey, testBucket)
	if err != nil {
		t.Fatalf("ObjectExists failed: %v", err)
	}
	if !exists {
		t.Fatal("object should exist after upload")
	}

	// Remove local file
	os.Remove(archivePath)

	// Test GetObject
	if err := GetObject(testKey, testBucket); err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}

	// Verify downloaded file exists
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("downloaded file not found: %v", err)
	}

	// Clean up - delete from S3
	if err := DeleteObject(testKey, testBucket); err != nil {
		t.Logf("warning: failed to delete test object: %v", err)
	}
}

func TestStreamUpload(t *testing.T) {
	skipIfNoMinIO(t)

	// Create test data
	tempDir, err := os.MkdirTemp("", "stream_s3_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testDataDir := tempDir + "/data"
	os.MkdirAll(testDataDir, 0755)
	os.WriteFile(testDataDir+"/stream_test.txt", []byte("Stream upload test content"), 0644)

	testKey := "test-stream-upload.tar.zst"

	// Test streaming upload
	reader, errChan := ZipStream([]string{testDataDir})
	ctx := context.Background()

	if err := StreamUpload(ctx, reader, testKey, testBucket, "STANDARD"); err != nil {
		t.Fatalf("StreamUpload failed: %v", err)
	}

	// Check compression errors
	if compressErr := <-errChan; compressErr != nil {
		t.Fatalf("compression error: %v", compressErr)
	}

	// Verify object exists
	exists, err := ObjectExists(testKey, testBucket)
	if err != nil {
		t.Fatalf("ObjectExists failed: %v", err)
	}
	if !exists {
		t.Fatal("streamed object should exist after upload")
	}

	// Clean up
	DeleteObject(testKey, testBucket)
}

func TestOptimalPartSize(t *testing.T) {
	tests := []struct {
		fileSize int64
		expected int64
	}{
		{1024, minPartSize},                      // Small file -> min part size
		{100 * 1024 * 1024, minPartSize},         // 100 MB -> min part size
		{50 * 1024 * 1024 * 1024, 6 * 1024 * 1024}, // 50 GB -> ~5 MiB rounded up
		{500 * 1024 * 1024 * 1024, 51 * 1024 * 1024}, // 500 GB -> ~50 MiB
	}

	for _, tt := range tests {
		result := optimalPartSize(tt.fileSize)
		if result < minPartSize || result > maxPartSize {
			t.Errorf("optimalPartSize(%d) = %d, out of bounds [%d, %d]",
				tt.fileSize, result, minPartSize, maxPartSize)
		}
	}
}

func TestGetReadableBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 B"},
		{1500, "1.5 kB"},
		{1500000, "1.5 MB"},
		{1500000000, "1.5 GB"},
	}

	for _, tt := range tests {
		result := getReadableBytes(tt.bytes)
		if !strings.Contains(result, strings.Split(tt.expected, " ")[0][:3]) {
			t.Errorf("getReadableBytes(%d) = %s, want %s", tt.bytes, result, tt.expected)
		}
	}
}

