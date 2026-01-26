package tileutils

import (
	"bytes"

	gziplib "github.com/klauspost/compress/gzip"
)

// WorkerGzipCompressor provides optimized gzip compression using a worker-local buffer pool
type WorkerGzipCompressor struct {
	writer *gziplib.Writer
	level  int
}

// NewWorkerGzipCompressor creates a new worker-local gzip compressor
func NewWorkerGzipCompressor(level int) *WorkerGzipCompressor {
	return &WorkerGzipCompressor{
		level: level,
		// writer will be created lazily
	}
}

// Compress compresses data using the provided buffer and reused writer.
// Returns a buffer, which will almost certainly be the buffer provided as input.
func (c *WorkerGzipCompressor) Compress(data []byte, buf *bytes.Buffer) (*bytes.Buffer, error) {
	// Create or reset gzip writer
	if c.writer == nil {
		var err error
		c.writer, err = gziplib.NewWriterLevel(buf, c.level)
		if err != nil {
			return nil, err
		}
	} else {
		c.writer.Reset(buf)
	}

	// Write and close
	if _, err := c.writer.Write(data); err != nil {
		return nil, err
	}
	if err := c.writer.Close(); err != nil {
		return nil, err
	}

	// NOTE: The caller has to release the buffer back to the pool if it acquired one!
	return buf, nil
}
