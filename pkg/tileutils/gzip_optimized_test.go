package tileutils

import (
	"bytes"
	"io"
	"testing"

	gziplib "github.com/klauspost/compress/gzip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerBufferPool(t *testing.T) {
	pool := NewWorkerBufferPool(3, 1024*1024) // 3 buffers max, 1MB max size
	
	// Test getting buffers
	buf1 := pool.Get()
	buf2 := pool.Get()
	buf3 := pool.Get()
	
	require.NotNil(t, buf1)
	require.NotNil(t, buf2)
	require.NotNil(t, buf3)
	
	// Write some data
	buf1.WriteString("test1")
	buf2.WriteString("test2")
	buf3.WriteString("test3")
	
	// Return buffers
	pool.Put(buf1)
	pool.Put(buf2)
	pool.Put(buf3)
	
	// Get buffers again - should be reused and clean
	buf4 := pool.Get()
	buf5 := pool.Get()
	buf6 := pool.Get()
	
	assert.Equal(t, 0, buf4.Len(), "Buffer should be reset")
	assert.Equal(t, 0, buf5.Len(), "Buffer should be reset")
	assert.Equal(t, 0, buf6.Len(), "Buffer should be reset")
	
	// Check stats
	stats := pool.Stats()
	assert.Equal(t, int64(3), stats.Created, "Should have created 3 buffers")
	assert.Equal(t, int64(3), stats.Reused, "Should have reused 3 buffers")
	assert.Equal(t, float64(0.5), stats.ReuseRatio, "Reuse ratio should be 50%")
}

func TestWorkerBufferPoolMaxSize(t *testing.T) {
	pool := NewWorkerBufferPool(2, 1024) // Only 2 buffers max, 1KB max buffer size
	
	buf1 := pool.Get()
	buf2 := pool.Get()
	buf3 := pool.Get()
	
	// Write data to make buffers different sizes
	buf1.WriteString("small")
	buf2.Write(make([]byte, 2048)) // Too large - should not be kept
	buf3.WriteString("normal")
	
	// Return all buffers
	pool.Put(buf1)  // Should be kept (small size, pool has space)
	pool.Put(buf2)  // Should be discarded due to size
	pool.Put(buf3)  // Should be kept (normal size, pool still has space for 2 total)
	
	stats := pool.Stats()
	assert.Equal(t, int64(2), stats.Active, "Should keep 2 buffers (buf1 and buf3)")
	
	// Test that oversized buffer was rejected
	buf4 := pool.Get() // Should get buf1 or buf3
	buf5 := pool.Get() // Should get the other one
	buf6 := pool.Get() // Should create new buffer since pool is empty
	
	assert.Equal(t, 0, buf4.Len(), "Buffer should be reset")
	assert.Equal(t, 0, buf5.Len(), "Buffer should be reset")
	assert.Equal(t, 0, buf6.Len(), "Buffer should be reset")
}

func TestGzipOptimizedCorrectness(t *testing.T) {
	testData := []byte("Hello, World! This is a test of gzip compression with buffer pooling.")
	
	// Compress with original
	originalResult, err := Gzip(testData)
	require.NoError(t, err)
	
	// Compress with optimized worker-local approach
	pool := NewWorkerBufferPool(5, 2*1024*1024)
	compressor := NewWorkerGzipCompressor(pool, gziplib.BestCompression)
	optimizedResult, err := compressor.Compress(testData)
	require.NoError(t, err)
	
	// Both should decompress to the same original data
	originalDecompressed := decompressGzip(t, originalResult)
	optimizedDecompressed := decompressGzip(t, optimizedResult)
	
	assert.Equal(t, testData, originalDecompressed)
	assert.Equal(t, testData, optimizedDecompressed)
	
	// Results might not be identical due to different buffer usage,
	// but decompressed data should be the same
	assert.Equal(t, originalDecompressed, optimizedDecompressed)
}

func TestWorkerGzipCompressor(t *testing.T) {
	pool := NewWorkerBufferPool(3, 1024*1024)
	compressor := NewWorkerGzipCompressor(pool, gziplib.BestCompression)
	
	testData := []byte("Test data for worker gzip compressor")
	
	// Compress multiple times to test buffer reuse
	for i := 0; i < 10; i++ {
		compressed, err := compressor.Compress(testData)
		require.NoError(t, err)
		
		// Verify decompression
		decompressed := decompressGzip(t, compressed)
		assert.Equal(t, testData, decompressed)
	}
	
	// Check that buffers were reused
	stats := pool.Stats()
	assert.Greater(t, stats.ReuseRatio, 0.5, "Should have good buffer reuse ratio")
	assert.LessOrEqual(t, stats.Created, int64(3), "Should not create more than pool size")
}



func TestConcurrentWorkerPools(t *testing.T) {
	// Test that multiple worker pools work independently
	numWorkers := 4
	pools := make([]*WorkerBufferPool, numWorkers)
	compressors := make([]*WorkerGzipCompressor, numWorkers)
	
	for i := 0; i < numWorkers; i++ {
		pools[i] = NewWorkerBufferPool(3, 1024*1024)
		compressors[i] = NewWorkerGzipCompressor(pools[i], gziplib.BestCompression)
	}
	
	testData := []byte("Concurrent test data")
	
	// Run concurrent compressions
	results := make(chan []byte, numWorkers*10)
	
	for worker := 0; worker < numWorkers; worker++ {
		go func(w int) {
			for i := 0; i < 10; i++ {
				compressed, err := compressors[w].Compress(testData)
				require.NoError(t, err)
				results <- compressed
			}
		}(worker)
	}
	
	// Collect and verify results
	for i := 0; i < numWorkers*10; i++ {
		compressed := <-results
		decompressed := decompressGzip(t, compressed)
		assert.Equal(t, testData, decompressed)
	}
	
	// Verify each pool was used
	for i, pool := range pools {
		stats := pool.Stats()
		assert.Greater(t, stats.Created+stats.Reused, int64(0), 
			"Worker %d pool should have been used", i)
	}
}

// Helper function to decompress gzip data for testing
func decompressGzip(t *testing.T, data []byte) []byte {
	reader, err := gziplib.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer reader.Close()
	
	result, err := io.ReadAll(reader)
	require.NoError(t, err)
	
	return result
}