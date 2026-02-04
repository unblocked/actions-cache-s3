package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	zstd "github.com/klauspost/compress/zstd"
)

// Zip - Create .zip file and add dirs and files that match glob patterns
func Zip(filename string, artifacts []string) error {
	start := time.Now()
	slog.Info("starting to zip", "filename", filename)
	// tar + gzip
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(5))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)

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

				slog.Debug("adding file to archive", "file", file)

				PrintMemUsage()

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
				}
				return nil
			})
			if walkErr != nil {
				return walkErr
			}
		}
	}

	// produce tar
	if err := tw.Close(); err != nil {
		return err
	}
	// produce gzip
	if err := zw.Close(); err != nil {
		return err
	}

	// write the .tar.gzip
	fileToWrite, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, os.FileMode(0600))
	if err != nil {
		return fmt.Errorf("failed to open file %q: %w", filename, err)
	}
	defer fileToWrite.Close()

	if _, err := io.Copy(fileToWrite, &buf); err != nil {
		return fmt.Errorf("failed copying buffer to open file %s: %w", filename, err)
	}

	elapsed := time.Since(start)
	file, err := fileToWrite.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file %s: %w", filename, err)
	}

	slog.Info("successfully zipped", "size", getReadableBytes(file.Size()), "duration", elapsed)
	return nil
}

// Unzip - Unzip all files and directories inside .zip file
func Unzip(filename string) error {
	start := time.Now()
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	zr, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(5))
	if err != nil {
		return err
	}
	defer zr.Close()

	tarReader := tar.NewReader(zr)

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
		}
	}
	elapsed := time.Since(start)
	slog.Info("successfully unzipped", "filename", filename, "duration", elapsed)
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

func PrintMemUsage() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Live objects = Mallocs - Frees
	liveObjects := m.Mallocs - m.Frees

	// For info on each, see: https://golang.org/pkg/runtime/#MemStats
	slog.Debug("memory usage",
		"alloc_mib", bToMb(m.Alloc),
		"total_alloc_mib", bToMb(m.TotalAlloc),
		"sys_mib", bToMb(m.Sys),
		"live_objects", liveObjects,
		"heap_alloc_mib", bToMb(m.HeapAlloc),
		"num_gc", m.NumGC,
	)
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}
