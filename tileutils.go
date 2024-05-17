package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

func lonToX(lon float64, zoom int) int {
	n := math.Pow(2, float64(zoom))
	return int(math.Floor((lon + 180) / 360 * n))
}

func latToY(lat float64, zoom int) int {
	latRad := lat * math.Pi / 180
	n := math.Pow(2, float64(zoom))
	return int(math.Floor((1 - math.Log(math.Tan(latRad)+1/math.Cos(latRad))/math.Pi) / 2 * n))
}

// BoundingBox is a lat/lon set of coordinates for a bounding box
type BoundingBox struct {
	Left   float64
	Right  float64
	Top    float64
	Bottom float64
}

// ListTiles returns a list of all the tiles within the given zooms based on the TileJSON
func ListTiles(zooms []int, tj *TileJSON) []TileCoords {
	tiles := make([]TileCoords, 0, 2<<zooms[len(zooms)-1])
	for _, z := range zooms {
		newTiles := tilesInBbox(BoundingBox{
			Left:   tj.Bounds[0],
			Right:  tj.Bounds[2],
			Bottom: tj.Bounds[1],
			Top:    tj.Bounds[3],
		}, z)
		tiles = append(tiles, newTiles...)
	}
	return tiles
}

// tilesInBbox returns a list of all tiles within that lat/lon bounding box at the specified zoom level
func tilesInBbox(bbox BoundingBox, zoom int) []TileCoords {
	fmt.Printf("zoom: %d\n", zoom)
	xMin := lonToX(bbox.Left, zoom)
	xMax := lonToX(bbox.Right, zoom)
	yMin := latToY(bbox.Top, zoom)
	yMax := latToY(bbox.Bottom, zoom)
	tileMax := (1 << zoom) - 1

	if xMax > tileMax {
		xMax = tileMax
	}
	if yMax > tileMax {
		yMax = tileMax
	}

	tiles := make([]TileCoords, 0, (xMax-xMin)*(yMax-yMin))

	// cluster tiles in "steps" so we aren't iterating over the whole globe
	// instead, try to keep the tiles clustered together
	numSteps := 4.0
	stepX := int(math.Ceil(float64(xMax-xMin+1)/numSteps)) + 1
	stepY := int(math.Ceil(float64(yMax-yMin+1)/numSteps)) + 1
	for sY := 0; sY < int(numSteps); sY++ {
		stepYMin := yMin + (sY * stepY)
		stepYMax := yMin + ((sY + 1) * stepY)
		if stepYMax > yMax {
			stepYMax = yMax + 1
		}
		for sX := 0; sX < int(numSteps); sX++ {
			stepXMin := xMin + (sX * stepX)
			stepXMax := xMin + ((sX + 1) * stepX)
			if stepXMax > xMax {
				stepXMax = xMax + 1
			}
			for i := stepXMin; i < stepXMax; i++ {
				for j := stepYMin; j < stepYMax; j++ {
					tiles = append(tiles, TileCoords{
						Z: zoom,
						X: i,
						Y: j,
					})
				}
			}
		}
	}
	return tiles
}

// RoundRobinTiles assigns tiles to workers in round robin fashion
func RoundRobinTiles(input []TileCoords, numWorkers int) [][]TileCoords {
	out := make([][]TileCoords, numWorkers)
	for i, v := range input {
		index := i % numWorkers
		out[index] = append(out[index], v)
	}
	return out
}

// tilesFromFile reads the tile coordinates to generate from a file
func tilesFromFile(filename string) ([]TileCoords, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("unable to read file (%s): %w", filename, err)
	}
	lines := strings.Split(string(data), "\n")
	tiles := make([]TileCoords, 0, len(lines))
	for _, l := range lines {
		if l == "" {
			continue
		}
		coords := strings.Split(l, "/")
		if len(coords) != 3 {
			return nil, fmt.Errorf("invalid line, expected 3 coordinates but got %d: %s", len(coords), l)
		}
		z, err := strconv.Atoi(coords[0])
		if err != nil {
			return nil, err
		}
		x, err := strconv.Atoi(coords[1])
		if err != nil {
			return nil, err
		}
		y, err := strconv.Atoi(coords[2])
		if err != nil {
			return nil, err
		}
		tc := TileCoords{
			Z: z,
			X: x,
			Y: y,
		}
		tiles = append(tiles, tc)
	}
	return tiles, nil
}
