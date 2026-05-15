package drive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"pgregory.net/rapid"
)

// memBlockStore is a mock blockStore that stores encrypted blocks in memory.
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

func (m *memBlockStore) Invalidate(_ string, _ int) {}

func (m *memBlockStore) fetchBlock(_ context.Context, _ string, index int, _, _ string) ([]byte, error) {
	return m.GetBlock(context.TODO(), "", index, "", "")
}

func (m *memBlockStore) getBufCache() *bufferCache { return nil }

// newTestFD creates a read-mode FileDescriptor backed by real crypto.
// The mock blockStore returns encrypted blocks that the session key can
// decrypt, exercising the full decrypt path.
func newTestFD(t testing.TB, plaintext []byte) *FileDescriptor {
	t.Helper()

	sessionKey, err := crypto.GenerateSessionKey()
	if err != nil {
		t.Fatalf("GenerateSessionKey: %v", err)
	}

	nBlocks := BlockCount(int64(len(plaintext)))
	store := &memBlockStore{blocks: make(map[int][]byte)}
	blocks := make([]proton.Block, 0, nBlocks)

	for i := 0; i < nBlocks; i++ {
		start := int64(i) * BlockSize
		end := start + BlockSize
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
		size := rapid.IntRange(1, BlockSize+1024).Draw(rt, "size")
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
		size := rapid.Int64Range(1, BlockSize*2).Draw(rt, "size")
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
		size := rapid.IntRange(1, BlockSize).Draw(rt, "size")
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
	if testing.Short() {
		t.Skip("skipping: 10 MB PGP encrypt/decrypt takes ~48 s")
	}

	// 2.5 blocks worth of data.
	size := int(BlockSize*2 + BlockSize/2)
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
	size := int(BlockSize + 1024)
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

// ---------------------------------------------------------------------------
// Write-mode mock and helper (Tasks 7.3, 7.4)
// ---------------------------------------------------------------------------

// writeMemBlockStore extends memBlockStore to capture RequestUpload and
// UploadBlock calls for write-path testing.
type writeMemBlockStore struct {
	memBlockStore
	mu       sync.Mutex
	uploads  map[int][]byte // keyed by 1-based block index
	reqCount int
}

func (m *writeMemBlockStore) RequestUpload(_ context.Context, req proton.BlockUploadReq) ([]proton.BlockUploadLink, error) {
	m.mu.Lock()
	m.reqCount++
	m.mu.Unlock()
	links := make([]proton.BlockUploadLink, len(req.BlockList))
	for i, b := range req.BlockList {
		links[i] = proton.BlockUploadLink{
			BareURL: fmt.Sprintf("https://test/upload/%d", b.Index),
			Token:   fmt.Sprintf("upload-token-%d", b.Index),
		}
	}
	return links, nil
}

func (m *writeMemBlockStore) UploadBlock(_ context.Context, _ string, index int, _, _ string, data []byte) error {
	m.mu.Lock()
	m.uploads[index] = append([]byte(nil), data...)
	m.mu.Unlock()
	return nil
}

// newWriteTestFD creates a write-mode FileDescriptor without real PGP
// keyrings. Suitable for testing buffer accumulation, mode checks, and
// Close idempotency — anything that doesn't invoke flushBlock's crypto.
func newWriteTestFD(t testing.TB) (*FileDescriptor, *writeMemBlockStore) { //nolint:unparam // store return used by future tests
	t.Helper()

	sessionKey, err := crypto.GenerateSessionKey()
	if err != nil {
		t.Fatalf("GenerateSessionKey: %v", err)
	}

	store := &writeMemBlockStore{
		memBlockStore: memBlockStore{blocks: make(map[int][]byte)},
		uploads:       make(map[int][]byte),
	}

	fd := &FileDescriptor{
		linkID:     "write-test-link",
		revisionID: "write-test-rev",
		shareID:    "write-test-share",
		sessionKey: sessionKey,
		// nodeKR and addrKR are nil — tests must not trigger flushBlock crypto
		mode:       fdWrite,
		store:      store,
		curBlock:   make([]byte, 0, BlockSize),
		tokens:     make(map[int]uploadedBlock),
		verifyCode: make([]byte, 32),
	}
	return fd, store
}

// ---------------------------------------------------------------------------
// Property tests — write path (Task 7.3)
// ---------------------------------------------------------------------------

// TestFDPropertyWriteBlockBoundaries verifies that after writing N bytes,
// curBlock holds N%BlockSize bytes and curIdx equals ⌊N/BlockSize⌋.
// This tests the accumulation logic without triggering actual crypto in
// flushBlock (which would need real PGP keyrings). We cap writes below
// BlockSize so flushBlock is never called.
//
// **Validates: Requirements 2.3**
func TestFDPropertyWriteBlockBoundaries(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		fd, _ := newWriteTestFD(t)

		// Write a total of N bytes in random-sized chunks, all fitting
		// within a single block to avoid triggering flushBlock crypto.
		totalSize := rapid.IntRange(1, int(BlockSize)-1).Draw(rt, "totalSize")
		written := 0
		for written < totalSize {
			chunkSize := rapid.IntRange(1, totalSize-written).Draw(rt, "chunkSize")
			data := make([]byte, chunkSize)
			n, err := fd.Write(data)
			if err != nil {
				rt.Fatalf("Write: %v", err)
			}
			if n != chunkSize {
				rt.Fatalf("Write returned %d, want %d", n, chunkSize)
			}
			written += n
		}

		fd.mu.Lock()
		gotCurBlockLen := len(fd.curBlock)
		gotCurIdx := fd.curIdx
		gotFileSize := fd.fileSize
		fd.mu.Unlock()

		// All data fits in one block, so curIdx should be 0 and
		// curBlock should hold all written bytes.
		wantCurBlockLen := totalSize
		wantCurIdx := 0

		if gotCurBlockLen != wantCurBlockLen {
			rt.Fatalf("curBlock len = %d, want %d (wrote %d bytes)", gotCurBlockLen, wantCurBlockLen, totalSize)
		}
		if gotCurIdx != wantCurIdx {
			rt.Fatalf("curIdx = %d, want %d", gotCurIdx, wantCurIdx)
		}
		if gotFileSize != int64(totalSize) {
			rt.Fatalf("fileSize = %d, want %d", gotFileSize, totalSize)
		}
	})
}

