package tileutils

import (
	"bytes"

	gziplib "github.com/klauspost/compress/gzip"
)

// Gzip is a utility function to zip up a tile for storage
func Gzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	g, err := gziplib.NewWriterLevel(&buf, gziplib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := g.Write(data); err != nil {
		return nil, err
	}
	if err := g.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
