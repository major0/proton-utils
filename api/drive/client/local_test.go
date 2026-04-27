package client

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

// --- ReadBlock pread semantics ---

func TestLocalReader_ReadBlock_CorrectOffset(t *testing.T) {
	dir := t.TempDir()
	// Create a file with 2 full blocks + partial tail
	size := int64(drive.BlockSize*2 + 1000)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	path := filepath.Join(dir, "pread.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	r := NewLocalReader(path, size)
	defer func() { _ = r.Close() }()

	ctx := context.Background()

	// Block 0: full block at offset 0
	buf0 := make([]byte, drive.BlockSize)
	n0, err := r.ReadBlock(ctx, 0, buf0)
	if err != nil {
		t.Fatalf("ReadBlock(0): %v", err)
	}
	if n0 != drive.BlockSize {
		t.Fatalf("ReadBlock(0) n = %d, want %d", n0, drive.BlockSize)
	}
	if !bytes.Equal(buf0[:n0], data[:drive.BlockSize]) {
		t.Fatal("ReadBlock(0) data mismatch")
	}

	// Block 1: full block at offset BlockSize
	buf1 := make([]byte, drive.BlockSize)
	n1, err := r.ReadBlock(ctx, 1, buf1)
	if err != nil {
		t.Fatalf("ReadBlock(1): %v", err)
	}
	if n1 != drive.BlockSize {
		t.Fatalf("ReadBlock(1) n = %d, want %d", n1, drive.BlockSize)
	}
	if !bytes.Equal(buf1[:n1], data[drive.BlockSize:2*drive.BlockSize]) {
		t.Fatal("ReadBlock(1) data mismatch")
	}

	// Block 2: partial tail (1000 bytes)
	buf2 := make([]byte, 1000)
	n2, err := r.ReadBlock(ctx, 2, buf2)
	if err != nil {
		t.Fatalf("ReadBlock(2): %v", err)
	}
	if n2 != 1000 {
		t.Fatalf("ReadBlock(2) n = %d, want 1000", n2)
	}
	if !bytes.Equal(buf2[:n2], data[2*drive.BlockSize:]) {
		t.Fatal("ReadBlock(2) data mismatch")
	}
}

// --- BlockSize boundary calculation ---

func TestLocalReader_BlockSize_FullAndPartial(t *testing.T) {
	tests := []struct {
		name  string
		size  int64
		index int
		want  int64
	}{
		{"full block 0", int64(drive.BlockSize * 3), 0, drive.BlockSize},
		{"full block 1", int64(drive.BlockSize * 3), 1, drive.BlockSize},
		{"full block 2", int64(drive.BlockSize * 3), 2, drive.BlockSize},
		{"partial tail", int64(drive.BlockSize + 500), 1, 500},
		{"single byte file", 1, 0, 1},
		{"exactly one block", drive.BlockSize, 0, drive.BlockSize},
		{"beyond end", 100, 1, 0},
		{"zero size", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewLocalReader("/dev/null", tt.size)
			defer func() { _ = r.Close() }()
			if got := r.BlockSize(tt.index); got != tt.want {
				t.Errorf("BlockSize(%d) = %d, want %d (size=%d)", tt.index, got, tt.want, tt.size)
			}
		})
	}
}

// --- BlockCount for various file sizes ---

func TestLocalReader_BlockCount_Table(t *testing.T) {
	tests := []struct {
		name string
		size int64
		want int
	}{
		{"zero", 0, 0},
		{"one byte", 1, 1},
		{"exactly BlockSize", drive.BlockSize, 1},
		{"BlockSize+1", drive.BlockSize + 1, 2},
		{"two full blocks", drive.BlockSize * 2, 2},
		{"two blocks + 1", drive.BlockSize*2 + 1, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewLocalReader("/dev/null", tt.size)
			defer func() { _ = r.Close() }()
			if got := r.BlockCount(); got != tt.want {
				t.Errorf("BlockCount() = %d, want %d (size=%d)", got, tt.want, tt.size)
			}
		})
	}
}

// --- Property: BlockCount/BlockSize consistency ---

func TestLocalReader_BlockCountBlockSize_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		size := int64(rapid.IntRange(0, drive.BlockSize*20).Draw(t, "size"))
		r := NewLocalReader("/dev/null", size)
		defer func() { _ = r.Close() }()

		// BlockCount must match drive.BlockCount
		wantBlocks := drive.BlockCount(size)
		if r.BlockCount() != wantBlocks {
			t.Fatalf("BlockCount() = %d, want %d for size %d", r.BlockCount(), wantBlocks, size)
		}

		// Sum of BlockSize(i) must equal file size
		var total int64
		for i := 0; i < r.BlockCount(); i++ {
			bs := r.BlockSize(i)
			if bs <= 0 || bs > drive.BlockSize {
				t.Fatalf("BlockSize(%d) = %d out of range for size %d", i, bs, size)
			}
			total += bs
		}
		if total != size {
			t.Fatalf("sum(BlockSize) = %d, want %d", total, size)
		}

		// BlockSize beyond last block must be 0
		if r.BlockCount() > 0 {
			if r.BlockSize(r.BlockCount()) != 0 {
				t.Fatalf("BlockSize(%d) = %d, want 0 (beyond end)", r.BlockCount(), r.BlockSize(r.BlockCount()))
			}
		}
	})
}

// --- CloneReader returns independent instance ---

