package tileutils

import (
	"bytes"
	"sync/atomic"
)

// WorkerBufferPool provides reusable buffers for a single worker (no synchronization needed)
type WorkerBufferPool struct {
	buffers   []*bytes.Buffer
	maxSize   int
	created   int64
	reused    int64
	maxBuffer int // maximum buffer size to keep in pool
}

// NewWorkerBufferPool creates a new worker-local buffer pool
func NewWorkerBufferPool(maxSize int, maxBufferSize int) *WorkerBufferPool {
	return &WorkerBufferPool{
		buffers:   make([]*bytes.Buffer, 0, maxSize),
		maxSize:   maxSize,
		maxBuffer: maxBufferSize,
	}
}

// Get returns a clean buffer from the pool or creates a new one
func (p *WorkerBufferPool) Get() *bytes.Buffer {
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
func (p *WorkerBufferPool) Put(buf *bytes.Buffer) {
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
	// If pool is full, let the buffer be garbage collected
}

// Stats returns usage statistics for the pool
func (p *WorkerBufferPool) Stats() PoolStats {
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