package main

const (
	// PutAction - Put artifacts
	PutAction = "put"

	// DeleteAction - Delete artifacts
	DeleteAction = "delete"

	// GetAction - Get artifacts
	GetAction = "get"

	// ErrCodeNotFound - s3 Not found error code
	ErrCodeNotFound = "NotFound"

	// Compression modes
	CompressionZstd = "zstd"
	CompressionNone = "none"
)

type (
	// Action - Input params
	Action struct {
		Action     string
		Bucket     string
		S3Class    string
		DefaultKey string
		Key        string
		Artifacts  []string

		// Compression settings
		Compression      string // "zstd" or "none"
		CompressionLevel int    // zstd level (1-19), 0 = default

		// S3 transfer settings
		UploadConcurrency   int   // number of parallel upload parts
		DownloadConcurrency int   // number of parallel download parts
		UploadPartSize      int64 // part size in bytes for uploads, 0 = auto
		DownloadPartSize    int64 // part size in bytes for downloads
	}
)

// TransferConfig returns the S3 transfer configuration derived from this Action.
func (a Action) TransferConfig() TransferConfig {
	return TransferConfig{
		UploadConcurrency:   a.UploadConcurrency,
		DownloadConcurrency: a.DownloadConcurrency,
		UploadPartSize:      a.UploadPartSize,
		DownloadPartSize:    a.DownloadPartSize,
	}
}
