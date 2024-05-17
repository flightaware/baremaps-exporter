package tileutils

import (
	"bytes"
	"io"
	"testing"

	gziplib "github.com/klauspost/compress/gzip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGzip(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5}
	output, err := Gzip(data)
	assert.Nil(t, err)

	// decode the gzip and make sure it is the same as the original input
	buf := bytes.NewBuffer(output)
	r, err := gziplib.NewReader(buf)
	require.Nil(t, err)
	input, err := io.ReadAll(r)
	require.Nil(t, err)
	assert.Equal(t, data, input)
}
