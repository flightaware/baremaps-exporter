package tileutils

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/twpayne/go-mbtiles"
)

type MbTilesMetadata map[string]string

type MbTilesFormat string

const (
	MbTilesFormatPbf  MbTilesFormat = "pbf"
	MbTilesFormatJpg  MbTilesFormat = "jpg"
	MbTilesFormatPng  MbTilesFormat = "png"
	MbTilesFormatWebP MbTilesFormat = "webp"
)

type CreateMetadataOptions struct {
	Filename string
	Version  string
	Format   MbTilesFormat
}

// CreateMetadata generates the (name,value) metadata pairs for .mbtiles files.
// Since name is required, it falls back to the filename if not provided.
// format is also required, so it falls back to pbf if not provided.
func CreateMetadata(tj *TileJSON, opts CreateMetadataOptions) MbTilesMetadata {
	format := opts.Format
	if string(format) == "" {
		format = MbTilesFormatPbf
	}
	meta := MbTilesMetadata{
		"name":   tj.Name,
		"format": string(format),
		"type":   "baselayer",
	}
	if tj.Name == "" && opts.Filename != "" {
		meta["name"] = opts.Filename
	}
	if tj.Description != "" {
		meta["description"] = tj.Description
	}
	if tj.Attribution != "" {
		meta["attribution"] = tj.Attribution
	}
	if tj.Version != "" {
		meta["version"] = tj.Version
	}
	// overwrite TileJSON version with passed in version, since that might be set in the command line
	if opts.Version != "" {
		meta["version"] = opts.Version
	}
	if tj.MinZoom != -1 {
		meta["minzoom"] = strconv.Itoa(tj.MinZoom)
	}
	if tj.MaxZoom != -1 {
		meta["maxzoom"] = strconv.Itoa(tj.MaxZoom)
	}
	if tj.Bounds != nil {
		meta["bounds"] = strings.Join(floatToString(tj.Bounds), ",")
	}
	if tj.Center != nil {
		center := ""
		if len(tj.Center) == 2 || len(tj.Center) == 3 {
			center = strings.Join(floatToString(tj.Center[:2]), ",")
		}
		if len(tj.Center) == 3 {
			center += fmt.Sprintf(",%d", int(tj.Center[2]))
		}
		meta["center"] = center
	}

	// mbtiles spec requires the json field for vector format and it's not meaningful for rasters
	if opts.Format == MbTilesFormatPbf {
		metaJSONField := CreateMetadataJSON(tj)
		if metaJSONBytes, err := json.Marshal(metaJSONField); err == nil {
			meta["json"] = string(metaJSONBytes)
		}
	}

	return meta
}

// CreateMetadataJSON generates a mbtiles MetadataJson object based on the TileJSON input
func CreateMetadataJSON(tj *TileJSON) *mbtiles.MetadataJson {
	meta := mbtiles.MetadataJson{
		VectorLayers: extractLayersFromTileJSON(tj),
	}
	return &meta
}

func extractLayersFromTileJSON(tj *TileJSON) []mbtiles.MetadataJsonVectorLayer {
	layers := make([]mbtiles.MetadataJsonVectorLayer, 0, len(tj.VectorLayers))
	for _, layer := range tj.VectorLayers {
		l := layer // create local variable copy
		layer := mbtiles.MetadataJsonVectorLayer{
			ID:     &l.ID,
			Fields: map[string]string{},
		}
		minzoom := -1
		maxzoom := -1
		// extract min/max zoom
		for _, q := range l.Queries {
			if minzoom == -1 || q.MinZoom < minzoom {
				minzoom = q.MinZoom
			}
			if q.MaxZoom > maxzoom {
				maxzoom = q.MaxZoom
			}
		}
		if minzoom == -1 {
			minzoom = 0
		}
		if maxzoom == -1 {
			maxzoom = 22
		}
		layer.MinZoom = &minzoom
		layer.MaxZoom = &maxzoom
		layers = append(layers, layer)
	}
	return layers
}