func TestLocalReader_CloneReader_Independent(t *testing.T) {
	dir := t.TempDir()
	data := []byte("hello world, this is clone test data!")
	path := filepath.Join(dir, "clone.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	r := NewLocalReader(path, int64(len(data)))
	defer func() { _ = r.Close() }()

	// Template has nil fd
	if r.f != nil {
		t.Fatal("template should have nil fd")
	}

	clone, err := r.CloneReader()
	if err != nil {
		t.Fatalf("CloneReader: %v", err)
	}
	defer func() { _ = clone.Close() }()

	// Clone has its own fd
	lr := clone.(*LocalReader)
	if lr.f == nil {
		t.Fatal("clone should have non-nil fd")
	}

	// Template fd still nil after clone
	if r.f != nil {
		t.Fatal("template fd should still be nil after CloneReader")
	}

	// Clone reads independently
	ctx := context.Background()
	buf := make([]byte, len(data))
	n, err := clone.ReadBlock(ctx, 0, buf)
	if err != nil {
		t.Fatalf("clone ReadBlock: %v", err)
	}
	if !bytes.Equal(buf[:n], data) {
		t.Fatalf("clone read %q, want %q", buf[:n], data)
	}
}

// --- CloneWriter returns independent instance ---

func TestLocalWriter_CloneWriter_Independent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clone_w.bin")
	// Pre-create file for writing
	if err := os.WriteFile(path, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}

	w := NewLocalWriter(path)
	defer func() { _ = w.Close() }()

	// Template has nil fd
	if w.f != nil {
		t.Fatal("template should have nil fd")
	}

	clone, err := w.CloneWriter()
	if err != nil {
		t.Fatalf("CloneWriter: %v", err)
	}
	defer func() { _ = clone.Close() }()

	// Clone has its own fd
	lw := clone.(*LocalWriter)
	if lw.f == nil {
		t.Fatal("clone should have non-nil fd")
	}

	// Template fd still nil after clone
	if w.f != nil {
		t.Fatal("template fd should still be nil after CloneWriter")
	}

	// Clone writes independently
	ctx := context.Background()
	payload := []byte("written by clone")
	if err := clone.WriteBlock(ctx, 0, payload); err != nil {
		t.Fatalf("clone WriteBlock: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, payload) {
		t.Fatalf("file content = %q, want prefix %q", got, payload)
	}
}

// --- Close semantics ---

func TestLocalReader_Close_NilFD(t *testing.T) {
	// Template (nil fd) — Close is a no-op
	r := NewLocalReader("/dev/null", 0)
	if r.f != nil {
		t.Fatal("template should have nil fd")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close on template: %v", err)
	}
}

func TestLocalReader_Close_CloneFD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close.bin")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	r := NewLocalReader(path, 1)
	clone, err := r.CloneReader()
	if err != nil {
		t.Fatal(err)
	}

	lr := clone.(*LocalReader)
	if lr.f == nil {
		t.Fatal("clone should have fd")
	}

	if err := clone.Close(); err != nil {
		t.Fatalf("Close on clone: %v", err)
	}

	// fd should be closed — reading from it should fail
	buf := make([]byte, 1)
	_, err = lr.f.Read(buf)
	if err == nil {
		t.Fatal("expected error reading from closed fd")
	}
}

func TestLocalWriter_Close_NilFD(t *testing.T) {
	w := NewLocalWriter("/dev/null")
	if w.f != nil {
		t.Fatal("template should have nil fd")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close on template: %v", err)
	}
}

func TestLocalWriter_Close_CloneFD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close_w.bin")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	w := NewLocalWriter(path)
	clone, err := w.CloneWriter()
	if err != nil {
		t.Fatal(err)
	}

	lw := clone.(*LocalWriter)
	if lw.f == nil {
		t.Fatal("clone should have fd")
	}

	if err := clone.Close(); err != nil {
		t.Fatalf("Close on clone: %v", err)
	}

	// fd should be closed — writing to it should fail
	_, err = lw.f.Write([]byte("x"))
	if err == nil {
		t.Fatal("expected error writing to closed fd")
	}
}

// --- Lazy open ---

func TestLocalReader_LazyOpen(t *testing.T) {
	dir := t.TempDir()
	data := []byte("lazy open reader test")
	path := filepath.Join(dir, "lazy.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	r := NewLocalReader(path, int64(len(data)))
	defer func() { _ = r.Close() }()

	// fd is nil before any read
	if r.f != nil {
		t.Fatal("fd should be nil before ReadBlock")
	}

	// ReadBlock on template opens fd lazily
	buf := make([]byte, len(data))
	n, err := r.ReadBlock(context.Background(), 0, buf)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}

	// fd is now open
	if r.f == nil {
		t.Fatal("fd should be non-nil after ReadBlock")
	}

	if !bytes.Equal(buf[:n], data) {
		t.Fatalf("got %q, want %q", buf[:n], data)
	}
}

func TestLocalWriter_LazyOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lazy_w.bin")
	if err := os.WriteFile(path, make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}

	w := NewLocalWriter(path)
	defer func() { _ = w.Close() }()

	// fd is nil before any write
	if w.f != nil {
		t.Fatal("fd should be nil before WriteBlock")
	}

	// WriteBlock on template opens fd lazily
	if err := w.WriteBlock(context.Background(), 0, []byte("lazy")); err != nil {
		t.Fatalf("WriteBlock: %v", err)
	}

	// fd is now open
	if w.f == nil {
		t.Fatal("fd should be non-nil after WriteBlock")
	}

	got, err := os.ReadFile(path) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, []byte("lazy")) {
		t.Fatalf("file content = %q, want prefix %q", got, "lazy")
	}
}
