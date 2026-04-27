package client

import "context"

// BlockReader reads blocks from a source. Implementations carry their
// own state (file path, link, session key, etc.).
type BlockReader interface {
	// ReadBlock reads block at index (0-based) into buf. Returns bytes read.
	ReadBlock(ctx context.Context, index int, buf []byte) (int, error)
	// BlockCount returns the total number of blocks.
	BlockCount() int
	// BlockSize returns the size of block at index (0-based).
	BlockSize(index int) int64
	// TotalSize returns the total file size in bytes.
	TotalSize() int64
	// Describe returns a human-readable name for error messages.
	Describe() string
	// Close releases resources.
	Close() error
}

// BlockWriter writes blocks to a destination. Implementations carry
// their own state (file path, link, session key, etc.).
type BlockWriter interface {
	// WriteBlock writes data as block at index (0-based).
	WriteBlock(ctx context.Context, index int, data []byte) error
	// Describe returns a human-readable name for error messages.
	Describe() string
	// Close releases resources.
	Close() error
}

// CloneableReader is implemented by BlockReaders that support per-worker
// cloning. Each clone opens its own file descriptor so workers avoid
// sharing kernel-level FD state.
type CloneableReader interface {
	CloneReader() (BlockReader, error)
}

// CloneableWriter is implemented by BlockWriters that support per-worker
// cloning.
type CloneableWriter interface {
	CloneWriter() (BlockWriter, error)
}

// CopyJob is a fully resolved source/destination pair.
type CopyJob struct {
	Src BlockReader
	Dst BlockWriter
}

// TransferOpts configures bulk transfer behavior.
type TransferOpts struct {
	Progress func(completed, total int, bytes int64, rate float64)
	Verbose  func(src, dst string)
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