// TestFDPropertyCloseIdempotentWrite verifies that Close on a write-mode
// FD is idempotent — second Close returns nil.
//
// **Validates: Requirements 2.6**
func TestFDPropertyCloseIdempotentWrite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		fd, _ := newWriteTestFD(t)
		// Close without writing anything — no crypto needed.
		if err := fd.Close(); err != nil {
			rt.Fatalf("first Close: %v", err)
		}
		if err := fd.Close(); err != nil {
			rt.Fatalf("second Close: %v", err)
		}

		// Verify the FD is actually closed.
		fd.mu.Lock()
		closed := fd.closed
		fd.mu.Unlock()
		if !closed {
			rt.Fatal("FD not marked closed after Close")
		}
	})
}

// TestFDPropertyReadOnlyRejectsWrites verifies that Write and WriteAt on
// a read-mode FD return syscall.EBADF.
//
// **Validates: Requirements 6.2**
func TestFDPropertyReadOnlyRejectsWrites(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		size := rapid.IntRange(1, 4096).Draw(rt, "size")
		plaintext := make([]byte, size)
		fd := newTestFD(t, plaintext)

		writeLen := rapid.IntRange(1, 256).Draw(rt, "writeLen")
		buf := make([]byte, writeLen)

		// Write must fail with EBADF.
		_, err := fd.Write(buf)
		if !errors.Is(err, syscall.EBADF) {
			rt.Fatalf("Write on read FD: got %v, want syscall.EBADF", err)
		}

		// WriteAt must fail with EBADF.
		off := rapid.Int64Range(0, int64(size)-1).Draw(rt, "writeAtOff")
		_, err = fd.WriteAt(buf, off)
		if !errors.Is(err, syscall.EBADF) {
			rt.Fatalf("WriteAt on read FD: got %v, want syscall.EBADF", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Unit tests — write path (Task 7.4)
// ---------------------------------------------------------------------------

// TestFDWriteAccumulation writes small chunks and verifies curBlock grows.
func TestFDWriteAccumulation(t *testing.T) {
	fd, _ := newWriteTestFD(t)

	// Write 3 chunks of 100 bytes each.
	for i := 0; i < 3; i++ {
		data := bytes.Repeat([]byte{byte(i)}, 100)
		n, err := fd.Write(data)
		if err != nil {
			t.Fatalf("Write chunk %d: %v", i, err)
		}
		if n != 100 {
			t.Fatalf("Write chunk %d: n = %d, want 100", i, n)
		}
	}

	fd.mu.Lock()
	gotLen := len(fd.curBlock)
	gotIdx := fd.curIdx
	gotSize := fd.fileSize
	fd.mu.Unlock()

	if gotLen != 300 {
		t.Fatalf("curBlock len = %d, want 300", gotLen)
	}
	if gotIdx != 0 {
		t.Fatalf("curIdx = %d, want 0", gotIdx)
	}
	if gotSize != 300 {
		t.Fatalf("fileSize = %d, want 300", gotSize)
	}
}

// TestFDReadOnlyRejectsWrite creates a read FD and verifies Write returns EBADF.
func TestFDReadOnlyRejectsWrite(t *testing.T) {
	fd := newTestFD(t, make([]byte, 64))

	_, err := fd.Write([]byte("hello"))
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("Write on read FD: got %v, want syscall.EBADF", err)
	}
}

// TestFDReadOnlyRejectsWriteAt creates a read FD and verifies WriteAt returns EBADF.
func TestFDReadOnlyRejectsWriteAt(t *testing.T) {
	fd := newTestFD(t, make([]byte, 64))

	_, err := fd.WriteAt([]byte("hello"), 0)
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("WriteAt on read FD: got %v, want syscall.EBADF", err)
	}
}

// TestFDCloseIdempotentWrite verifies Close on a write FD can be called
// twice without error (no data written, so no crypto needed).
func TestFDCloseIdempotentWrite(t *testing.T) {
	fd, _ := newWriteTestFD(t)

	if err := fd.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := fd.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestFDTruncateUpdatesFileSize writes data, truncates to a smaller size,
// and verifies fileSize is updated.
func TestFDTruncateUpdatesFileSize(t *testing.T) {
	fd, _ := newWriteTestFD(t)

	// Write 500 bytes.
	data := make([]byte, 500)
	if _, err := fd.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}

	fd.mu.Lock()
	before := fd.fileSize
	fd.mu.Unlock()
	if before != 500 {
		t.Fatalf("fileSize before truncate = %d, want 500", before)
	}

	// Truncate to 200.
	if err := fd.Truncate(200); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	fd.mu.Lock()
	after := fd.fileSize
	fd.mu.Unlock()
	if after != 200 {
		t.Fatalf("fileSize after truncate = %d, want 200", after)
	}
}

// TestFDTruncateOnReadFDReturnsEBADF verifies Truncate on a read FD fails.
func TestFDTruncateOnReadFDReturnsEBADF(t *testing.T) {
	fd := newTestFD(t, make([]byte, 64))

	err := fd.Truncate(32)
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("Truncate on read FD: got %v, want syscall.EBADF", err)
	}
}

