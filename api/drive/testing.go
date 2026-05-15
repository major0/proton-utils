package drive

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
)

// NewTestLink creates a Link with a test name override for use in tests
// that need working Name() calls without real crypto infrastructure.
// The testName field causes Name() to return the given name directly,
// bypassing decryption.
func NewTestLink(pLink *proton.Link, parent *Link, share *Share, resolver LinkResolver, name string) *Link {
	l := NewLink(pLink, parent, share, resolver)
	l.testName = name
	return l
}

// NewTestClient creates a Client with a pre-populated link table for use
// in tests that need GetLink lookups without real API infrastructure.
// The links map is keyed by LinkID.
func NewTestClient(links map[string]*Link) *Client {
	table := make(map[string]*Link, len(links))
	for k, v := range links {
		table[k] = v
	}
	return &Client{
		linkTable: table,
	}
}

// testBlockStore is a simple in-memory blockStore for exported test helpers.
type testBlockStore struct {
	blocks map[int][]byte // keyed by 1-based index
}

func (s *testBlockStore) GetBlock(_ context.Context, _ string, index int, _, _ string) ([]byte, error) {
	data, ok := s.blocks[index]
	if !ok {
		return nil, fmt.Errorf("block %d not found", index)
	}
	return data, nil
}

func (s *testBlockStore) RequestUpload(_ context.Context, _ proton.BlockUploadReq) ([]proton.BlockUploadLink, error) {
	return nil, nil
}

func (s *testBlockStore) UploadBlock(_ context.Context, _ string, _ int, _, _ string, _ []byte) error {
	return nil
}

func (s *testBlockStore) Invalidate(_ string, _ int) {}

func (s *testBlockStore) fetchBlock(_ context.Context, _ string, index int, _, _ string) ([]byte, error) {
	return s.GetBlock(context.TODO(), "", index, "", "")
}

func (s *testBlockStore) getBufCache() *bufferCache { return nil }

// NewTestFD creates a read-mode FileDescriptor backed by real crypto for
// use in external test packages. The plaintext is split into blocks,
// encrypted with a generated session key, and stored in an in-memory
// block store. The returned FD supports ReadAt/Read/Seek/Close.
func NewTestFD(plaintext []byte) (*FileDescriptor, error) {
	sessionKey, err := crypto.GenerateSessionKey()
	if err != nil {
		return nil, fmt.Errorf("GenerateSessionKey: %w", err)
	}

	nBlocks := BlockCount(int64(len(plaintext)))
	store := &testBlockStore{blocks: make(map[int][]byte)}
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
			return nil, fmt.Errorf("encrypt block %d: %w", i, encErr)
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
	}, nil
}
