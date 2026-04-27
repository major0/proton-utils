package client

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/major0/proton-cli/api/drive"
)

// LocalReader reads blocks from a local file. It holds no file
// descriptor itself — each worker gets its own FD via CloneReader.
// The original is used only for metadata (block count, size).
type LocalReader struct {
	path    string
	size    int64
	nBlocks int
	f       *os.File // nil on the template; set on clones
}

// NewLocalReader creates a BlockReader template for a local file.
func NewLocalReader(path string, size int64) *LocalReader {
	return &LocalReader{
		path:    path,
		size:    size,
		nBlocks: drive.BlockCount(size),
	}
}

// CloneReader opens a new file descriptor for a worker.
func (r *LocalReader) CloneReader() (BlockReader, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	return &LocalReader{
		path:    r.path,
		size:    r.size,
		nBlocks: r.nBlocks,
		f:       f,
	}, nil
}

// ReadBlock reads block at index into buf using pread. If no file
// descriptor is open (template instance), one is opened lazily.
func (r *LocalReader) ReadBlock(_ context.Context, index int, buf []byte) (int, error) {
	if r.f == nil {
		f, err := os.Open(r.path)
		if err != nil {
			return 0, err
		}
		r.f = f
	}
	offset := int64(index) * drive.BlockSize
	sz := r.BlockSize(index)
	n, err := r.f.ReadAt(buf[:sz], offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	return n, nil
}

// BlockCount returns the total number of blocks.
func (r *LocalReader) BlockCount() int { return r.nBlocks }

// BlockSize returns the size of block at index.
func (r *LocalReader) BlockSize(index int) int64 {
	offset := int64(index) * drive.BlockSize
	remaining := r.size - offset
	if remaining <= 0 {
		return 0
	}
	if remaining > drive.BlockSize {
		return drive.BlockSize
	}
	return remaining
}

// Describe returns the file path.
func (r *LocalReader) Describe() string { return r.path }

// TotalSize returns the file size.
func (r *LocalReader) TotalSize() int64 { return r.size }

// Close closes the file descriptor if this is a clone.
func (r *LocalReader) Close() error {
	if r.f != nil {
		return r.f.Close()
	}
	return nil
}

// LocalWriter writes blocks to a local file. Like LocalReader, it
// holds no file descriptor — workers get their own via CloneWriter.
type LocalWriter struct {
	path string
	f    *os.File // nil on the template; set on clones
}

// NewLocalWriter creates a BlockWriter template for a local file.
func NewLocalWriter(path string) *LocalWriter {
	return &LocalWriter{path: path}
}

// CloneWriter opens a new file descriptor for a worker.
func (w *LocalWriter) CloneWriter() (BlockWriter, error) {
	f, err := os.OpenFile(w.path, os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &LocalWriter{path: w.path, f: f}, nil
}

// WriteBlock writes data at the correct offset using pwrite. If no
// file descriptor is open (template instance), one is opened lazily.
func (w *LocalWriter) WriteBlock(_ context.Context, index int, data []byte) error {
	if w.f == nil {
		f, err := os.OpenFile(w.path, os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		w.f = f
	}
	offset := int64(index) * drive.BlockSize
	_, err := w.f.WriteAt(data, offset)
	return err
}

// Describe returns the file path.
func (w *LocalWriter) Describe() string { return w.path }

// Close closes the file descriptor if this is a clone.
func (w *LocalWriter) Close() error {
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}
