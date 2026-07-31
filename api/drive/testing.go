package drive

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
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

// symlinkTestResolver is a minimal LinkResolver that supplies a single test
// address keyring so a POSIX-marked file link's revision XAttr decrypts in
// tests without a live session. It performs no network I/O.
type symlinkTestResolver struct {
	kr     *crypto.KeyRing
	addrID string
}

func (r *symlinkTestResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, nil
}

func (r *symlinkTestResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewLink(pLink, parent, parent.share, r)
}

func (r *symlinkTestResolver) GetLink(_ string) *Link { return nil }

func (r *symlinkTestResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{ID: r.addrID}, true
}

func (r *symlinkTestResolver) AddressKeyRing(id string) (*crypto.KeyRing, bool) {
	if id != r.addrID {
		return nil, false
	}
	return r.kr, true
}

func (r *symlinkTestResolver) Throttle() *api.Throttle                   { return nil }
func (r *symlinkTestResolver) MaxWorkers() int                           { return 1 }
func (r *symlinkTestResolver) FetchRevisionXAttr(context.Context, *Link) {}

// newTestPosixFileLink builds a resolvable file Link backed by real crypto for
// use in external test packages (e.g. internal/fusemount/drive). The revision
// XAttr POSIX section carries Symlink=symlink and Common.Size=size, encrypted
// with a freshly generated keyring that is pre-cached on the Link and served
// by the resolver — so IsSymlink(), Size(), Mode(), and the timestamp
// accessors resolve synchronously through the real decrypt path without a live
// session. The target content is not stored (reading it back requires the live
// OpenFD path); this covers the metadata surface the FUSE layer resolves at
// Lookup/Getattr.
func newTestPosixFileLink(name string, size int64, symlink bool) (*Link, error) {
	key, err := crypto.GenerateKey("test", "test@test.local", "rsa", 2048)
	if err != nil {
		return nil, fmt.Errorf("GenerateKey: %w", err)
	}
	kr, err := crypto.NewKeyRing(key)
	if err != nil {
		return nil, fmt.Errorf("NewKeyRing: %w", err)
	}

	x := proton.RevisionXAttr{
		Common: proton.RevisionXAttrCommon{
			ModificationTime: "2024-01-01T00:00:00+0000",
			Size:             size,
		},
	}
	setPosixXAttr(&x, PosixXAttr{Symlink: symlink})
	data, err := json.Marshal(x)
	if err != nil {
		return nil, fmt.Errorf("marshal xattr: %w", err)
	}
	enc, err := kr.Encrypt(crypto.NewPlainMessage(data), kr)
	if err != nil {
		return nil, fmt.Errorf("encrypt xattr: %w", err)
	}
	blob, err := enc.GetArmored()
	if err != nil {
		return nil, fmt.Errorf("armor xattr: %w", err)
	}

	resolver := &symlinkTestResolver{kr: kr, addrID: "addr-1"}

	// A share with metadata caching so resolved values may be reused, mirroring
	// the protonfs runtime (MemoryCacheLevel always CacheMetadata).
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root
	share.MemoryCacheLevel = api.CacheMetadata

	pLink := &proton.Link{
		LinkID:         "file-" + name,
		Type:           proton.LinkTypeFile,
		State:          proton.LinkStateActive,
		SignatureEmail: "test@test.local",
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:             "rev-" + name,
				State:          proton.RevisionStateActive,
				Size:           size,
				XAttr:          blob,
				SignatureEmail: "test@test.local",
			},
		},
	}
	l := NewTestLink(pLink, root, share, resolver, name)
	l.cachedKeyRing = kr
	return l, nil
}

// NewTestSymlinkLink builds a symlink-marked file Link whose IsSymlink()
// resolves true and whose Size() equals len(target), decryptable without a
// live session. For use in FUSE-layer tests that need a *Link the metadata
// accessors treat as a symlink (mode, size, detection). The verbatim target
// round-trip is covered separately at the api/drive layer over NewTestFD.
func NewTestSymlinkLink(name, target string) (*Link, error) {
	return newTestPosixFileLink(name, int64(len(target)), true)
}

// NewTestRegularFileLink builds a non-symlink file Link (IsSymlink() false)
// with Size() == size, decryptable without a live session. For use in
// FUSE-layer tests that need a plain-file *Link (e.g. readlink on a
// non-symlink returning EINVAL).
func NewTestRegularFileLink(name string, size int64) (*Link, error) {
	return newTestPosixFileLink(name, size, false)
}

// NewTestResolvedFileLink builds a plain (non-symlink) file Link whose
// metadata accessors return the given values directly, WITHOUT any crypto:
// the resolvedMeta cache is pre-populated and the XAttr fetch gate is marked
// done, so Mode()/Size()/ModifyTime()/CreateTime() resolve from the cache and
// IsSymlink() returns false (no keyring is available, so decryption is never
// attempted). This is the fast path for unit and property tests that need a
// *Link reporting a specific mode without paying for RSA key generation.
func NewTestResolvedFileLink(name string, size int64, mode uint32) *Link {
	pLink := &proton.Link{
		LinkID: "file-" + name,
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:    "rev-" + name,
				State: proton.RevisionStateActive,
				Size:  size,
			},
		},
	}
	l := NewTestLink(pLink, nil, nil, nil, name)
	l.meta = &resolvedMeta{size: size, mode: mode}
	l.fetchDone = true
	return l
}
