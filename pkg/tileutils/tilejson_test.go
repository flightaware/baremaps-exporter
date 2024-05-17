package tileutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTileJSON(t *testing.T) {
	assert := assert.New(t)
	tj, lq, err := ParseTileJSON("./testdata/tiles.json")
	assert.Nil(err)

	assert.Equal(tj.Attribution, "for me")
	assert.Equal(tj.MinZoom, 0)
	assert.Equal(tj.MaxZoom, 14)

	// check that zoom 12 has 2 keys
	assert.Len(lq[12], 2)
	assert.Contains(lq[12]["ocean"], "SELECT id, tags, geom FROM osm_ocean")
	assert.Contains(lq[12]["labels"], "SELECT id, tags, geom FROM big_labels")
	assert.Contains(lq[12]["labels"], "SELECT id, tags, geom FROM small_labels")
}
