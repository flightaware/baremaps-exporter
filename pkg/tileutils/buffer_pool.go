package tileutils

import (
	"bytes"
	"sync/atomic"
)

// BytesBufferPool provides reusable buffers for a single worker (no synchronization needed)
type BytesBufferPool struct {
	buffers   []*bytes.Buffer
	maxSize   int
	created   int64
	reused    int64
	maxBuffer int // maximum buffer size to keep in pool
}

// NewBytesBufferPool creates a new worker-local buffer pool
func NewBytesBufferPool(maxPoolSize, maxBufferSize int) *BytesBufferPool {
	return &BytesBufferPool{
		buffers:   make([]*bytes.Buffer, 0, maxPoolSize),
		maxSize:   maxPoolSize,
		maxBuffer: maxBufferSize,
	}
}

// Get returns a clean buffer from the pool or creates a new one
func (p *BytesBufferPool) Get() *bytes.Buffer {
	if len(p.buffers) > 0 {
		// Pop from the end for better performance
		buf := p.buffers[len(p.buffers)-1]
		p.buffers = p.buffers[:len(p.buffers)-1]
		atomic.AddInt64(&p.reused, 1)
		return buf
	}

	// Create new buffer if pool is empty
	atomic.AddInt64(&p.created, 1)
	return &bytes.Buffer{}
}

// Put returns a buffer to the pool after resetting it
func (p *BytesBufferPool) Put(buf *bytes.Buffer) {
	if buf == nil {
		return
	}

	// Don't keep buffers that are too large to prevent memory bloat
	if buf.Cap() > p.maxBuffer {
		return
	}

	// Reset the buffer
	buf.Reset()

	// Add to pool if there's space
	if len(p.buffers) < p.maxSize {
		p.buffers = append(p.buffers, buf)
	}
	// Else if pool is full, let the buffer be garbage collected
}

// Stats returns usage statistics for the pool
func (p *BytesBufferPool) Stats() PoolStats {
	created := atomic.LoadInt64(&p.created)
	reused := atomic.LoadInt64(&p.reused)
	total := created + reused

	var reuseRatio float64
	if total > 0 {
		reuseRatio = float64(reused) / float64(total)
	}

	return PoolStats{
		Created:    created,
		Reused:     reused,
		Active:     int64(len(p.buffers)),
		ReuseRatio: reuseRatio,
	}
}

// PoolStats contains buffer pool usage statistics
type PoolStats struct {
	Created    int64   // Total buffers created
	Reused     int64   // Total buffer reuses
	Active     int64   // Buffers currently in pool
	ReuseRatio float64 // Ratio of reused vs created
}
