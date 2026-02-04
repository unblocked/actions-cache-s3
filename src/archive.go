package main

import (
	"archive/tar"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	zstd "github.com/klauspost/compress/zstd"
)

// Zip - Create .tar.zst file and add dirs and files that match glob patterns
// Streams directly to file to minimize memory usage
func Zip(filename string, artifacts []string) error {
	start := time.Now()
	slog.Info("starting to zip", "filename", filename)

	// Create output file first - stream directly to it instead of buffering in memory
	outFile, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(0600))
	if err != nil {
		return fmt.Errorf("failed to create output file %q: %w", filename, err)
	}
	defer outFile.Close()

	// Create zstd writer that writes directly to file
	zw, err := zstd.NewWriter(outFile, zstd.WithEncoderConcurrency(runtime.NumCPU()))
	if err != nil {
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}

	// Create tar writer on top of zstd
	tw := tar.NewWriter(zw)

	var fileCount int
	for _, pattern := range artifacts {
		slog.Debug("zipping pattern", "pattern", pattern)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			// walk through every file in the folder
			walkErr := filepath.Walk(match, func(file string, fi os.FileInfo, err error) error {
				// Check for walk errors first
				if err != nil {
					return err
				}

				// generate tar header
				header, err := tar.FileInfoHeader(fi, file)
				if err != nil {
					return err
				}

				// must provide real name
				// (see https://golang.org/src/archive/tar/common.go?#L626)
				header.Name = filepath.ToSlash(file)

				// write header
				if err := tw.WriteHeader(header); err != nil {
					return err
				}
				// if not a dir, write file content
				if !fi.IsDir() {
					data, err := os.Open(file)
					if err != nil {
						return err
					}
					defer data.Close()

					if _, err := io.Copy(tw, data); err != nil {
						return err
					}
					fileCount++
				}
				return nil
			})
			if walkErr != nil {
				return walkErr
			}
		}
	}

	// Close tar writer first
	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}

	// Close zstd writer to flush remaining data
	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to close zstd writer: %w", err)
	}

	// Get final file size
	fileInfo, err := outFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file %s: %w", filename, err)
	}

	elapsed := time.Since(start)
	slog.Info("successfully zipped", "size", getReadableBytes(fileInfo.Size()), "files", fileCount, "duration", elapsed)
	return nil
}

// ZipStream creates a streaming tar.zst archive and returns an io.ReadCloser.
// The compression happens in a goroutine, allowing the data to be streamed
// directly to S3 without creating a temp file on disk.
// The caller MUST call Close() on the returned reader when done.
func ZipStream(artifacts []string) (io.ReadCloser, <-chan error) {
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	go func() {
		defer pw.Close()
		defer close(errChan)

		// Create zstd writer that writes to the pipe
		zw, err := zstd.NewWriter(pw, zstd.WithEncoderConcurrency(runtime.NumCPU()))
		if err != nil {
			errChan <- fmt.Errorf("failed to create zstd writer: %w", err)
			return
		}

		// Create tar writer on top of zstd
		tw := tar.NewWriter(zw)

		var fileCount int
		for _, pattern := range artifacts {
			slog.Debug("streaming pattern", "pattern", pattern)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				errChan <- err
				return
			}
			for _, match := range matches {
				walkErr := filepath.Walk(match, func(file string, fi os.FileInfo, err error) error {
					if err != nil {
						return err
					}

					header, err := tar.FileInfoHeader(fi, file)
					if err != nil {
						return err
					}

					header.Name = filepath.ToSlash(file)

					if err := tw.WriteHeader(header); err != nil {
						return err
					}

					if !fi.IsDir() {
						data, err := os.Open(file)
						if err != nil {
							return err
						}
						defer data.Close()

						if _, err := io.Copy(tw, data); err != nil {
							return err
						}
						fileCount++
					}
					return nil
				})
				if walkErr != nil {
					errChan <- walkErr
					return
				}
			}
		}

		// Close tar writer first
		if err := tw.Close(); err != nil {
			errChan <- fmt.Errorf("failed to close tar writer: %w", err)
			return
		}

		// Close zstd writer to flush remaining data
		if err := zw.Close(); err != nil {
			errChan <- fmt.Errorf("failed to close zstd writer: %w", err)
			return
		}

		slog.Debug("streaming compression completed", "files", fileCount)
	}()

	return pr, errChan
}

// Unzip - Unzip all files and directories inside .tar.zst file
func Unzip(filename string) error {
	start := time.Now()
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Use all available CPU cores for decompression
	zr, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(runtime.NumCPU()))
	if err != nil {
		return err
	}
	defer zr.Close()

	tarReader := tar.NewReader(zr)

	var fileCount int
	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}
		target := filepath.ToSlash(header.Name)

		if header.Typeflag == tar.TypeReg {
			// Create the directory that contains it
			dir := filepath.Dir(target)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}

			// Write the file
			if err := extractFile(target, header, tarReader); err != nil {
				return err
			}
			fileCount++
		}
	}
	elapsed := time.Since(start)
	slog.Info("successfully unzipped", "filename", filename, "files", fileCount, "duration", elapsed)
	return nil
}

// extractFile extracts a single file from the tar reader
func extractFile(target string, header *tar.Header, tarReader *tar.Reader) error {
	fileToWrite, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
	if err != nil {
		return fmt.Errorf("failed creating %s: %w", target, err)
	}
	defer fileToWrite.Close()

	// Copy over contents
	if _, err := io.Copy(fileToWrite, tarReader); err != nil {
		return fmt.Errorf("failed copying contents to %s: %w", target, err)
	}

	if err := os.Chtimes(header.Name, header.AccessTime, header.ModTime); err != nil {
		return fmt.Errorf("failed setting timestamps to %s: %w", target, err)
	}

	return nil
}

func getReadableBytes(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "kMGTPE"[exp])
}


