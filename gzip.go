package main

import (
	"bytes"

	gziplib "github.com/klauspost/compress/gzip"
)

// gzip is a utility function to zip up a tile for storage
func gzip(data []byte) ([]byte, error) {
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
