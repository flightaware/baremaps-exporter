package main

import (
	"runtime"
	"strings"

	"github.com/alexflint/go-arg"
	"github.com/flightaware/baremaps-exporter/v2/pkg/exporter"
)

type Args struct {
	TileJSON   string `arg:"positional,required" help:"input tilejson file"`
	Output     string `arg:"-o,--output" help:"output file or directory"`
	MbTiles    bool   `arg:"--mbtiles" help:"output mbtiles instead of files (automatically selected if output filename ends in '.mbtiles')"`
	Dsn        string `arg:"-d,--dsn" help:"database connection string (dsn) for postgis"`
	NumWorkers int    `arg:"-w,--workers" help:"number of workers to spawn"`
	BatchSize  uint   `arg:"-b,--batch" help:"size of the batch to query and write at once"`
	Version    string `arg:"--tileversion" help:"version of the tileset (string) written to mbtiles metadata"`
	Zoom       string `arg:"--zoom" help:"comma-delimited set specific zooms to export (eg: 2,4,6,8)"`
	TilesFile  string `arg:"-f,--file" help:"a list of tiles to also generate, from a file where each line is a z/x/y tile coordinate"`
}

func (Args) Description() string {
	return "export baremaps-compatible tilesets from a postgis server"
}

func main() {
	// defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()
	args := Args{
		NumWorkers: runtime.NumCPU(),
		BatchSize:  10,
	}
	arg.MustParse(&args)

	if strings.HasSuffix(args.Output, ".mbtiles") {
		args.MbTiles = true
	}

	config := exporter.Config{
		TileJSON:         args.TileJSON,
		Output:           args.Output,
		MbTiles:          args.MbTiles,
		Dsn:              args.Dsn,
		NumWorkers:       args.NumWorkers,
		Version:          args.Version,
		Zoom:             args.Zoom,
		TilesFile:        args.TilesFile,
		MbTilesBatchSize: args.BatchSize,
	}

	exp, err := exporter.NewExporter(config)
	if err != nil {
		panic(err)
	}
	defer exp.Close()

	err = exp.Export()
	if err != nil {
		panic(err)
	}
}
