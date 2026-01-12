package exporter

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/flightaware/baremaps-exporter/v2/pkg/tileutils"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	gziplib "github.com/klauspost/compress/gzip"
	"github.com/twpayne/go-mbtiles"
)

// WorkerParams holds parameters for a tile worker
type WorkerParams struct {
	Num               int
	Wg                *sync.WaitGroup
	Exporter          *Exporter
	TileList          []tileutils.TileCoords
	GzipCompression   bool
	Writer            tileutils.TileWriter
	BulkWriter        tileutils.TileBulkWriter
	Conn              *pgxpool.Conn
	GzipCompressor    *tileutils.WorkerGzipCompressor
	GzipBufferPool    *tileutils.BytesBufferPool
	TileCache         []mbtiles.TileData
	TileCachePosition uint
	TileBufferCache   []*bytes.Buffer
	Count             uint
}

// Do processes a slice of tiles for a single worker
func (p *WorkerParams) Do() {
	defer p.Wg.Done()

	// Open database connection
	_conn, err := p.Exporter.ConnectWithRetries(5)
	if err != nil {
		fmt.Printf("could not acquire connection! %v\n", err)
		return
	}
	defer _conn.Release()
	p.Conn = _conn

	// Create worker-local gzip compressor for memory optimization
	if p.GzipCompression {
		p.GzipBufferPool = tileutils.NewBytesBufferPool(int(p.Exporter.config.MbTilesBatchSize), 2*1024*1024) // match buffers to batch size, max 2MB each
		p.GzipCompressor = tileutils.NewWorkerGzipCompressor(gziplib.BestSpeed)                               // Use BestSpeed for better performance
	}

	fmt.Printf("[%d] connected, compression=%t\n", p.Num, p.GzipCompression)

	p.TileCache = make([]mbtiles.TileData, (int(p.Exporter.config.MbTilesBatchSize)))
	p.TileBufferCache = make([]*bytes.Buffer, (int(p.Exporter.config.MbTilesBatchSize)))

	// Disable JIT, it doesn't help us with highly prepared statements
	if _, err := p.Conn.Exec(context.Background(), "SET jit = off;"); err != nil {
		log.Fatalf("error configuring postgres to disable JIT: %v", err)
	}
	if p.Exporter.config.InitSQLCmd != "" {
		if _, err := p.Conn.Exec(context.Background(), p.Exporter.config.InitSQLCmd); err != nil {
			log.Fatalf("error running initialization postgres command: %s: %v", p.Exporter.config.InitSQLCmd, err)
		}
	}

	// Process all tiles in this worker's list
	for _, coord := range p.TileList {
		p.Count++
		p.Exporter.UpdateProgress(p.Num, int(p.Count))

		err := p.FetchTile(coord)
		if err != nil {
			fmt.Printf("error during fetch (%d,%d,%d): %v\n", coord.Z, coord.X, coord.Y, err)
		}
	}

	// Write remaining tiles in cache
	if p.TileCachePosition > 0 && p.BulkWriter != nil {
		err := p.BulkWriter.BulkWrite(p.TileCache[:p.TileCachePosition])
		if err != nil {
			fmt.Printf("error writing remaining tiles: %v\n", err)
		}
		// release remaining buffers
		for i, buf := range p.TileBufferCache {
			if buf != nil {
				p.GzipBufferPool.Put(buf)
				p.TileBufferCache[i] = nil
			}
		}
		p.TileCachePosition = 0
	}

	// Log buffer pool efficiency for this worker
	if p.GzipCompressor != nil {
		stats := p.GzipBufferPool.Stats()
		fmt.Printf("[%d] Buffer pool stats: Created=%d, Reused=%d, ReuseRatio=%.2f%%\n",
			p.Num, stats.Created, stats.Reused, stats.ReuseRatio*100)
	}
}

func (p *WorkerParams) FetchTile(coord tileutils.TileCoords) error {
	// Query tile from the database
	queryStr := p.Exporter.sqlQueryByZoom[coord.Z]
	start := time.Now()
	rows, err := p.Conn.Query(context.Background(), queryStr, coord.Z, coord.X, coord.Y)
	if err != nil {
		return fmt.Errorf("error querying postgres for tile (%d,%d,%d): %w", coord.Z, coord.X, coord.Y, err)
	}
	defer rows.Close()
	// DriverBytes is the raw re-usable buffer but is only valid until Scan is next called.
	// Must be used with Query and not QueryRow, since QueryRow closes rows result immediately which makes
	// accesses to the buffer unstable.
	var mvtTile pgtype.DriverBytes
	if rows.Next() {
		if err := rows.Scan(&mvtTile); err != nil {
			return fmt.Errorf("error during tile scan: %w", err)
		}
	} else {
		// Check if the query returned no rows or if an error occurred during Next()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("rows error: %w", err)
		}
		return fmt.Errorf("no tile data returned for (%d,%d,%d)", coord.Z, coord.X, coord.Y)
	}

	// Log slow tiles
	end := time.Now()
	if end.Sub(start) > time.Duration(5)*time.Second {
		fmt.Printf("[%d] slow tile: %d/%d/%d - %s\n", p.Num, coord.Z, coord.X, coord.Y, end.Sub(start))
	}

	// Apply gzip compression if needed using optimized compressor
	if p.GzipCompression && p.GzipCompressor != nil {
		buf := p.GzipBufferPool.Get()
		p.TileBufferCache[p.TileCachePosition] = buf
		_, err := p.GzipCompressor.Compress(mvtTile, buf)
		if err != nil {
			return fmt.Errorf("error compressing tile: %w", err)
		}
		mvtTile = buf.Bytes()
	}

	// Write the tile
	if p.BulkWriter != nil {
		p.TileCache[p.TileCachePosition] = mbtiles.TileData{
			Z:    coord.Z,
			X:    coord.X,
			Y:    coord.Y,
			Data: mvtTile,
		}
		p.TileCachePosition++

		if p.TileCachePosition == p.Exporter.config.MbTilesBatchSize {
			err := p.BulkWriter.BulkWrite(p.TileCache)
			if err != nil {
				return fmt.Errorf("error writing tiles: %w", err)
			}
			p.TileCachePosition = 0
			for i, buf := range p.TileBufferCache {
				if buf != nil {
					p.GzipBufferPool.Put(buf)
					p.TileBufferCache[i] = nil
				}
			}
		}
	} else {
		err := p.Writer.Write(coord.Z, coord.X, coord.Y, mvtTile)
		if err != nil {
			return fmt.Errorf("error writing tile (%d, %d, %d): %w", coord.Z, coord.X, coord.Y, err)
		}
	}
	return nil
}
