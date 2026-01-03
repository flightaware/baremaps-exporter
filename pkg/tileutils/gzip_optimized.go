package tileutils

import (
	gziplib "github.com/klauspost/compress/gzip"
)

// WorkerGzipCompressor provides optimized gzip compression using a worker-local buffer pool
type WorkerGzipCompressor struct {
	BufferPool *WorkerBufferPool // Make public for stats access
	writer     *gziplib.Writer
	level      int
}

// NewWorkerGzipCompressor creates a new worker-local gzip compressor
func NewWorkerGzipCompressor(bufferPool *WorkerBufferPool, level int) *WorkerGzipCompressor {
	return &WorkerGzipCompressor{
		BufferPool: bufferPool,
		level:      level,
		// writer will be created lazily
	}
}

// Compress compresses data using the worker's buffer pool and reused writer
func (c *WorkerGzipCompressor) Compress(data []byte) ([]byte, error) {
	// Get a buffer from the pool
	buf := c.BufferPool.Get()
	defer c.BufferPool.Put(buf)
	
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
	
	// Return a copy of the compressed data
	// Note: We still need to copy because the buffer will be reused
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}



