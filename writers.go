package tileutils

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/twpayne/go-mbtiles"
)

const (
	mbTilesInsertRetries = 5
)

// TileWriter abstracts how to write out tiles, allowing for different formats and implementations
type TileWriter interface {
	// New creates a new TileWriter for this type
	New() (TileWriter, func(), error)
	// Write commits a tile with tileData at the Z/X/Y coordinate
	Write(z, x, y int, tileData []byte) error
}

// TileBulkWriter extends the TileWriter interface to include the ability to write out tiles in bulk
type TileBulkWriter interface {
	// BulkWrite commits a slice of tiles
	BulkWrite(data []mbtiles.TileData) error
}

// FileWriter writes tiles out to a directory structure.
// The directories are organized under the Path provided,
// like Path/{z}/{x}/{y}.mvt
type FileWriter struct {
	Path string
}

func (fw *FileWriter) Write(z, x, y int, tileData []byte) error {
	basePath := path.Join(fw.Path, strconv.Itoa(z), strconv.Itoa(x))
	err := os.MkdirAll(basePath, 0755)
	if err != nil {
		fmt.Printf("error making directory for output (%s): %v\n", basePath, err)
		return err
	}
	filename := path.Join(basePath, fmt.Sprintf("%d.mvt", y))
	return os.WriteFile(filename, tileData, 0644)
}

func (fw *FileWriter) New() (TileWriter, func(), error) {
	return fw, func() {}, nil
}

// DummyWriter doesn't do anything. It outputs info about the tile to be written
// to stdout. It doesn't actually write any tiles.
type DummyWriter struct{}

func (fw *DummyWriter) Write(z, x, y int, tileData []byte) error {
	fmt.Printf("%d/%d/%d - %d bytes\n", z, x, y, len(tileData))
	return nil
}

func (fw *DummyWriter) New() (TileWriter, func(), error) {
	return fw, func() {}, nil
}

// MbTilesWriter outputs tiles to a mbtiles file.
//
// Parameters:
//   - Filename: the output file to be written
//   - Writer: an instance of mbtiles.Writer to be used when writing the tiles
type MbTilesWriter struct {
	Filename string
	Writer   *mbtiles.Writer
}

func (w *MbTilesWriter) Write(z, x, y int, tileData []byte) error {
	var err error
	for i := 0; i < mbTilesInsertRetries; i++ {
		err = w.Writer.InsertTile(z, x, y, tileData)
		if err == nil {
			return nil
		}
		fmt.Printf("err during database write, waiting to retry (%d): %v\n", i, err)
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	return err
}

func (w *MbTilesWriter) BulkWrite(data []mbtiles.TileData) error {
	var err error
	for i := 0; i < mbTilesInsertRetries; i++ {
		err = w.Writer.BulkInsertTile(data)
		if err == nil {
			return nil
		}
		fmt.Printf("err during database write, waiting to retry (%d): %v\n", i, err)
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	return err
}

func (w *MbTilesWriter) WriteMetadata(name, value string) error {
	return w.Writer.InsertMetadata(name, value)
}

func (w *MbTilesWriter) BulkWriteMetadata(meta MbTilesMetadata) error {
	for name, value := range meta {
		if err := w.WriteMetadata(name, value); err != nil {
			return err
		}
	}
	return nil
}

func (w *MbTilesWriter) New() (TileWriter, func(), error) {
	// sqlite3 relies on you to create the file first
	if err := os.MkdirAll(path.Dir(w.Filename), 0755); err != nil {
		return nil, nil, err
	}
	if _, err := os.Create(w.Filename); err != nil {
		return nil, nil, err
	}
	// create a mbtiles writer, which is a wrapper around sqlite3
	_writer, err := mbtiles.NewWriter(w.Filename)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating writer: %w", err)
	}
	// create the tiles table
	if err := _writer.CreateTiles(); err != nil {
		return nil, nil, fmt.Errorf("error creating tiles table: %w", err)
	}
	// create the metadata view
	if err := _writer.CreateMetadata(); err != nil {
		return nil, nil, fmt.Errorf("error creating metadata table: %w", err)
	}
	// drop the tiles index
	if err := _writer.DeleteTileIndex(); err != nil {
		return nil, nil, fmt.Errorf("error deleting tile index: %w", err)
	}
	// set optimizations
	if err := _writer.SetOptimizations(mbtiles.Optimizations{
		JournalModeMemory: true,
	}); err != nil {
		return nil, nil, fmt.Errorf("error setting optimizations: %w", err)
	}

	w.Writer = _writer
	return w,
		func() {
			w.Writer.CreateTileIndex()
			w.Writer.Close()
		},
		nil
}