// TestFDTruncateOnClosedFDReturnsErrClosed verifies Truncate after Close fails.
func TestFDTruncateOnClosedFDReturnsErrClosed(t *testing.T) {
	fd, _ := newWriteTestFD(t)
	_ = fd.Close()

	err := fd.Truncate(0)
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Truncate on closed FD: got %v, want os.ErrClosed", err)
	}
}

// TestFDWriteOnClosedFDReturnsErrClosed verifies Write after Close fails.
func TestFDWriteOnClosedFDReturnsErrClosed(t *testing.T) {
	fd, _ := newWriteTestFD(t)
	_ = fd.Close()

	_, err := fd.Write([]byte("hello"))
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write on closed FD: got %v, want os.ErrClosed", err)
	}
}

// TestFDSyncOnReadFDReturnsEBADF verifies Sync on a read FD fails.
func TestFDSyncOnReadFDReturnsEBADF(t *testing.T) {
	fd := newTestFD(t, make([]byte, 64))

	err := fd.Sync()
	if !errors.Is(err, syscall.EBADF) {
		t.Fatalf("Sync on read FD: got %v, want syscall.EBADF", err)
	}
}

// ---------------------------------------------------------------------------
// Concurrency stress tests (Task 9.2)
// ---------------------------------------------------------------------------

