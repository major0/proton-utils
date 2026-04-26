package driveCmd

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/major0/proton-cli/api/drive"
	"github.com/major0/proton-cli/api/drive/client"
)

// CopyJob is a fully resolved source/destination pair.
type CopyJob struct {
	Src client.BlockReader
	Dst client.BlockWriter
}

// TransferOpts configures bulk transfer behavior.
type TransferOpts struct {
	Progress func(completed, total int, bytes int64, rate float64)
	Verbose  func(src, dst string)
}

// CloneableReader is implemented by BlockReaders that support per-worker
// cloning. Each clone opens its own file descriptor so workers avoid
// sharing kernel-level FD state.
type CloneableReader interface {
	CloneReader() (client.BlockReader, error)
}

// CloneableWriter is implemented by BlockWriters that support per-worker
// cloning.
type CloneableWriter interface {
	CloneWriter() (client.BlockWriter, error)
}

// LocalReader reads blocks from a local file. It holds no file
// descriptor itself — each worker gets its own FD via CloneReader.
// The original is used only for metadata (block count, size).
type LocalReader struct {
	path    string
	size    int64
	nBlocks int
	f       *os.File // nil on the template; set on clones
	direct  bool     // true if O_DIRECT / F_NOCACHE is active
}

// NewLocalReader creates a BlockReader template for a local file.
func NewLocalReader(path string, size int64) *LocalReader {
	return &LocalReader{
		path:    path,
		size:    size,
		nBlocks: drive.BlockCount(size),
	}
}

// CloneReader opens a new file descriptor for a worker, attempting
// direct I/O to bypass the page cache.
func (r *LocalReader) CloneReader() (client.BlockReader, error) {
	f, direct, err := openDirect(r.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return &LocalReader{
		path:    r.path,
		size:    r.size,
		nBlocks: r.nBlocks,
		f:       f,
		direct:  direct,
	}, nil
}

// ReadBlock reads block at index into buf using pread. If no file
// descriptor is open (template instance), one is opened lazily.
func (r *LocalReader) ReadBlock(_ context.Context, index int, buf []byte) (int, error) {
	if r.f == nil {
		f, direct, err := openDirect(r.path, os.O_RDONLY, 0)
		if err != nil {
			return 0, err
		}
		r.f = f
		r.direct = direct
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
// When using direct I/O, the final file size is set via ftruncate
// in Close since the last block write may be padded to sector alignment.
type LocalWriter struct {
	path     string
	fileSize int64    // expected final size; set by pipeline via SetSize
	f        *os.File // nil on the template; set on clones
	direct   bool     // true if O_DIRECT / F_NOCACHE is active
}

// NewLocalWriter creates a BlockWriter template for a local file.
func NewLocalWriter(path string) *LocalWriter {
	return &LocalWriter{path: path}
}

// SetSize records the expected final file size. Called before the
// pipeline starts so Close can ftruncate after direct I/O writes.
func (w *LocalWriter) SetSize(size int64) { w.fileSize = size }

// CloneWriter opens a new file descriptor for a worker, attempting
// direct I/O to bypass the page cache.
func (w *LocalWriter) CloneWriter() (client.BlockWriter, error) {
	f, direct, err := openDirect(w.path, os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &LocalWriter{
		path:     w.path,
		fileSize: w.fileSize,
		f:        f,
		direct:   direct,
	}, nil
}

// sectorAlign rounds size up to the nearest 4096-byte boundary.
func sectorAlign(size int64) int64 {
	const align = 4096
	return (size + align - 1) &^ (align - 1)
}

// WriteBlock writes data at the correct offset using pwrite. If no
// file descriptor is open (template instance), one is opened lazily.
// For direct I/O, the last block is padded to sector alignment.
func (w *LocalWriter) WriteBlock(_ context.Context, index int, data []byte) error {
	if w.f == nil {
		f, direct, err := openDirect(w.path, os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		w.f = f
		w.direct = direct
	}
	offset := int64(index) * drive.BlockSize

	// Direct I/O requires writes to be sector-aligned. Full blocks
	// (4MB) are always aligned. The last block may be short — pad it.
	if w.direct && int64(len(data))%4096 != 0 {
		aligned := sectorAlign(int64(len(data)))
		padded := alignedAlloc(int(aligned))
		copy(padded, data)
		_, err := w.f.WriteAt(padded, offset)
		return err
	}

	_, err := w.f.WriteAt(data, offset)
	return err
}

// Describe returns the file path.
func (w *LocalWriter) Describe() string { return w.path }

// Close truncates the file to the expected size (removing any padding
// from the last direct I/O write) and closes the file descriptor.
func (w *LocalWriter) Close() error {
	if w.f == nil {
		return nil
	}
	// If direct I/O was used and we know the file size, truncate to
	// remove any sector-alignment padding from the last block.
	if w.direct && w.fileSize > 0 {
		if err := w.f.Truncate(w.fileSize); err != nil {
			_ = w.f.Close()
			return err
		}
	}
	return w.f.Close()
}

// blockMap tracks block assignment for a single CopyJob. Workers claim
// blocks sequentially via an advancing counter — no bitmap needed since
// blocks are never released or reordered.
type blockMap struct {
	job   *CopyJob
	total int
	next  int // next block to claim; caller holds pipeline mutex
}

// newBlockMap creates a blockMap for a CopyJob.
func newBlockMap(job *CopyJob) *blockMap {
	return &blockMap{job: job, total: job.Src.BlockCount()}
}

// claim returns the index of the next unclaimed block, or -1 if all
// blocks have been claimed. Caller must hold the pipeline mutex.
func (m *blockMap) claim() int {
	if m.next >= m.total {
		return -1
	}
	idx := m.next
	m.next++
	return idx
}
