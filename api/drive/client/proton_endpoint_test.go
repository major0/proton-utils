package client

import (
	"context"
	"fmt"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

// mockBlockStore implements BlockStore for testing ProtonReader/ProtonWriter
// without a real session.
type mockBlockStore struct {
	blocks map[string][]byte // "linkID:index" → data
}

func (m *mockBlockStore) GetBlock(_ context.Context, linkID string, index int, _, _ string) ([]byte, error) {
	key := fmt.Sprintf("%s:%d", linkID, index)
	data, ok := m.blocks[key]
	if !ok {
		return nil, fmt.Errorf("block not found: %s", key)
	}
	return data, nil
}

func (m *mockBlockStore) RequestUpload(_ context.Context, _ proton.BlockUploadReq) ([]proton.BlockUploadLink, error) {
	return nil, nil
}

func (m *mockBlockStore) UploadBlock(_ context.Context, _ string, _ int, _, _ string, _ []byte) error {
	return nil
}

// newMockStore creates a mockBlockStore pre-populated with block data.
func newMockStore(linkID string, blocks map[int][]byte) *mockBlockStore {
	m := &mockBlockStore{blocks: make(map[string][]byte)}
	for idx, data := range blocks {
		m.blocks[fmt.Sprintf("%s:%d", linkID, idx)] = data
	}
	return m
}

func TestProtonReader_BlockCount(t *testing.T) {
	tests := []struct {
		name       string
		blockSizes []int64
		fileSize   int64
		want       int
	}{
		{"from blockSizes", []int64{100, 200, 300}, 600, 3},
		{"from fileSize fallback", nil, drive.BlockSize*2 + 1, 3},
		{"single block", []int64{42}, 42, 1},
		{"empty blockSizes uses fileSize", nil, drive.BlockSize, 1},
		{"zero file", nil, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewProtonReader("link1", nil, nil, tt.fileSize, tt.blockSizes, nil)
			if got := r.BlockCount(); got != tt.want {
				t.Fatalf("BlockCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestProtonReader_BlockSize(t *testing.T) {
	tests := []struct {
		name       string
		blockSizes []int64
		fileSize   int64
		index      int
		want       int64
	}{
		{"from xattr sizes", []int64{100, 200, 300}, 600, 0, 100},
		{"from xattr sizes last", []int64{100, 200, 300}, 600, 2, 300},
		{"fallback full block", nil, drive.BlockSize * 3, 0, drive.BlockSize},
		{"fallback last partial", nil, drive.BlockSize + 100, 1, 100},
		{"fallback beyond end", nil, drive.BlockSize, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewProtonReader("link1", nil, nil, tt.fileSize, tt.blockSizes, nil)
			if got := r.BlockSize(tt.index); got != tt.want {
				t.Fatalf("BlockSize(%d) = %d, want %d", tt.index, got, tt.want)
			}
		})
	}
}

func TestProtonReader_TotalSize(t *testing.T) {
	tests := []struct {
		name     string
		fileSize int64
	}{
		{"zero", 0},
		{"small", 1024},
		{"large", drive.BlockSize * 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewProtonReader("link1", nil, nil, tt.fileSize, nil, nil)
			if got := r.TotalSize(); got != tt.fileSize {
				t.Fatalf("TotalSize() = %d, want %d", got, tt.fileSize)
			}
		})
	}
}

func TestProtonReader_Describe(t *testing.T) {
	tests := []struct {
		name   string
		linkID string
	}{
		{"simple", "abc123"},
		{"empty", ""},
		{"long", "a-very-long-link-id-string-here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewProtonReader(tt.linkID, nil, nil, 0, nil, nil)
			if got := r.Describe(); got != tt.linkID {
				t.Fatalf("Describe() = %q, want %q", got, tt.linkID)
			}
		})
	}
}

func TestProtonReader_ReadBlock(t *testing.T) {
	linkID := "test-link"
	blockData := []byte("encrypted-block-data-here")
	// ProtonReader uses 1-based index for store.GetBlock (index+1).
	store := newMockStore(linkID, map[int][]byte{1: blockData})
	blocks := []proton.Block{{BareURL: "https://example.com/block/0", Token: "tok0"}}

	r := NewProtonReader(linkID, blocks, nil, int64(len(blockData)), []int64{int64(len(blockData))}, store)

	buf := make([]byte, len(blockData))
	n, err := r.ReadBlock(context.Background(), 0, buf)
	if err != nil {
		t.Fatalf("ReadBlock: %v", err)
	}
	if n != len(blockData) {
		t.Fatalf("ReadBlock returned %d bytes, want %d", n, len(blockData))
	}
	for i := range blockData {
		if buf[i] != blockData[i] {
			t.Fatalf("mismatch at byte %d: got %d, want %d", i, buf[i], blockData[i])
		}
	}
}

func TestProtonReader_ReadBlock_OutOfRange(t *testing.T) {
	r := NewProtonReader("link1", []proton.Block{{BareURL: "u", Token: "t"}}, nil, 100, []int64{100}, nil)

	buf := make([]byte, 100)
	_, err := r.ReadBlock(context.Background(), 5, buf)
	if err == nil {
		t.Fatal("expected error for out-of-range block index")
	}
}

func TestProtonReader_Close(t *testing.T) {
	r := NewProtonReader("link1", nil, nil, 0, nil, nil)
	if err := r.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

func testFileHandle(linkID string) *FileHandle {
	return &FileHandle{
		LinkID:     linkID,
		RevisionID: "rev1",
		ShareID:    "share1",
		VolumeID:   "vol1",
		AddressID:  "addr1",
		SigAddr:    "test@example.com",
	}
}

func TestProtonWriter_Describe(t *testing.T) {
	tests := []struct {
		name   string
		linkID string
	}{
		{"simple", "write-link-1"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fh := testFileHandle(tt.linkID)
			w := NewProtonWriter(fh, nil, nil)
			if got := w.Describe(); got != tt.linkID {
				t.Fatalf("Describe() = %q, want %q", got, tt.linkID)
			}
		})
	}
}

func TestProtonWriter_Close_NoBlocks(t *testing.T) {
	fh := testFileHandle("link1")
	w := NewProtonWriter(fh, nil, nil)
	if err := w.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

// TestProtonReader_BlockSize_SumEqualsTotal_Property verifies that the sum
// of all block sizes equals TotalSize for any file size.
//
// **Validates: Requirements 2.4**
func TestProtonReader_BlockSize_SumEqualsTotal_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		fileSize := int64(rapid.IntRange(1, drive.BlockSize*10).Draw(t, "fileSize"))
		r := NewProtonReader("link1", nil, nil, fileSize, nil, nil)

		var sum int64
		for i := 0; i < r.BlockCount(); i++ {
			bs := r.BlockSize(i)
			if bs <= 0 || bs > drive.BlockSize {
				t.Fatalf("BlockSize(%d) = %d out of range for fileSize %d", i, bs, fileSize)
			}
			sum += bs
		}
		if sum != fileSize {
			t.Fatalf("sum of BlockSize = %d, want %d", sum, fileSize)
		}
	})
}
