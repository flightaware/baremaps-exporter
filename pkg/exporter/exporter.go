package exporter

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flightaware/baremaps-exporter/v2/pkg/tileutils"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/exp/slices"
)

const (
	ProgressUpdateRate = time.Duration(15) * time.Second
	MbTilesBatchSize   = 10
)

// Config holds the configuration for the exporter
type Config struct {
	TileJSON   string
	Output     string
	MbTiles    bool
	Dsn        string
	NumWorkers int
	Version    string
	Zoom       string
	TilesFile  string
}

// Exporter handles the tile export process
type Exporter struct {
	config      Config
	pool        *pgxpool.Pool
	tileJSON    *tileutils.TileJSON
	queryMap    tileutils.ZoomLayerInfo
	progress    map[int]int
	progressMux sync.Mutex
}

// NewExporter creates a new exporter instance
func NewExporter(config Config) (*Exporter, error) {
	// Parse database config
	pgConfig, err := pgxpool.ParseConfig(config.Dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Read tilejson
	tileJSON, queryMap, err := tileutils.ParseTileJSON(config.TileJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TileJSON: %w", err)
	}

	// Configure connection pool
	pgConfig.MinConns = int32(runtime.NumCPU())
	pgConfig.MaxConns = int32(2 * runtime.NumCPU())

	pool, err := pgxpool.NewWithConfig(context.Background(), pgConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	return &Exporter{
		config:   config,
		pool:     pool,
		tileJSON: tileJSON,
		queryMap: queryMap,
		progress: make(map[int]int),
	}, nil
}

// Close closes the database connection pool
func (e *Exporter) Close() {
	if e.pool != nil {
		e.pool.Close()
	}
}

// GenerateZoomLevels parses the zoom configuration and returns zoom levels
func (e *Exporter) GenerateZoomLevels() ([]int, error) {
	var zooms []int

	if e.config.Zoom != "" {
		// Parse comma-delimited zoom levels
		strZooms := strings.Split(e.config.Zoom, ",")
		zooms = make([]int, 0, len(strZooms))
		for _, z := range strZooms {
			intZoom, err := strconv.Atoi(z)
			if err != nil {
				return nil, fmt.Errorf("invalid zoom level: %s", z)
			}
			zooms = append(zooms, intZoom)
		}
	} else {
		// Use all zoom levels from TileJSON
		zooms = make([]int, 0, e.tileJSON.MaxZoom-e.tileJSON.MinZoom+1)
		for z := e.tileJSON.MinZoom; z <= e.tileJSON.MaxZoom; z++ {
			zooms = append(zooms, z)
		}
	}

	slices.Sort(zooms)

	// Update TileJSON min/max zoom to match requested output
	if len(zooms) > 0 {
		e.tileJSON.MinZoom = zooms[0]
		e.tileJSON.MaxZoom = zooms[len(zooms)-1]
	}

	return zooms, nil
}

// GenerateTileList creates the list of tiles to process
func (e *Exporter) GenerateTileList(zooms []int) ([]tileutils.TileCoords, error) {
	tiles := tileutils.ListTiles(zooms, e.tileJSON)

	// Add extra tiles from file if specified
	if e.config.TilesFile != "" {
		extraTiles, err := tileutils.TilesFromFile(e.config.TilesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read tiles from file: %w", err)
		}
		fmt.Printf("read tile coordinates from file: %d\n", len(extraTiles))
		tiles = append(tiles, extraTiles...)
	}

	return tiles, nil
}

// CreateWriters creates the appropriate tile writers based on configuration
func (e *Exporter) CreateWriters() (tileutils.TileWriter, tileutils.TileBulkWriter, func(), error) {
	var mbWriter *tileutils.MbTilesWriter

	if e.config.Output == "" {
		return &tileutils.DummyWriter{}, nil, func() {}, nil
	}

	if e.config.MbTiles {
		mbWriter = &tileutils.MbTilesWriter{
			Filename: e.config.Output,
		}
		writer, close, err := mbWriter.New()
		if err != nil {
			return nil, nil, nil, err
		}

		meta := tileutils.CreateMetadata(e.tileJSON, tileutils.CreateMetadataOptions{
			Filename: e.config.TileJSON,
			Version:  e.config.Version,
			Format:   tileutils.MbTilesFormatPbf,
		})
		err = mbWriter.BulkWriteMetadata(meta)
		if err != nil {
			return nil, nil, nil, err
		}

		return writer, mbWriter, close, nil
	}

	writer := &tileutils.FileWriter{
		Path: e.config.Output,
	}
	w, close, err := writer.New()
	return w, nil, close, err
}

// ConnectWithRetries attempts to acquire a database connection with retries
func (e *Exporter) ConnectWithRetries(numRetries int) (*pgxpool.Conn, error) {
	var lastErr error
	for i := 0; i < numRetries; i++ {
		conn, err := e.pool.Acquire(context.Background())
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	return nil, lastErr
}

// UpdateProgress updates the progress for a worker
func (e *Exporter) UpdateProgress(workerNum, count int) {
	e.progressMux.Lock()
	e.progress[workerNum] = count
	e.progressMux.Unlock()
}

// GetTotalProgress returns the total progress across all workers
func (e *Exporter) GetTotalProgress() int {
	e.progressMux.Lock()
	defer e.progressMux.Unlock()

	total := 0
	for _, count := range e.progress {
		total += count
	}
	return total
}

// ProgressReporter runs a progress reporting loop
func (e *Exporter) ProgressReporter(ctx context.Context, totalTiles int) {
	ticker := time.NewTicker(ProgressUpdateRate)
	defer ticker.Stop()

	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			current := e.GetTotalProgress()
			progress := float64(current) / float64(totalTiles) * 100.0
			elapsed := time.Duration(int(t.Sub(start).Seconds())) * time.Second

			var remaining time.Duration
			if progress > 0 {
				totalTime := time.Duration(int(elapsed.Seconds()/(progress/100.0))) * time.Second
				remaining = totalTime - elapsed
			}

			fmt.Printf("progress: %.2f%% (%s elapsed, %s remaining)\n", progress, elapsed, remaining)

			if current >= totalTiles {
				return
			}
		}
	}
}

// Export runs the complete tile export process
func (e *Exporter) Export() error {
	// Generate zoom levels
	zooms, err := e.GenerateZoomLevels()
	if err != nil {
		return err
	}

	// Generate tile list
	tiles, err := e.GenerateTileList(zooms)
	if err != nil {
		return err
	}

	tileLen := len(tiles)
	fmt.Printf("number of tiles: %d\n", tileLen)

	// Create writers
	writer, bulkWriter, closeFunc, err := e.CreateWriters()
	if err != nil {
		return err
	}
	defer closeFunc()

	// Determine number of workers
	numWorkers := e.config.NumWorkers
	if numWorkers > tileLen {
		numWorkers = tileLen
	}

	// Distribute tiles to workers
	rrTiles := tileutils.RoundRobinTiles(tiles, numWorkers)

	// Start progress reporter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.ProgressReporter(ctx, tileLen)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		worker := WorkerParams{
			Num:             i,
			Wg:              &wg,
			Exporter:        e,
			TileList:        rrTiles[i],
			Writer:          writer,
			BulkWriter:      bulkWriter,
			GzipCompression: e.config.MbTiles,
		}
		go worker.Do()
	}

	// Wait for completion
	wg.Wait()
	cancel() // Stop progress reporter

	return nil
}
