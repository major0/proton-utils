package drive

import (
	"context"
	"fmt"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// ProtonReader reads blocks from a Proton Drive file via the blockStore.
type ProtonReader struct {
	linkID     string
	blocks     []proton.Block
	sessionKey *crypto.SessionKey
	fileSize   int64
	blockSizes []int64
	store      blockStore
	nBlocks    int
}

// NewProtonReader creates a BlockReader for a Proton Drive file.
func NewProtonReader(linkID string, blocks []proton.Block, sessionKey *crypto.SessionKey, fileSize int64, blockSizes []int64, store blockStore) *ProtonReader {
	n := len(blocks)
	if n == 0 {
		n = len(blockSizes)
	}
	if n == 0 {
		n = BlockCount(fileSize)
	}
	return &ProtonReader{
		linkID:     linkID,
		blocks:     blocks,
		sessionKey: sessionKey,
		fileSize:   fileSize,
		blockSizes: blockSizes,
		store:      store,
		nBlocks:    n,
	}
}

// ReadBlock fetches block at index from the blockStore, decrypts it
// with the session key, and copies the plaintext into buf.
func (r *ProtonReader) ReadBlock(ctx context.Context, index int, buf []byte) (int, error) {
	if index >= len(r.blocks) {
		return 0, fmt.Errorf("block index %d out of range (have %d blocks)", index, len(r.blocks))
	}
	pb := r.blocks[index]

	encrypted, err := r.store.GetBlock(ctx, r.linkID, index+1, pb.BareURL, pb.Token)
	if err != nil {
		return 0, err
	}

	// Decrypt the data packet using the session key.
	plainMsg, err := r.sessionKey.Decrypt(encrypted)
	if err != nil {
		return 0, fmt.Errorf("decrypt block %d: %w", index, err)
	}
	n := copy(buf, plainMsg.GetBinary())
	return n, nil
}

// BlockCount returns the total number of blocks.
func (r *ProtonReader) BlockCount() int { return r.nBlocks }

// BlockSize returns the size of block at index.
func (r *ProtonReader) BlockSize(index int) int64 {
	if index < len(r.blockSizes) {
		return r.blockSizes[index]
	}
	offset := int64(index) * BlockSize
	remaining := r.fileSize - offset
	if remaining <= 0 {
		return 0
	}
	if remaining > BlockSize {
		return BlockSize
	}
	return remaining
}

// Describe returns the link ID.
func (r *ProtonReader) Describe() string { return r.linkID }

// TotalSize returns the file size.
func (r *ProtonReader) TotalSize() int64 { return r.fileSize }

// Close is a no-op.
func (r *ProtonReader) Close() error { return nil }

// ProtonWriter writes blocks to a Proton Drive file via the blockStore.
// Each WriteBlock call encrypts, hashes, requests an upload URL, and
// uploads the encrypted block. Close commits the revision with the
// manifest signature and XAttr.
type ProtonWriter struct {
	linkID     string
	revisionID string
	shareID    string
	volumeID   string
	addressID  string
	sigAddr    string
	sessionKey *crypto.SessionKey
	nodeKR     *crypto.KeyRing
	addrKR     *crypto.KeyRing
	store      blockStore
	session    *api.Session
	verifyCode []byte // raw verification code from CreateFile

	// Per-block results collected during WriteBlock, indexed by block
	// index (0-based). Protected by mu for concurrent pipeline workers.
	mu        sync.Mutex
	uploaded  map[int]uploadedBlock
	totalSize int64
	closed    bool // prevents double-commit
}

// uploadedBlock holds the result of a single block upload.
type uploadedBlock struct {
	token   string // upload token returned by RequestUpload
	encHash []byte // SHA-256 of encrypted block (for manifest)
	rawSize int64  // plaintext block size
}

// NewProtonWriter creates a BlockWriter for a Proton Drive file.
func NewProtonWriter(fh *FileHandle, store blockStore, session *api.Session) *ProtonWriter {
	return &ProtonWriter{
		linkID:     fh.LinkID,
		revisionID: fh.RevisionID,
		shareID:    fh.ShareID,
		volumeID:   fh.VolumeID,
		addressID:  fh.AddressID,
		sigAddr:    fh.SigAddr,
		sessionKey: fh.SessionKey,
		nodeKR:     fh.NodeKR,
		addrKR:     fh.AddrKR,
		store:      store,
		session:    session,
		verifyCode: fh.VerificationCode,
		uploaded:   make(map[int]uploadedBlock),
	}
}

// computeVerificationToken XORs the verification code with the first
// bytes of the encrypted block data.
func computeVerificationToken(verifyCode, encData []byte) []byte {
	token := make([]byte, len(verifyCode))
	for i := range verifyCode {
		if i < len(encData) {
			token[i] = verifyCode[i] ^ encData[i]
		} else {
			token[i] = verifyCode[i]
		}
	}
	return token
}

// uploadParams returns an uploadParams populated from the writer's
// crypto and identity fields.
func (w *ProtonWriter) uploadParams() uploadParams {
	return uploadParams{
		sessionKey: w.sessionKey,
		addrKR:     w.addrKR,
		nodeKR:     w.nodeKR,
		verifyCode: w.verifyCode,
		addressID:  w.addressID,
		volumeID:   w.volumeID,
		shareID:    w.shareID,
		linkID:     w.linkID,
		revisionID: w.revisionID,
		sigAddr:    w.sigAddr,
	}
}

// WriteBlock encrypts a plaintext block, requests an upload URL, and
// uploads the encrypted data. Called concurrently by pipeline workers.
// Block index is 0-based from the pipeline; the Proton API uses 1-based.
func (w *ProtonWriter) WriteBlock(ctx context.Context, index int, data []byte) error {
	ub, err := encryptAndUploadBlock(ctx, w.uploadParams(), w.store, index+1, data)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.uploaded[index] = ub
	w.totalSize += ub.rawSize
	w.mu.Unlock()
	return nil
}

// Describe returns the link ID.
func (w *ProtonWriter) Describe() string { return w.linkID }

// Close commits the revision by signing the manifest and calling
// UpdateRevision with block tokens, XAttr, and manifest signature.
func (w *ProtonWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	// Use context.Background() to ensure commit completes even after
	// pipeline context cancellation.
	return commitRevisionFromTokens(context.Background(), w.session, w.uploadParams(), w.uploaded)
}
