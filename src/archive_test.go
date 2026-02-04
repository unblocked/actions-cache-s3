package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestZipAndUnzip(t *testing.T) {
	// Create a temp directory for test files
	tempDir, err := os.MkdirTemp("", "archive_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test files using relative paths
	testDir := "testdata"
	fullTestDir := filepath.Join(tempDir, testDir)
	if err := os.MkdirAll(fullTestDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	// Create some test files with content
	files := map[string]string{
		"file1.txt":        "Hello, World!",
		"file2.txt":        "This is a test file with more content.",
		"subdir/file3.txt": "Nested file content",
	}

	for name, content := range files {
		path := filepath.Join(fullTestDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	// Change to temp dir so we use relative paths in the archive
	origDir, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir to temp: %v", err)
	}
	defer os.Chdir(origDir)

	// Test Zip with relative path
	archivePath := "test.tar.zst"
	if err := Zip(archivePath, []string{testDir}); err != nil {
		t.Fatalf("Zip failed: %v", err)
	}

	// Verify archive was created
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("archive not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("archive is empty")
	}
	t.Logf("Archive size: %d bytes", info.Size())

	// Remove original files to prove unzip works
	os.RemoveAll(testDir)

	// Unzip
	if err := Unzip(archivePath); err != nil {
		t.Fatalf("Unzip failed: %v", err)
	}

	// Verify extracted files
	for name, expectedContent := range files {
		extractedPath := filepath.Join(testDir, name)
		content, err := os.ReadFile(extractedPath)
		if err != nil {
			t.Errorf("failed to read extracted %s: %v", name, err)
			continue
		}
		if string(content) != expectedContent {
			t.Errorf("content mismatch for %s: got %q, want %q", name, string(content), expectedContent)
		}
	}
}

func TestZipStream(t *testing.T) {
	// Create a temp directory for test files
	tempDir, err := os.MkdirTemp("", "zipstream_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Change to temp dir for relative paths
	origDir, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer os.Chdir(origDir)

	// Create test files with relative path
	testDir := "streamdata"
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	testContent := "Hello from ZipStream test!"
	testFile := filepath.Join(testDir, "stream_test.txt")
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Test ZipStream
	reader, errChan := ZipStream([]string{testDir})

	// Read all data from the stream
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read from stream: %v", err)
	}
	reader.Close()

	// Check for compression errors
	if compressErr := <-errChan; compressErr != nil {
		t.Fatalf("compression error: %v", compressErr)
	}

	if len(data) == 0 {
		t.Fatal("stream produced no data")
	}
	t.Logf("Stream produced %d bytes", len(data))

	// Write to file and verify we can unzip it
	archivePath := "stream.tar.zst"
	if err := os.WriteFile(archivePath, data, 0644); err != nil {
		t.Fatalf("failed to write archive: %v", err)
	}

	// Remove original to prove unzip works
	os.RemoveAll(testDir)

	// Unzip
	if err := Unzip(archivePath); err != nil {
		t.Fatalf("failed to unzip streamed archive: %v", err)
	}

	// Verify content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read extracted file: %v", err)
	}
	if string(content) != testContent {
		t.Errorf("content mismatch: got %q, want %q", string(content), testContent)
	}
}