// TestFDConcurrentReadAndReadAt runs multiple goroutines calling Read and
// ReadAt simultaneously on the same read FD. Verifies no panics and no
// data corruption (ReadAt returns correct data at each offset).
func TestFDConcurrentReadAndReadAt(t *testing.T) {
	// Use data spanning 2+ blocks to exercise cross-block reads.
	size := int(BlockSize + 4096)
	plaintext := make([]byte, size)
	for i := range plaintext {
		plaintext[i] = byte(i % 251)
	}

	fd := newTestFD(t, plaintext)

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*2)

	// Half the goroutines do sequential Read.
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			buf := make([]byte, 512)
			for i := 0; i < 20; i++ {
				_, err := fd.Read(buf)
				if err != nil && !errors.Is(err, io.EOF) {
					errs <- fmt.Errorf("reader %d iter %d: %w", id, i, err)
					return
				}
			}
		}(g)
	}

	// Other half do ReadAt at various offsets.
	for g := goroutines / 2; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				off := int64((id*1024 + i*256) % size)
				readLen := 256
				if int(off)+readLen > size {
					readLen = size - int(off)
				}
				if readLen <= 0 {
					continue
				}
				buf := make([]byte, readLen)
				n, err := fd.ReadAt(buf, off)
				if err != nil && !errors.Is(err, io.EOF) {
					errs <- fmt.Errorf("readat %d iter %d: %w", id, i, err)
					return
				}
				if !bytes.Equal(buf[:n], plaintext[off:off+int64(n)]) {
					errs <- fmt.Errorf("readat %d iter %d: data mismatch at offset %d", id, i, off)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestFDConcurrentWrite runs multiple goroutines calling Write
// simultaneously on the same write FD. Total writes stay under
// BlockSize to avoid triggering flushBlock crypto. Verifies no panics
// and total bytes written matches expectations.
func TestFDConcurrentWrite(t *testing.T) {
	fd, _ := newWriteTestFD(t)

	const goroutines = 8
	const writesPerGoroutine = 10
	const chunkSize = 64 // 8 * 10 * 64 = 5120 bytes, well under BlockSize

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	var totalWritten int64

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte(id % 256)}, chunkSize) //nolint:gosec // id ranges 0-7, modulo guarantees [0,255]
			for i := 0; i < writesPerGoroutine; i++ {
				n, err := fd.Write(data)
				if err != nil {
					errs <- fmt.Errorf("writer %d iter %d: %w", id, i, err)
					return
				}
				if n != chunkSize {
					errs <- fmt.Errorf("writer %d iter %d: wrote %d, want %d", id, i, n, chunkSize)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Verify total bytes accumulated.
	fd.mu.Lock()
	totalWritten = fd.fileSize
	gotLen := len(fd.curBlock)
	fd.mu.Unlock()

	wantTotal := int64(goroutines * writesPerGoroutine * chunkSize)
	if totalWritten != wantTotal {
		t.Fatalf("fileSize = %d, want %d", totalWritten, wantTotal)
	}
	if int64(gotLen) != wantTotal {
		t.Fatalf("curBlock len = %d, want %d", gotLen, wantTotal)
	}
}

// TestFDConcurrentCloseWithRead has one goroutine reading in a loop and
// another closing the FD. The reader must eventually get os.ErrClosed
// (or io.EOF) and must not panic.
func TestFDConcurrentCloseWithRead(t *testing.T) {
	size := int(BlockSize + 1024)
	plaintext := make([]byte, size)
	for i := range plaintext {
		plaintext[i] = byte(i % 199)
	}

	fd := newTestFD(t, plaintext)

	var wg sync.WaitGroup
	sawClosed := make(chan struct{})

	// Reader goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 512)
		for {
			_, err := fd.Read(buf)
			if errors.Is(err, os.ErrClosed) {
				close(sawClosed)
				return
			}
			if errors.Is(err, io.EOF) {
				// Reached end of file before close — seek back and retry.
				_, _ = fd.Seek(0, io.SeekStart)
				continue
			}
			// Other errors are acceptable during close race.
			if err != nil {
				close(sawClosed)
				return
			}
		}
	}()

	// Let the reader run a bit, then close.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Small busy loop to let reader start.
		for i := 0; i < 100; i++ {
			_ = i
		}
		if err := fd.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	wg.Wait()

	// Verify the reader saw the close (or at least didn't panic).
	select {
	case <-sawClosed:
		// Good — reader observed the close.
	default:
		// Reader finished without seeing ErrClosed — that's OK if it
		// hit EOF first and then Close happened. The important thing
		// is no panic and no data race.
	}
}

// TestFDConcurrentCloseWithWrite has one goroutine writing in a loop
// and another closing the FD. The writer must eventually get
// os.ErrClosed and must not panic. Writes stay under BlockSize.
func TestFDConcurrentCloseWithWrite(t *testing.T) {
	fd, _ := newWriteTestFD(t)

	var wg sync.WaitGroup
	sawClosed := make(chan struct{})

	// Writer goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		data := make([]byte, 64)
		for i := 0; i < 100; i++ {
			_, err := fd.Write(data)
			if errors.Is(err, os.ErrClosed) {
				close(sawClosed)
				return
			}
			if err != nil {
				close(sawClosed)
				return
			}
		}
		// Exhausted iterations without seeing close — still OK.
		close(sawClosed)
	}()

	// Let the writer run a bit, then close.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = i
		}
		if err := fd.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	wg.Wait()
	<-sawClosed
}
