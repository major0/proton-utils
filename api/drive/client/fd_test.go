package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

// memBlockStore is a mock BlockStore that stores encrypted blocks in memory.
type memBlockStore struct {
	blocks map[int][]byte // keyed by 1-based index
}

func (m *memBlockStore) GetBlock(_ context.Context, _ string, index int, _, _ string) ([]byte, error) {
	data, ok := m.blocks[index]
	if !ok {
		return nil, fmt.Errorf("block %d not found", index)
	}
	return data, nil
}

func (m *memBlockStore) RequestUpload(_ context.Context, _ proton.BlockUploadReq) ([]proton.BlockUploadLink, error) {
	return nil, nil
}

func (m *memBlockStore) UploadBlock(_ context.Context, _ string, _ int, _, _ string, _ []byte) error {
	return nil
}

func (m *memBlockStore) Invalidate(_ string) {}

// newTestFD creates a read-mode FileDescriptor backed by real crypto.
// The mock BlockStore returns encrypted blocks that the session key can
// decrypt, exercising the full decrypt path.
func newTestFD(t testing.TB, plaintext []byte) *FileDescriptor {
	t.Helper()

	sessionKey, err := crypto.GenerateSessionKey()
	if err != nil {
		t.Fatalf("GenerateSessionKey: %v", err)
	}

	nBlocks := drive.BlockCount(int64(len(plaintext)))
	store := &memBlockStore{blocks: make(map[int][]byte)}
	blocks := make([]proton.Block, 0, nBlocks)

	for i := 0; i < nBlocks; i++ {
		start := int64(i) * drive.BlockSize
		end := start + drive.BlockSize
		if end > int64(len(plaintext)) {
			end = int64(len(plaintext))
		}
		chunk := plaintext[start:end]

		encrypted, encErr := sessionKey.Encrypt(crypto.NewPlainMessage(chunk))
		if encErr != nil {
			t.Fatalf("Encrypt block %d: %v", i, encErr)
		}
		store.blocks[i+1] = encrypted // 1-based index

		blocks = append(blocks, proton.Block{
			BareURL: fmt.Sprintf("https://test/block/%d", i),
			Token:   fmt.Sprintf("token-%d", i),
		})
	}

	return &FileDescriptor{
		linkID:     "test-link",
		sessionKey: sessionKey,
		blocks:     blocks,
		fileSize:   int64(len(plaintext)),
		mode:       fdRead,
		store:      store,
	}
}

// ---------------------------------------------------------------------------
// Property tests (Task 6.2)
// ---------------------------------------------------------------------------

// TestFDPropertyReadAtPreservesOffset verifies that ReadAt does not change
// the FD's offset field, regardless of the read position.
//
// **Validates: Requirements 1.2, 1.5**
func TestFDPropertyReadAtPreservesOffset(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		size := rapid.IntRange(1, drive.BlockSize+1024).Draw(rt, "size")
		plaintext := rapid.SliceOfN(rapid.Byte(), size, size).Draw(rt, "plaintext")

		fd := newTestFD(t, plaintext)

		// Set an initial offset.
		initOff := rapid.Int64Range(0, int64(len(plaintext))).Draw(rt, "initOffset")
		fd.mu.Lock()
		fd.offset = initOff
		fd.mu.Unlock()

		// ReadAt at a random position.
		readOff := rapid.Int64Range(0, int64(len(plaintext))-1).Draw(rt, "readOff")
		readLen := rapid.IntRange(1, len(plaintext)).Draw(rt, "readLen")
		buf := make([]byte, readLen)
		_, _ = fd.ReadAt(buf, readOff)

		// Offset must be unchanged.
		fd.mu.Lock()
		got := fd.offset
		fd.mu.Unlock()

		if got != initOff {
			rt.Fatalf("offset changed: was %d, now %d after ReadAt(%d)", initOff, got, readOff)
		}
	})
}

// TestFDPropertySeekContract verifies that Seek with all whence values
// produces the correct offset per io.Seeker semantics.
//
// **Validates: Requirements 1.6**
func TestFDPropertySeekContract(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		size := rapid.Int64Range(1, drive.BlockSize*2).Draw(rt, "size")
		// Use a minimal plaintext — Seek doesn't read data.
		plaintext := make([]byte, size)

		fd := newTestFD(t, plaintext)

		// Set a starting offset.
		startOff := rapid.Int64Range(0, size).Draw(rt, "startOff")
		fd.mu.Lock()
		fd.offset = startOff
		fd.mu.Unlock()

		whence := rapid.IntRange(0, 2).Draw(rt, "whence")

		var base int64
		switch whence {
		case io.SeekStart:
			base = 0
		case io.SeekCurrent:
			base = startOff
		case io.SeekEnd:
			base = size
		}

		// Generate an offset that keeps the result in [0, size].
		seekOff := rapid.Int64Range(-base, size-base).Draw(rt, "seekOff")
		expected := base + seekOff

		got, err := fd.Seek(seekOff, whence)
		if err != nil {
			rt.Fatalf("Seek(%d, %d) error: %v", seekOff, whence, err)
		}
		if got != expected {
			rt.Fatalf("Seek(%d, %d) = %d, want %d (startOff=%d, size=%d)",
				seekOff, whence, got, expected, startOff, size)
		}

		// Verify internal offset matches.
		fd.mu.Lock()
		internal := fd.offset
		fd.mu.Unlock()
		if internal != expected {
			rt.Fatalf("internal offset %d != returned %d", internal, expected)
		}
	})
}

