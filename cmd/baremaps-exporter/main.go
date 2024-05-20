package main

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flightaware/baremaps-exporter/v2/pkg/tileutils"

	"github.com/alexflint/go-arg"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twpayne/go-mbtiles"
	"golang.org/x/exp/slices"
)

const (
	progressUpdateRate = time.Duration(15) * time.Second
	mbTilesBatchSize   = 10
)

var (
	workerProgress      = map[int]int{} // workerProgress checks how many tiles each worker has completed
	workerProgressMutex sync.Mutex
)

type Args struct {
	TileJSON   string `arg:"positional,required" help:"input tilejson file"`
	Output     string `arg:"-o,--output" help:"output file or directory"`
	MbTiles    bool   `arg:"--mbtiles" help:"output mbtiles instead of files (automatically selected if output filename ends in '.mbtiles')"`
	Dsn        string `arg:"-d,--dsn" help:"database connection string (dsn) for postgis"`
	NumWorkers int    `arg:"-w,--workers" help:"number of workers to spawn"`
	Version    string `arg:"--tileversion" help:"version of the tileset (string) written to mbtiles metadata"`
	Zoom       string `arg:"--zoom" help:"comma-delimited set specific zooms to export (eg: 2,4,6,8)"`
	TilesFile  string `arg:"-f,--file" help:"a list of tiles to also generate, from a file where each line is a z/x/y tile coordinate"`
}

func (Args) Description() string {
	return "export baremaps-compatible tilesets from a postgis server"
}

type WorkerParams struct {
	Num             int                      // worker number
	Wg              *sync.WaitGroup          // waitgroup to signal when completed
	Args            Args                     // input args
	TileList        []tileutils.TileCoords   // coords that this worker should process
	QueryMap        tileutils.ZoomLayerInfo  // a map of the queries relevant at each zoom level
	GzipCompression bool                     // true if gzip compression should be used
	Writer          tileutils.TileWriter     // writer to use for output
	BulkWriter      tileutils.TileBulkWriter // bulk writer if available
	Pool            *pgxpool.Pool            // postgres connection pool
}

// newWriters creates a TileWriter and TileBulkWriter based on the input arguments
func newWriters(args Args, tj *tileutils.TileJSON) (writer tileutils.TileWriter, bulkWriter tileutils.TileBulkWriter, close func(), err error) {
	var mbWriter *tileutils.MbTilesWriter
	if args.Output == "" {
		writer = &tileutils.DummyWriter{}
		return
	}
	if args.MbTiles {
		mbWriter = &tileutils.MbTilesWriter{
			Filename: args.Output,
		}
		writer = mbWriter
		bulkWriter = mbWriter
		writer, close, err = mbWriter.New()
		if err != nil {
			return
		}
		meta := tileutils.CreateMetadata(tj, tileutils.CreateMetadataOptions{
			Filename: args.TileJSON,
			Version:  args.Version,
			Format:   tileutils.MbTilesFormatPbf,
		})
		err = mbWriter.BulkWriteMetadata(meta)
		return
	}
	writer = &tileutils.FileWriter{
		Path: args.Output,
	}
	writer, close, err = writer.New()
	return
}

