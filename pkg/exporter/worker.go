package exporter

import (
	"bytes"
	"context"
	"fmt"
	"strings"
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
		p.GzipBufferPool = tileutils.NewBytesBufferPool(MbTilesBatchSize, 2*1024*1024) // match buffers to batch size, max 2MB each
		p.GzipCompressor = tileutils.NewWorkerGzipCompressor(gziplib.BestSpeed)        // Use BestSpeed for better performance
	}

	fmt.Printf("[%d] connected, compression=%t\n", p.Num, p.GzipCompression)

	p.TileCache = make([]mbtiles.TileData, MbTilesBatchSize)
	p.TileBufferCache = make([]*bytes.Buffer, MbTilesBatchSize)

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
	queryStr := p.queryString(coord)
	start := time.Now()
	rows, err := p.Conn.Query(context.Background(), queryStr)
	if err != nil {
		return fmt.Errorf("error querying postgres for tile (%d,%d,%d): %w", coord.Z, coord.X, coord.Y, err)
	}
	defer rows.Close()
	// DriverBytes is the raw re-usable buffer but is only valid until Scan is next called.
	// Must be used with Query and not QueryRow, since QueryRow closes rows result immediately which makes
	// accesses to the buffer unstable.
	var mvtTile pgtype.DriverBytes
	if err := rows.Scan(&mvtTile); err != nil {
		return fmt.Errorf("error during tile generation (%d,%d,%d): %w", coord.Z, coord.X, coord.Y, err)
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

		if p.TileCachePosition == MbTilesBatchSize {
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

func (p *WorkerParams) queryString(coord tileutils.TileCoords) string {
	queryStr := "SELECT "
	layerCount := 0

	for layerName, sqlStmts := range p.Exporter.queryMap[coord.Z] {
		if layerCount > 0 {
			queryStr += "||"
		}
		sql := "(WITH mvtgeom AS ("
		for i, query := range sqlStmts {
			template := "(SELECT ST_AsMVTGeom(t.geom, ST_TileEnvelope(%d, %d, %d)) AS geom, t.tags, t.id " +
				"FROM (%s) AS t " +
				"WHERE t.geom && ST_TileEnvelope(%d, %d, %d, margin => (64.0/4096)))"
			_sql := fmt.Sprintf(template,
				coord.Z, coord.X, coord.Y,
				strings.ReplaceAll(query, ";", ""),
				coord.Z, coord.X, coord.Y)
			if i != 0 {
				sql += " UNION "
			}
			sql += _sql
		}
		queryStr += sql + fmt.Sprintf(") SELECT ST_AsMVT(mvtgeom.*, '%s') FROM mvtgeom )", layerName)
		layerCount++
	}
	queryStr += " mvtTile;"
	return queryStr
}