// TestFDPropertyClosedRejectsOps verifies that after Close, Read, ReadAt,
// and Seek all return os.ErrClosed.
//
// **Validates: Requirements 1.7, 6.1**
func TestFDPropertyClosedRejectsOps(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		size := rapid.IntRange(1, drive.BlockSize).Draw(rt, "size")
		plaintext := rapid.SliceOfN(rapid.Byte(), size, size).Draw(rt, "plaintext")

		fd := newTestFD(t, plaintext)
		if err := fd.Close(); err != nil {
			rt.Fatalf("Close: %v", err)
		}

		buf := make([]byte, 16)

		// Read must fail.
		_, err := fd.Read(buf)
		if !errors.Is(err, os.ErrClosed) {
			rt.Fatalf("Read after Close: got %v, want os.ErrClosed", err)
		}

		// ReadAt must fail.
		off := rapid.Int64Range(0, int64(size)-1).Draw(rt, "readAtOff")
		_, err = fd.ReadAt(buf, off)
		if !errors.Is(err, os.ErrClosed) {
			rt.Fatalf("ReadAt after Close: got %v, want os.ErrClosed", err)
		}

		// Seek must fail.
		_, err = fd.Seek(0, io.SeekStart)
		if !errors.Is(err, os.ErrClosed) {
			rt.Fatalf("Seek after Close: got %v, want os.ErrClosed", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Unit tests (Task 6.3)
// ---------------------------------------------------------------------------

// TestFDSequentialReadAcrossBlocks creates a file spanning 2+ blocks, reads
// the entire thing in small chunks, and verifies the data matches.
func TestFDSequentialReadAcrossBlocks(t *testing.T) {
	// 2.5 blocks worth of data.
	size := int(drive.BlockSize*2 + drive.BlockSize/2)
	plaintext := make([]byte, size)
	for i := range plaintext {
		plaintext[i] = byte(i % 251) // deterministic pattern
	}

	fd := newTestFD(t, plaintext)

	var got []byte
	buf := make([]byte, 4096) // small read buffer
	for {
		n, err := fd.Read(buf)
		got = append(got, buf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}

	if !bytes.Equal(got, plaintext) {
		t.Fatalf("sequential read mismatch: got %d bytes, want %d", len(got), len(plaintext))
	}
}

// TestFDReadAtConcurrentSafety runs multiple goroutines calling ReadAt at
// different offsets simultaneously.
func TestFDReadAtConcurrentSafety(t *testing.T) {
	size := int(drive.BlockSize + 1024)
	plaintext := make([]byte, size)
	for i := range plaintext {
		plaintext[i] = byte(i % 199)
	}

	fd := newTestFD(t, plaintext)

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			off := int64(id) * 512
			if off >= int64(size) {
				off = 0
			}
			readLen := 256
			if int(off)+readLen > size {
				readLen = size - int(off)
			}
			buf := make([]byte, readLen)
			n, err := fd.ReadAt(buf, off)
			if err != nil && !errors.Is(err, io.EOF) {
				errs <- fmt.Errorf("goroutine %d: ReadAt(%d): %w", id, off, err)
				return
			}
			if !bytes.Equal(buf[:n], plaintext[off:off+int64(n)]) {
				errs <- fmt.Errorf("goroutine %d: data mismatch at offset %d", id, off)
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestFDSeekAllWhence tests Seek with SeekStart, SeekCurrent, and SeekEnd.
func TestFDSeekAllWhence(t *testing.T) {
	plaintext := make([]byte, 1024)
	fd := newTestFD(t, plaintext)

	// SeekStart to middle.
	got, err := fd.Seek(512, io.SeekStart)
	if err != nil {
		t.Fatalf("SeekStart: %v", err)
	}
	if got != 512 {
		t.Fatalf("SeekStart: got %d, want 512", got)
	}

	// SeekCurrent +100.
	got, err = fd.Seek(100, io.SeekCurrent)
	if err != nil {
		t.Fatalf("SeekCurrent: %v", err)
	}
	if got != 612 {
		t.Fatalf("SeekCurrent: got %d, want 612", got)
	}

	// SeekEnd -10.
	got, err = fd.Seek(-10, io.SeekEnd)
	if err != nil {
		t.Fatalf("SeekEnd: %v", err)
	}
	if got != 1014 {
		t.Fatalf("SeekEnd: got %d, want 1014", got)
	}

	// SeekStart to 0 (beginning).
	got, err = fd.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("SeekStart(0): %v", err)
	}
	if got != 0 {
		t.Fatalf("SeekStart(0): got %d, want 0", got)
	}

	// SeekEnd to end.
	got, err = fd.Seek(0, io.SeekEnd)
	if err != nil {
		t.Fatalf("SeekEnd(0): %v", err)
	}
	if got != 1024 {
		t.Fatalf("SeekEnd(0): got %d, want 1024", got)
	}

	// Out of bounds: negative result.
	_, err = fd.Seek(-1, io.SeekStart)
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Seek(-1, SeekStart): got %v, want os.ErrInvalid", err)
	}

	// Out of bounds: past end.
	_, err = fd.Seek(1, io.SeekEnd)
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Seek(1, SeekEnd): got %v, want os.ErrInvalid", err)
	}
}

// TestFDCloseIdempotent verifies that Close can be called twice without error.
func TestFDCloseIdempotent(t *testing.T) {
	plaintext := make([]byte, 64)
	fd := newTestFD(t, plaintext)

	if err := fd.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := fd.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