func connectWithRetries(pool *pgxpool.Pool, numRetries int) (*pgxpool.Conn, error) {
	var lastErr error
	for i := 0; i < numRetries; i++ {
		conn, err := pool.Acquire(context.Background())
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	return nil, lastErr
}

func progressReporter(total int, numWorkers int) {
	ticker := time.NewTicker(progressUpdateRate)
	start := time.Now()
	for {
		t := <-ticker.C
		counter := 0
		workerProgressMutex.Lock()
		for _, v := range workerProgress {
			counter += v
		}
		workerProgressMutex.Unlock()
		progress := float64(counter) / float64(total) * 100.0
		elapsed := time.Duration(int(t.Sub(start).Seconds())) * time.Second
		var remaining time.Duration
		totalTime := time.Duration(int(elapsed.Seconds()/(progress/100.0))) * time.Second
		remaining = totalTime - elapsed
		fmt.Printf("progress: %.2f%% (%s elapsed, %s remaining)\n", progress, elapsed, remaining)
		if counter == total {
			break
		}
	}
}

func tileWorker(params WorkerParams) {
	// open db connection
	conn, err := connectWithRetries(params.Pool, 5)
	if err != nil {
		fmt.Printf("could not acquire connection! %v\n", err)
		params.Wg.Done()
		return
	}
	fmt.Printf("[%d] connected, compression=%t\n", params.Num, params.GzipCompression)
	defer conn.Release()

	tileCache := make([]mbtiles.TileData, mbTilesBatchSize)
	tileCachePos := 0
	count := 0
	// extract all the tiles in this worker's list
	for _, c := range params.TileList {
		start := time.Now()
		count += 1
		workerProgressMutex.Lock()
		workerProgress[params.Num] = count
		workerProgressMutex.Unlock()
		queryStr := "SELECT "
		layerCount := 0
		for layerName, sqlStmts := range params.QueryMap[c.Z] {
			if layerCount > 0 {
				queryStr += "||"
			}
			sql := "(WITH mvtgeom AS ("
			for i, query := range sqlStmts {
				template := "(SELECT ST_AsMVTGeom(t.geom, ST_TileEnvelope(%d, %d, %d)) AS geom, t.tags, t.id " +
					"FROM (%s) AS t " +
					"WHERE t.geom && ST_TileEnvelope(%d, %d, %d, margin => (64.0/4096)))"
				_sql := fmt.Sprintf(template,
					c.Z, c.X, c.Y,
					strings.ReplaceAll(query, ";", ""),
					c.Z, c.X, c.Y)
				if i != 0 {
					sql += " UNION "
				}
				sql += _sql
			}
			queryStr += sql + fmt.Sprintf(") SELECT ST_AsMVT(mvtgeom.*, '%s') FROM mvtgeom )", layerName)
			layerCount++
		}
		queryStr += " mvtTile;"
		row := conn.QueryRow(context.Background(), queryStr)
		var mvtTile []byte
		err = row.Scan(&mvtTile)
		if err != nil {
			fmt.Printf("error during tile generation (%d,%d,%d): %v\n", c.Z, c.X, c.Y, err)
			continue
		}
		if params.GzipCompression {
			compressed, err := tileutils.Gzip(mvtTile)
			if err != nil {
				fmt.Printf("error compressing tile: %v\n", err)
			}
			mvtTile = compressed
		}
		end := time.Now()
		if end.Sub(start) > time.Duration(5)*time.Second {
			fmt.Printf("[%d] slow tile: %d/%d/%d - %s\n", params.Num, c.Z, c.X, c.Y, end.Sub(start))
			fmt.Println(queryStr)
		}

		if params.BulkWriter != nil {
			tileCache[tileCachePos] = mbtiles.TileData{
				Z:    c.Z,
				X:    c.X,
				Y:    c.Y,
				Data: mvtTile,
			}
			tileCachePos++
			if tileCachePos == mbTilesBatchSize {
				err := params.BulkWriter.BulkWrite(tileCache)
				if err != nil {
					fmt.Printf("error writing tiles")
					continue
				}
				tileCachePos = 0
			}

		} else {
			err := params.Writer.Write(c.Z, c.X, c.Y, mvtTile)
			if err != nil {
				fmt.Printf("error writing tile (%d, %d, %d): %v\n", c.Z, c.X, c.Y, err)
				continue
			}
		}
	}

	if tileCachePos > 0 && params.BulkWriter != nil {
		err := params.BulkWriter.BulkWrite(tileCache[:tileCachePos])
		if err != nil {
			fmt.Printf("error writing tiles")
		}
	}
	// signal we're done
	params.Wg.Done()

}

func main() {
	args := Args{
		NumWorkers: runtime.NumCPU(),
	}
	arg.MustParse(&args)
	if strings.HasSuffix(args.Output, ".mbtiles") {
		args.MbTiles = true
	}

	// open postgres pool
	config, err := pgxpool.ParseConfig(args.Dsn)
	if err != nil {
		panic(err)
	}

	// read tilejson
	tileJSON, tileMap, err := tileutils.ParseTileJSON(args.TileJSON)
	if err != nil {
		panic(err)
	}

	config.MinConns = int32(runtime.NumCPU())
	config.MaxConns = int32(2 * runtime.NumCPU())
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	var zooms []int
	// if comma-delimited list of zooms, use those
	if args.Zoom != "" {
		strZooms := strings.Split(args.Zoom, ",")
		zooms = make([]int, 0, len(strZooms))
		for _, z := range strZooms {
			intZoom, err := strconv.Atoi(z)
			if err != nil {
				panic(err)
			}
			zooms = append(zooms, intZoom)
		}
	} else {
		zooms = make([]int, 0, tileJSON.MaxZoom-tileJSON.MinZoom+1)
		for z := tileJSON.MinZoom; z <= tileJSON.MaxZoom; z++ {
			zooms = append(zooms, z)
		}
	}
	slices.Sort(zooms)
	// reset min/max zoom to match requested output
	tileJSON.MinZoom = zooms[0]
	tileJSON.MaxZoom = zooms[len(zooms)-1]

	tiles := tileutils.ListTiles(zooms, tileJSON)
	if args.TilesFile != "" {
		extraTiles, err := tileutils.TilesFromFile(args.TilesFile)
		if err != nil {
			panic(err)
		}
		fmt.Printf("read tile coordinates from file: %d\n", len(extraTiles))
		tiles = append(tiles, extraTiles...)
	}
	tileLen := len(tiles)
	fmt.Printf("number of tiles: %d\n", tileLen)

	writer, bulkWriter, close, err := newWriters(args, tileJSON)
	if err != nil {
		panic(err)
	}

	numWorkers := args.NumWorkers
	if numWorkers > tileLen {
		numWorkers = tileLen
	}
	// round robin the tiles so workers are hitting similar geospatial entries and zoom at the same time
	rrTiles := tileutils.RoundRobinTiles(tiles, numWorkers)
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerTiles := rrTiles[i]
		params := WorkerParams{
			Num:             i,
			Wg:              &wg,
			Args:            args,
			Pool:            pool,
			QueryMap:        tileMap,
			Writer:          writer,
			BulkWriter:      bulkWriter,
			TileList:        workerTiles,
			GzipCompression: args.MbTiles,
		}
		go tileWorker(params)
	}
	go progressReporter(tileLen, numWorkers)

	wg.Wait()
	close()
}
