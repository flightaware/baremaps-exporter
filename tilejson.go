package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

type TileJSON struct {
	Attribution  string        `json:"attribution,omitempty"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Version      string        `json:"version,omitempty"`
	MinZoom      int           `json:"minzoom"`
	MaxZoom      int           `json:"maxzoom"`
	Bounds       []float64     `json:"bounds,omitempty"`
	Center       []float64     `json:"center,omitempty"`
	VectorLayers []VectorLayer `json:"vector_layers"`
}

type VectorLayer struct {
	ID      string        `json:"id"`
	Queries []VectorQuery `json:"queries"`
}

type VectorQuery struct {
	MinZoom int    `json:"minzoom"`
	MaxZoom int    `json:"maxzoom"`
	SQL     string `json:"sql"`
}

// ZoomLayerInfo is a mapped index of queries at each zoom.
// map[int] where int is the zoom level.
// map[string][]string where string1 is the layer name/id and []string is the list of queries.
type ZoomLayerInfo map[int]map[string][]string

func ParseTileJSON(filename string) (*TileJSON, ZoomLayerInfo, error) {
	// read the file
	jsonBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, err
	}
	// parse the tilejson content
	tj := TileJSON{
		MinZoom: -1,
		MaxZoom: -1,
	}
	err = json.Unmarshal(jsonBytes, &tj)
	if err != nil {
		return nil, nil, err
	}
	// iterate through vector layers to extract the relevant SQL at each layer
	zooms := ZoomLayerInfo{}
	for _, layer := range tj.VectorLayers {
		for _, q := range layer.Queries {
			for i := q.MinZoom; i < q.MaxZoom; i++ {
				// replace $zoom with the zoom level
				sql := strings.ReplaceAll(q.SQL, "$zoom", strconv.Itoa(i))
				if _, ok := zooms[i]; !ok {
					zooms[i] = map[string][]string{}
				}
				if _, ok := zooms[i][layer.ID]; !ok {
					zooms[i][layer.ID] = []string{}
				}
				zooms[i][layer.ID] = append(zooms[i][layer.ID], sql)
			}
		}
	}
	return &tj, zooms, nil

}
