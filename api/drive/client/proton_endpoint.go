package client

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
)

// ProtonReader reads blocks from a Proton Drive file via the BlockStore.
type ProtonReader struct {
	linkID     string
	blocks     []proton.Block
	sessionKey *crypto.SessionKey
	fileSize   int64
	blockSizes []int64
	store      BlockStore
	nBlocks    int
}

// NewProtonReader creates a BlockReader for a Proton Drive file.
func NewProtonReader(linkID string, blocks []proton.Block, sessionKey *crypto.SessionKey, fileSize int64, blockSizes []int64, store BlockStore) *ProtonReader {
	n := len(blocks)
	if n == 0 {
		n = len(blockSizes)
	}
	if n == 0 {
		n = drive.BlockCount(fileSize)
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

// ReadBlock fetches block at index from the BlockStore, decrypts it
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
	offset := int64(index) * drive.BlockSize
	remaining := r.fileSize - offset
	if remaining <= 0 {
		return 0
	}
	if remaining > drive.BlockSize {
		return drive.BlockSize
	}
	return remaining
}

// Describe returns the link ID.
func (r *ProtonReader) Describe() string { return r.linkID }

// TotalSize returns the file size.
func (r *ProtonReader) TotalSize() int64 { return r.fileSize }

// Close is a no-op.
func (r *ProtonReader) Close() error { return nil }

// ProtonWriter writes blocks to a Proton Drive file via the BlockStore.
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
	store      BlockStore
	session    *api.Session
	verifyCode []byte // raw verification code from CreateFile

	// Per-block results collected during WriteBlock, indexed by block
	// index (0-based). Protected by mu for concurrent pipeline workers.
	mu        sync.Mutex
	uploaded  map[int]uploadedBlock
	totalSize int64
}

// uploadedBlock holds the result of a single block upload.
type uploadedBlock struct {
	token   string // upload token returned by RequestUpload
	encHash []byte // SHA-256 of encrypted block (for manifest)
	rawSize int64  // plaintext block size
}

// NewProtonWriter creates a BlockWriter for a Proton Drive file.
func NewProtonWriter(fh *FileHandle, store BlockStore, session *api.Session) *ProtonWriter {
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

// WriteBlock encrypts a plaintext block, requests an upload URL, and
// uploads the encrypted data. Called concurrently by pipeline workers.
// Block index is 0-based from the pipeline; the Proton API uses 1-based.
func (w *ProtonWriter) WriteBlock(ctx context.Context, index int, data []byte) error {
	apiIndex := index + 1 // Proton API block indices are 1-based

	// Encrypt block with session key.
	plain := crypto.NewPlainMessage(data)
	encData, err := w.sessionKey.Encrypt(plain)
	if err != nil {
		return fmt.Errorf("encrypt block %d: %w", apiIndex, err)
	}

	// Encrypted signature of the plaintext block.
	encSig, err := w.addrKR.SignDetachedEncrypted(plain, w.nodeKR)
	if err != nil {
		return fmt.Errorf("sign block %d: %w", apiIndex, err)
	}
	encSigStr, err := encSig.GetArmored()
	if err != nil {
		return fmt.Errorf("armor block sig %d: %w", apiIndex, err)
	}

	// SHA-256 of encrypted block for manifest + upload request.
	h := sha256.New()
	h.Write(encData)
	hash := h.Sum(nil)

	// Compute verification token: XOR(verifyCode, encData[0:len(verifyCode)]).
	verifyToken := computeVerificationToken(w.verifyCode, encData)

	// Request upload URL for this single block.
	req := proton.BlockUploadReq{
		AddressID:  w.addressID,
		VolumeID:   w.volumeID,
		LinkID:     w.linkID,
		RevisionID: w.revisionID,
		BlockList: []proton.BlockUploadInfo{{
			Index:        apiIndex,
			EncSignature: encSigStr,
			Verifier:     &proton.BlockVerifier{Token: base64.StdEncoding.EncodeToString(verifyToken)},
		}},
		ThumbnailList: []interface{}{},
	}
	links, err := w.store.RequestUpload(ctx, req)
	if err != nil {
		return fmt.Errorf("request upload block %d: %w", apiIndex, err)
	}
	if len(links) == 0 {
		return fmt.Errorf("no upload link for block %d", apiIndex)
	}

	// Upload encrypted block.
	if err := w.store.UploadBlock(ctx, w.linkID, apiIndex, links[0].BareURL, links[0].Token, encData); err != nil {
		return fmt.Errorf("upload block %d: %w", apiIndex, err)
	}

	// Record result for Close.
	w.mu.Lock()
	w.uploaded[index] = uploadedBlock{
		token:   links[0].Token,
		encHash: hash,
		rawSize: int64(len(data)),
	}
	w.totalSize += int64(len(data))
	w.mu.Unlock()

	return nil
}

// Describe returns the link ID.
func (w *ProtonWriter) Describe() string { return w.linkID }

// Close commits the revision by signing the manifest and calling
// UpdateRevision with block tokens, XAttr, and manifest signature.
func (w *ProtonWriter) Close() error {
	w.mu.Lock()
	nBlocks := len(w.uploaded)
	totalSize := w.totalSize
	w.mu.Unlock()

	if nBlocks == 0 {
		return nil
	}

	// Build block token list and manifest hash (ordered by index).
	blockTokens := make([]proton.BlockToken, nBlocks)
	blockSizes := make([]int64, nBlocks)
	var manifestData []byte
	for i := 0; i < nBlocks; i++ {
		ub, ok := w.uploaded[i]
		if !ok {
			return fmt.Errorf("missing block %d in upload results", i)
		}
		blockTokens[i] = proton.BlockToken{
			Index: i + 1, // 1-based
			Token: ub.token,
		}
		blockSizes[i] = ub.rawSize
		manifestData = append(manifestData, ub.encHash...)
	}

	// Sign the manifest (concatenated SHA-256 hashes of encrypted blocks).
	manifestSig, err := w.addrKR.SignDetached(crypto.NewPlainMessage(manifestData))
	if err != nil {
		return fmt.Errorf("sign manifest: %w", err)
	}
	manifestSigStr, err := manifestSig.GetArmored()
	if err != nil {
		return fmt.Errorf("armor manifest sig: %w", err)
	}

	// Build XAttr with file metadata.
	xAttrCommon := &proton.RevisionXAttrCommon{
		ModificationTime: time.Now().UTC().Format("2006-01-02T15:04:05-0700"),
		Size:             totalSize,
		BlockSizes:       blockSizes,
	}

	req := proton.UpdateRevisionReq{
		BlockList:         blockTokens,
		ManifestSignature: manifestSigStr,
		SignatureAddress:  w.sigAddr,
	}
	if err := req.SetEncXAttrString(w.addrKR, w.nodeKR, xAttrCommon); err != nil {
		return fmt.Errorf("encrypt xattr: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := w.session.Client.UpdateRevision(ctx, w.shareID, w.linkID, w.revisionID, req); err != nil {
		return fmt.Errorf("commit revision: %w", err)
	}

	return nil
}
