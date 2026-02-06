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

// zstdEncoderOptions returns zstd encoder options based on compression level.
// Level 0 means use the default.
func zstdEncoderOptions(level int) []zstd.EOption {
	opts := []zstd.EOption{zstd.WithEncoderConcurrency(runtime.NumCPU())}
	if level > 0 {
		opts = append(opts, zstd.WithEncoderLevel(zstd.EncoderLevel(level)))
	}
	return opts
}

// Zip creates an archive from the given artifact glob patterns.
// compression controls the format: "zstd" produces .tar.zst, "none" produces a plain .tar.
// compressionLevel is only used for zstd (1-19, 0 = default).
func Zip(filename string, artifacts []string, compression string, compressionLevel int) error {
	start := time.Now()
	slog.Info("starting to zip", "filename", filename, "compression", compression)

	// Create output file first - stream directly to it instead of buffering in memory
	outFile, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(0600))
	if err != nil {
		return fmt.Errorf("failed to create output file %q: %w", filename, err)
	}
	defer outFile.Close()

	// Set up the writer chain: tar -> (optional zstd) -> file
	var tw *tar.Writer
	var zw *zstd.Encoder

	if compression == CompressionZstd {
		zw, err = zstd.NewWriter(outFile, zstdEncoderOptions(compressionLevel)...)
		if err != nil {
			return fmt.Errorf("failed to create zstd writer: %w", err)
		}
		tw = tar.NewWriter(zw)
	} else {
		tw = tar.NewWriter(outFile)
	}

	fileCount, err := archiveArtifacts(tw, artifacts)
	if err != nil {
		return err
	}

	// Close tar writer first
	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer: %w", err)
	}

	// Close zstd writer to flush remaining data (if used)
	if zw != nil {
		if err := zw.Close(); err != nil {
			return fmt.Errorf("failed to close zstd writer: %w", err)
		}
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

// archiveArtifacts walks the given glob patterns and writes matching files into the tar writer.
// Returns the number of files added.
func archiveArtifacts(tw *tar.Writer, artifacts []string) (int, error) {
	var fileCount int
	for _, pattern := range artifacts {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fileCount, err
		}
		slog.Debug("processing pattern", "pattern", pattern, "matches", len(matches))
		if len(matches) == 0 {
			slog.Warn("no matches for pattern", "pattern", pattern)
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

				// must provide real name
				// (see https://golang.org/src/archive/tar/common.go?#L626)
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
					slog.Debug("added file to archive", "file", file, "size", fi.Size())
				}
				return nil
			})
			if walkErr != nil {
				return fileCount, walkErr
			}
		}
	}
	return fileCount, nil
}

// ZipStream creates a streaming archive and returns an io.ReadCloser.
// The archiving (and optional compression) happens in a goroutine, allowing the data
// to be streamed directly to S3 without creating a temp file on disk.
// compression controls the format: "zstd" produces tar.zst, "none" produces plain tar.
// The caller MUST call Close() on the returned reader when done.
func ZipStream(artifacts []string, compression string, compressionLevel int) (io.ReadCloser, <-chan error) {
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	go func() {
		defer pw.Close()
		defer close(errChan)

		var tw *tar.Writer
		var zw *zstd.Encoder

		if compression == CompressionZstd {
			var err error
			zw, err = zstd.NewWriter(pw, zstdEncoderOptions(compressionLevel)...)
			if err != nil {
				errChan <- fmt.Errorf("failed to create zstd writer: %w", err)
				return
			}
			tw = tar.NewWriter(zw)
		} else {
			tw = tar.NewWriter(pw)
		}

		fileCount, err := archiveArtifacts(tw, artifacts)
		if err != nil {
			errChan <- err
			return
		}

		// Close tar writer first
		if err := tw.Close(); err != nil {
			errChan <- fmt.Errorf("failed to close tar writer: %w", err)
			return
		}

		// Close zstd writer to flush remaining data (if used)
		if zw != nil {
			if err := zw.Close(); err != nil {
				errChan <- fmt.Errorf("failed to close zstd writer: %w", err)
				return
			}
		}

		slog.Debug("streaming archive completed", "files", fileCount, "compression", compression)
	}()

	return pr, errChan
}

// Unzip extracts an archive created by Zip.
// compression controls the expected format: "zstd" reads tar.zst, "none" reads plain tar.
func Unzip(filename string, compression string) error {
	start := time.Now()
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	var tarReader *tar.Reader
	var zr *zstd.Decoder

	if compression == CompressionZstd {
		zr, err = zstd.NewReader(file, zstd.WithDecoderConcurrency(runtime.NumCPU()))
		if err != nil {
			return err
		}
		defer zr.Close()
		tarReader = tar.NewReader(zr)
	} else {
		tarReader = tar.NewReader(file)
	}

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
