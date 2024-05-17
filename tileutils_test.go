package tileutils

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTilesInBbox(t *testing.T) {
	type testCase struct {
		name     string
		bbox     BoundingBox
		zoom     int
		numTiles int
	}
	tests := []testCase{}
	for z := 0; z < 10; z++ {
		tests = append(tests, testCase{
			name: fmt.Sprintf("zoom %d", z),
			bbox: BoundingBox{
				Left:   -180,
				Right:  180,
				Top:    85,
				Bottom: -85,
			},
			zoom:     z,
			numTiles: int(math.Pow(4, float64(z))),
		})
	}
	for _, tt := range tests {
		test := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tiles := tilesInBbox(test.bbox, test.zoom)
			assert.Equal(t, test.numTiles, len(tiles))
			if test.numTiles != len(tiles) {
				fmt.Println(tiles)
			}
		})
	}

}
