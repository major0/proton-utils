package drive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// fdMode distinguishes read-only from read-write file descriptors.
type fdMode int

const (
	fdRead fdMode = iota
	fdWrite
)

// FileDescriptor is an active handle to an open Proton Drive file.
// It tracks byte offset, owns crypto state for decrypt/encrypt, and
// delegates block I/O to a blockStore. Implements io.Reader,
// io.ReaderAt, io.Seeker, io.Writer, io.WriterAt, and io.Closer.
//
// Concurrency: fd.mu protects offset, closed, curBlock, curIdx, and
// fileSize. Read/ReadAt release the lock before calling store.GetBlock
// so block I/O never holds the FD mutex. Write/WriteAt hold the lock
// during buffer accumulation but flushBlock only copies data and spawns
// a goroutine — the actual encrypt+upload runs outside the lock.
// Close sets closed=true under lock, then flushes/waits outside it;
// concurrent ops see os.ErrClosed on their next lock acquisition.
type FileDescriptor struct {
	// Identity
	linkID     string
	revisionID string
	shareID    string

	// Crypto
	sessionKey *crypto.SessionKey
	nodeKR     *crypto.KeyRing
	addrKR     *crypto.KeyRing

	// Block metadata (for reads — block URLs/tokens from revision)
	blocks []proton.Block

	// State
	mu       sync.Mutex
	offset   int64
	fileSize int64
	closed   bool
	mode     fdMode

	// ctx is the context from OpenFD/CreateFD, used for block I/O
	// cancellation. Since io.Reader/io.Writer don't accept contexts,
	// this is the best available mechanism for cancellation.
	ctx context.Context

	// Write-side fields (only populated for fdWrite mode)
	curBlock []byte                // current block accumulator (up to BlockSize)
	curIdx   int                   // current block index being filled
	inflight sync.WaitGroup        // tracks in-flight upload goroutines
	tokens   map[int]uploadedBlock // collected after upload, keyed by block index
	tokensMu sync.Mutex            // protects tokens and firstErr
	firstErr error                 // first upload error

	// Upload metadata (write-only, from FileHandle)
	volumeID   string
	addressID  string
	sigAddr    string
	verifyCode []byte       // raw verification code for block tokens
	session    *api.Session // needed for UpdateRevision

	// Shared infrastructure
	store blockStore

	// Read strategy — selected at FD construction based on BlockCacheMode.
	// nil defaults to decryptedReadStrategy behavior (for test FDs not
	// constructed via OpenFD).
	reader readStrategy

	// Prefetch configuration
	prefetchBlocks int // number of blocks to prefetch ahead (0 = disabled)

	// Link reference for Stat
	link *Link
}

// Compile-time interface checks.
var (
	_ io.Reader   = (*FileDescriptor)(nil)
	_ io.ReaderAt = (*FileDescriptor)(nil)
	_ io.Seeker   = (*FileDescriptor)(nil)
	_ io.Writer   = (*FileDescriptor)(nil)
	_ io.WriterAt = (*FileDescriptor)(nil)
	_ io.Closer   = (*FileDescriptor)(nil)
)

// OpenFD opens a Proton Drive file for reading and returns a
// FileDescriptor. It wraps Client.OpenFile to fetch revision metadata
// and derive the session key, then constructs a read-mode FD backed
// by a blockStore.
func (c *Client) OpenFD(ctx context.Context, link *Link) (*FileDescriptor, error) {
	fh, err := c.OpenFile(ctx, link)
	if err != nil {
		return nil, fmt.Errorf("OpenFD: %w", err)
	}

	store := c.blockStore

	// Select read strategy based on BlockCacheMode. Default to encrypted
	// for any value other than "decrypted" (defensive).
	var strategy readStrategy
	if c.BlockCacheMode == "decrypted" {
		strategy = decryptedReadStrategy{}
	} else {
		strategy = encryptedReadStrategy{}
	}

	return &FileDescriptor{
		linkID:         fh.LinkID,
		revisionID:     fh.RevisionID,
		shareID:        fh.Share.ProtonShare().ShareID,
		sessionKey:     fh.SessionKey,
		blocks:         fh.Blocks,
		fileSize:       fh.FileSize,
		mode:           fdRead,
		ctx:            ctx,
		store:          store,
		reader:         strategy,
		prefetchBlocks: c.PrefetchBlocks,
		link:           link,
	}, nil
}

// decryptBlock decrypts an encrypted block using the FD's session key.
func (fd *FileDescriptor) decryptBlock(encrypted []byte) ([]byte, error) {
	msg, err := fd.sessionKey.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return msg.GetBinary(), nil
}

// Read implements io.Reader. It reads decrypted file data starting at
// the current offset, advancing the offset by the number of bytes read.
// Handles reads spanning multiple blocks.
func (fd *FileDescriptor) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	fd.mu.Lock()
	if fd.closed {
		fd.mu.Unlock()
		return 0, os.ErrClosed
	}
	offset := fd.offset
	fileSize := fd.fileSize
	fd.mu.Unlock()

	if offset >= fileSize {
		return 0, io.EOF
	}

	totalRead := 0
	for len(p) > 0 && offset < fileSize {
		blockIdx := int(offset / BlockSize)
		blockOffset := offset % BlockSize

		plain, err := fd.readBlock(blockIdx)
		if err != nil {
			if totalRead > 0 {
				break
			}
			return 0, fmt.Errorf("fd.Read block %d: %w", blockIdx, err)
		}

		// Copy from blockOffset into p.
		avail := int64(len(plain)) - blockOffset
		if avail <= 0 {
			break
		}
		n := copy(p, plain[blockOffset:])
		p = p[n:]
		offset += int64(n)
		totalRead += n
	}

	fd.mu.Lock()
	fd.offset = offset
	fd.mu.Unlock()

	if totalRead == 0 && offset >= fileSize {
		return 0, io.EOF
	}
	return totalRead, nil
}

// ReadAt implements io.ReaderAt. It reads decrypted file data at the
// given offset without modifying the FD's current offset. Handles
// reads spanning multiple blocks.
func (fd *FileDescriptor) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	fd.mu.Lock()
	if fd.closed {
		fd.mu.Unlock()
		return 0, os.ErrClosed
	}
	fileSize := fd.fileSize
	fd.mu.Unlock()

	if off >= fileSize {
		return 0, io.EOF
	}

	requested := len(p)
	totalRead := 0
	for len(p) > 0 && off < fileSize {
		blockIdx := int(off / BlockSize)
		if blockIdx >= len(fd.blocks) {
			break // no more blocks available
		}
		blockOffset := off % BlockSize

		plain, err := fd.readBlock(blockIdx)
		if err != nil {
			if totalRead > 0 {
				return totalRead, err
			}
			return 0, fmt.Errorf("fd.ReadAt block %d: %w", blockIdx, err)
		}

		avail := int64(len(plain)) - blockOffset
		if avail <= 0 {
			break
		}
		n := copy(p, plain[blockOffset:])
		p = p[n:]
		off += int64(n)
		totalRead += n
	}

	// io.ReaderAt contract: return io.EOF if fewer bytes than requested
	// were read because end of file was reached.
	if totalRead < requested {
		return totalRead, io.EOF
	}
	return totalRead, nil
}

// readBlock fetches and decrypts a single block by index. Delegates to
// the readStrategy selected at FD construction time. If no strategy is
// set (test FDs not constructed via OpenFD), defaults to decrypted-mode
// behavior for backward compatibility.
func (fd *FileDescriptor) readBlock(blockIdx int) ([]byte, error) {
	if blockIdx >= len(fd.blocks) {
		return nil, fmt.Errorf("block index %d out of range (have %d blocks)", blockIdx, len(fd.blocks))
	}

	r := fd.reader
	if r == nil {
		r = decryptedReadStrategy{}
	}
	return r.readBlock(fd, blockIdx)
}

// readStrategy abstracts the mode-dependent read path. Implementations
// are selected once at FD construction based on BlockCacheMode.
type readStrategy interface {
	// readBlock returns decrypted plaintext for the given block index.
	readBlock(fd *FileDescriptor, blockIdx int) ([]byte, error)
	// prefetch initiates background fetches for blocks ahead of blockIdx.
	prefetch(fd *FileDescriptor, blockIdx int)
}

// encryptedReadStrategy implements readStrategy for encrypted mode.
// The blockStore's GetBlock manages the buffer cache with ciphertext.
// The FD layer calls GetBlock and decrypts the result every time.
type encryptedReadStrategy struct{}

func (encryptedReadStrategy) readBlock(fd *FileDescriptor, blockIdx int) ([]byte, error) {
	encryptedReadStrategy{}.prefetch(fd, blockIdx)

	pb := fd.blocks[blockIdx]
	apiIdx := blockIdx + 1
	encrypted, err := fd.store.GetBlock(fd.ctx, fd.linkID, apiIdx, pb.BareURL, pb.Token)
	if err != nil {
		return nil, err
	}
	return fd.decryptBlock(encrypted)
}

func (encryptedReadStrategy) prefetch(fd *FileDescriptor, blockIdx int) {
	if fd.prefetchBlocks <= 0 {
		return
	}
	for i := 1; i <= fd.prefetchBlocks; i++ {
		idx := blockIdx + i
		if idx >= len(fd.blocks) {
			break
		}
		pb := fd.blocks[idx]
		apiIdx := idx + 1
		store := fd.store
		ctx := fd.ctx
		linkID := fd.linkID
		go func() {
			_, _ = store.GetBlock(ctx, linkID, apiIdx, pb.BareURL, pb.Token)
		}()
	}
}

// decryptedReadStrategy implements readStrategy for decrypted mode.
// The FD layer manages the buffer cache directly with plaintext. This
// wraps the existing getDecryptedBlock/prefetch methods on FileDescriptor.
type decryptedReadStrategy struct{}

func (decryptedReadStrategy) readBlock(fd *FileDescriptor, blockIdx int) ([]byte, error) {
	fd.prefetch(blockIdx)
	return fd.getDecryptedBlock(blockIdx)
}

func (decryptedReadStrategy) prefetch(fd *FileDescriptor, blockIdx int) {
	fd.prefetch(blockIdx)
}

// getDecryptedBlock returns decrypted plaintext for a block. Checks the
// buffer cache first (stores plaintext). On miss, reserves a slot,
// fetches encrypted data, decrypts, and populates the cache.
func (fd *FileDescriptor) getDecryptedBlock(blockIdx int) ([]byte, error) {
	apiIdx := blockIdx + 1
	store := fd.store

	// 1. Buffer cache check — stores decrypted plaintext.
	if bc := store.getBufCache(); bc != nil {
		if data, err := bc.Get(fd.linkID, apiIdx); data != nil || err != nil {
			return data, err
		}

		// 2. Reserve a slot for this block. If Reserve returns false,
		// another goroutine claimed it — re-Get to wait on their result.
		if !bc.Reserve(fd.linkID, apiIdx) {
			data, err := bc.Get(fd.linkID, apiIdx)
			if data != nil || err != nil {
				return data, err
			}
			// Slot was evicted/invalidated — fall through to fetch.
		} else {
			// We own the fetching slot. Fetch, decrypt, Put plaintext.
			plain, err := fd.fetchDecrypt(blockIdx)
			if err != nil {
				bc.PutError(fd.linkID, apiIdx, err)
				return nil, err
			}
			bc.Put(fd.linkID, apiIdx, plain)
			return plain, nil
		}
	}

	// No buffer cache or slot lost — fetch and decrypt directly.
	return fd.fetchDecrypt(blockIdx)
}

// fetchDecrypt fetches an encrypted block from disk/HTTP and decrypts it.
func (fd *FileDescriptor) fetchDecrypt(blockIdx int) ([]byte, error) {
	pb := fd.blocks[blockIdx]
	apiIdx := blockIdx + 1

	encrypted, err := fd.store.fetchBlock(fd.ctx, fd.linkID, apiIdx, pb.BareURL, pb.Token)
	if err != nil {
		return nil, err
	}

	return fd.decryptBlock(encrypted)
}

// prefetch initiates background fetches for blocks ahead of blockIdx.
// Each prefetch goroutine fetches the encrypted block, decrypts it, and
// stores the plaintext in the buffer cache. Uses Reserve for
// deduplication — only one goroutine fetches/decrypts a given block.
func (fd *FileDescriptor) prefetch(blockIdx int) {
	if fd.prefetchBlocks <= 0 {
		return
	}
	for i := 1; i <= fd.prefetchBlocks; i++ {
		idx := blockIdx + i
		if idx >= len(fd.blocks) {
			break
		}

		store := fd.store
		bc := store.getBufCache()
		if bc == nil {
			continue
		}

		apiIdx := idx + 1
		linkID := fd.linkID

		// Reserve before spawning goroutine — avoids goroutine overhead
		// when the block is already cached or being fetched.
		if !bc.Reserve(linkID, apiIdx) {
			continue
		}

		// We own the fetching slot. Spawn goroutine to fetch+decrypt.
		pb := fd.blocks[idx]
		ctx := fd.ctx
		sessionKey := fd.sessionKey

		go func() {
			encrypted, err := store.fetchBlock(ctx, linkID, apiIdx, pb.BareURL, pb.Token)
			if err != nil {
				bc.PutError(linkID, apiIdx, err)
				return
			}
			msg, err := sessionKey.Decrypt(encrypted)
			if err != nil {
				bc.PutError(linkID, apiIdx, err)
				return
			}
			bc.Put(linkID, apiIdx, msg.GetBinary())
		}()
	}
}

// Seek implements io.Seeker. It repositions the FD offset without
// performing any I/O. Prefetch resumes on the next Read.
func (fd *FileDescriptor) Seek(offset int64, whence int) (int64, error) {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	if fd.closed {
		return 0, os.ErrClosed
	}

	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = fd.offset + offset
	case io.SeekEnd:
		newOffset = fd.fileSize + offset
	default:
		return 0, os.ErrInvalid
	}

	if newOffset < 0 || newOffset > fd.fileSize {
		return 0, os.ErrInvalid
	}

	fd.offset = newOffset
	return newOffset, nil
}

// Close implements io.Closer. For read-mode FDs, it marks the FD as
// closed. For write-mode FDs, it flushes any pending data and commits
// the revision. Idempotent — second Close is a no-op.
func (fd *FileDescriptor) Close() error {
	fd.mu.Lock()
	if fd.closed {
		fd.mu.Unlock()
		return nil
	}
	fd.closed = true
	mode := fd.mode
	hasCurBlock := len(fd.curBlock) > 0
	fd.mu.Unlock()

	if mode == fdWrite && (hasCurBlock || fd.hasTokens()) {
		if hasCurBlock {
			fd.flushBlock(fd.curIdx, fd.curBlock)
		}
		fd.inflight.Wait()

		fd.tokensMu.Lock()
		err := fd.firstErr
		fd.tokensMu.Unlock()
		if err != nil {
			return err
		}

		return fd.commitRevision()
	}

	return nil
}

// Stat returns metadata from the underlying Link without API calls.
func (fd *FileDescriptor) Stat() FileInfo {
	return fd.link.Stat()
}

// hasTokens reports whether any uploaded block tokens have been collected.
func (fd *FileDescriptor) hasTokens() bool {
	fd.tokensMu.Lock()
	defer fd.tokensMu.Unlock()
	return len(fd.tokens) > 0
}

// newWriteFD constructs a write-mode FileDescriptor from a FileHandle.
func newWriteFD(ctx context.Context, fh *FileHandle, store blockStore, session *api.Session) *FileDescriptor {
	return &FileDescriptor{
		linkID:     fh.LinkID,
		revisionID: fh.RevisionID,
		shareID:    fh.ShareID,
		sessionKey: fh.SessionKey,
		nodeKR:     fh.NodeKR,
		addrKR:     fh.AddrKR,
		mode:       fdWrite,
		ctx:        ctx,
		store:      store,
		curBlock:   make([]byte, 0, BlockSize),
		tokens:     make(map[int]uploadedBlock),
		volumeID:   fh.VolumeID,
		addressID:  fh.AddressID,
		sigAddr:    fh.SigAddr,
		verifyCode: fh.VerificationCode,
		session:    session,
	}
}

// CreateFD creates a new file in Proton Drive and returns a write-mode
// FileDescriptor. It wraps Client.CreateFile to obtain the FileHandle.
func (c *Client) CreateFD(ctx context.Context, share *Share, parent *Link, name string) (*FileDescriptor, error) {
	fh, err := c.CreateFile(ctx, share, parent, name)
	if err != nil {
		return nil, fmt.Errorf("CreateFD: %w", err)
	}

	store := c.blockStore
	return newWriteFD(ctx, fh, store, c.Session), nil
}

// OverwriteFD creates a new revision on an existing file and returns a
// write-mode FileDescriptor. It wraps Client.OverwriteFile.
func (c *Client) OverwriteFD(ctx context.Context, share *Share, link *Link) (*FileDescriptor, error) {
	fh, err := c.OverwriteFile(ctx, share, link)
	if err != nil {
		return nil, fmt.Errorf("OverwriteFD: %w", err)
	}

	store := c.blockStore

	// Invalidate stale cached blocks from the previous revision before
	// uploading new blocks. Use ceiling division to cover partial tail.
	oldSize := link.Size()
	if oldSize > 0 {
		oldBlockCount := int((oldSize + BlockSize - 1) / BlockSize)
		store.Invalidate(link.LinkID(), oldBlockCount)
	}

	return newWriteFD(ctx, fh, store, c.Session), nil
}

// Write implements io.Writer. It buffers data into the current block
// and flushes full blocks for encrypt+upload. Returns syscall.EBADF
// if the FD is read-only.
func (fd *FileDescriptor) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	fd.mu.Lock()
	if fd.closed {
		fd.mu.Unlock()
		return 0, os.ErrClosed
	}
	if fd.mode != fdWrite {
		fd.mu.Unlock()
		return 0, syscall.EBADF
	}

	total := 0
	for len(p) > 0 {
		space := int(BlockSize) - len(fd.curBlock)
		chunk := len(p)
		if chunk > space {
			chunk = space
		}
		fd.curBlock = append(fd.curBlock, p[:chunk]...)
		p = p[chunk:]
		total += chunk
		fd.fileSize += int64(chunk)

		if len(fd.curBlock) == int(BlockSize) {
			fd.flushBlock(fd.curIdx, fd.curBlock)
			fd.curBlock = make([]byte, 0, BlockSize)
			fd.curIdx++
		}
	}
	fd.mu.Unlock()

	return total, nil
}

// WriteAt implements io.WriterAt. It writes data at the given offset
// without modifying the FD's current offset. Returns syscall.EBADF
// if the FD is read-only.
func (fd *FileDescriptor) WriteAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	fd.mu.Lock()
	if fd.closed {
		fd.mu.Unlock()
		return 0, os.ErrClosed
	}
	if fd.mode != fdWrite {
		fd.mu.Unlock()
		return 0, syscall.EBADF
	}

	total := 0
	for len(p) > 0 {
		targetIdx := int(off / BlockSize)
		blockOff := int(off % BlockSize)

		// If targeting a different block, flush the current one first.
		if targetIdx != fd.curIdx {
			if len(fd.curBlock) > 0 {
				fd.flushBlock(fd.curIdx, fd.curBlock)
			}
			fd.curBlock = make([]byte, 0, BlockSize)
			fd.curIdx = targetIdx
		}

		// Extend curBlock with zeros if the write offset is beyond
		// the current accumulator length.
		if blockOff > len(fd.curBlock) {
			fd.curBlock = append(fd.curBlock, make([]byte, blockOff-len(fd.curBlock))...)
		}

		space := int(BlockSize) - blockOff
		chunk := len(p)
		if chunk > space {
			chunk = space
		}

		// Overwrite or extend within the block.
		end := blockOff + chunk
		if end <= len(fd.curBlock) {
			copy(fd.curBlock[blockOff:end], p[:chunk])
		} else {
			// Partially overwrite existing, then append the rest.
			if blockOff < len(fd.curBlock) {
				copy(fd.curBlock[blockOff:], p[:len(fd.curBlock)-blockOff])
			}
			fd.curBlock = append(fd.curBlock, p[len(fd.curBlock)-blockOff:chunk]...)
		}

		p = p[chunk:]
		off += int64(chunk)
		total += chunk

		// Track file size growth.
		if off > fd.fileSize {
			fd.fileSize = off
		}

		if len(fd.curBlock) == int(BlockSize) {
			fd.flushBlock(fd.curIdx, fd.curBlock)
			fd.curBlock = make([]byte, 0, BlockSize)
			fd.curIdx++
		}
	}
	fd.mu.Unlock()

	return total, nil
}

// flushBlock submits a block for encrypt+upload in a background
// goroutine. The result is collected in the tokens map.
func (fd *FileDescriptor) flushBlock(index int, data []byte) {
	// Make a copy so the caller can reuse the slice.
	block := make([]byte, len(data))
	copy(block, data)

	fd.inflight.Add(1)
	go func() {
		defer fd.inflight.Done()

		apiIndex := index + 1 // Proton API uses 1-based block indices

		// Encrypt block with session key.
		plain := crypto.NewPlainMessage(block)
		encData, err := fd.sessionKey.Encrypt(plain)
		if err != nil {
			fd.setFirstErr(fmt.Errorf("encrypt block %d: %w", apiIndex, err))
			return
		}

		// Encrypted signature of the plaintext block.
		encSig, err := fd.addrKR.SignDetachedEncrypted(plain, fd.nodeKR)
		if err != nil {
			fd.setFirstErr(fmt.Errorf("sign block %d: %w", apiIndex, err))
			return
		}
		encSigStr, err := encSig.GetArmored()
		if err != nil {
			fd.setFirstErr(fmt.Errorf("armor block sig %d: %w", apiIndex, err))
			return
		}

		// SHA-256 of encrypted block for manifest.
		h := sha256.New()
		h.Write(encData)
		hash := h.Sum(nil)

		// Compute verification token.
		verifyToken := computeVerificationToken(fd.verifyCode, encData)

		// Request upload URL.
		req := proton.BlockUploadReq{
			AddressID:  fd.addressID,
			VolumeID:   fd.volumeID,
			LinkID:     fd.linkID,
			RevisionID: fd.revisionID,
			BlockList: []proton.BlockUploadInfo{{
				Index:        apiIndex,
				EncSignature: encSigStr,
				Verifier:     &proton.BlockVerifier{Token: base64.StdEncoding.EncodeToString(verifyToken)},
			}},
			ThumbnailList: []interface{}{},
		}

		ctx, cancel := context.WithTimeout(fd.ctx, 60*time.Second)
		defer cancel()

		links, err := fd.store.RequestUpload(ctx, req)
		if err != nil {
			fd.setFirstErr(fmt.Errorf("request upload block %d: %w", apiIndex, err))
			return
		}
		if len(links) == 0 {
			fd.setFirstErr(fmt.Errorf("no upload link for block %d", apiIndex))
			return
		}

		// Upload encrypted block.
		if err := fd.store.UploadBlock(ctx, fd.linkID, apiIndex, links[0].BareURL, links[0].Token, encData); err != nil {
			fd.setFirstErr(fmt.Errorf("upload block %d: %w", apiIndex, err))
			return
		}

		// Record result.
		fd.tokensMu.Lock()
		fd.tokens[index] = uploadedBlock{
			token:   links[0].Token,
			encHash: hash,
			rawSize: int64(len(block)),
		}
		fd.tokensMu.Unlock()
	}()
}

// setFirstErr records the first error encountered during background
// uploads. Subsequent errors are discarded.
func (fd *FileDescriptor) setFirstErr(err error) {
	fd.tokensMu.Lock()
	defer fd.tokensMu.Unlock()
	if fd.firstErr == nil {
		fd.firstErr = err
	}
}

// Sync flushes any partial block, waits for in-flight uploads, commits
// the current revision, and creates a new revision for subsequent
// writes. Returns syscall.EBADF if the FD is read-only.
func (fd *FileDescriptor) Sync() error {
	fd.mu.Lock()
	if fd.closed {
		fd.mu.Unlock()
		return os.ErrClosed
	}
	if fd.mode != fdWrite {
		fd.mu.Unlock()
		return syscall.EBADF
	}

	// Flush partial block.
	if len(fd.curBlock) > 0 {
		fd.flushBlock(fd.curIdx, fd.curBlock)
		fd.curBlock = fd.curBlock[:0]
	}
	fd.mu.Unlock()

	fd.inflight.Wait()

	fd.tokensMu.Lock()
	err := fd.firstErr
	nTokens := len(fd.tokens)
	fd.tokensMu.Unlock()

	if err != nil {
		return err
	}

	// No-op if nothing was written since last Sync.
	if nTokens == 0 {
		return nil
	}

	if err := fd.commitRevision(); err != nil {
		return err
	}

	// Create a new revision for subsequent writes.
	ctx, cancel := context.WithTimeout(fd.ctx, 30*time.Second)
	defer cancel()

	res, err := fd.session.Client.CreateRevision(ctx, fd.shareID, fd.linkID)
	if err != nil {
		return fmt.Errorf("fd.Sync: create new revision: %w", err)
	}

	// Fetch new verification code.
	vd, err := fd.session.Client.GetVerificationData(ctx, fd.shareID, fd.linkID, res.ID)
	if err != nil {
		return fmt.Errorf("fd.Sync: verification data: %w", err)
	}
	verifyCode, err := base64.StdEncoding.DecodeString(vd.VerificationCode)
	if err != nil {
		return fmt.Errorf("fd.Sync: decode verification code: %w", err)
	}

	fd.mu.Lock()
	fd.revisionID = res.ID
	fd.verifyCode = verifyCode
	fd.curIdx = 0
	fd.curBlock = make([]byte, 0, BlockSize)
	fd.mu.Unlock()

	fd.tokensMu.Lock()
	fd.tokens = make(map[int]uploadedBlock)
	fd.firstErr = nil
	fd.tokensMu.Unlock()

	return nil
}

// Truncate adjusts the file size. Full block discard is deferred —
// for now it only updates the fileSize field.
func (fd *FileDescriptor) Truncate(size int64) error {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	if fd.closed {
		return os.ErrClosed
	}
	if fd.mode != fdWrite {
		return syscall.EBADF
	}

	fd.fileSize = size
	return nil
}

// commitRevision builds the manifest, signs it, and calls
// UpdateRevision to commit the current revision as active.
func (fd *FileDescriptor) commitRevision() error {
	fd.tokensMu.Lock()
	nBlocks := len(fd.tokens)
	// Copy tokens under lock.
	tokensCopy := make(map[int]uploadedBlock, nBlocks)
	for k, v := range fd.tokens {
		tokensCopy[k] = v
	}
	fd.tokensMu.Unlock()

	if nBlocks == 0 {
		return nil
	}

	// Build ordered block token list and manifest hash.
	blockTokens := make([]proton.BlockToken, nBlocks)
	blockSizes := make([]int64, nBlocks)
	var manifestData []byte
	var totalSize int64
	for i := 0; i < nBlocks; i++ {
		ub, ok := tokensCopy[i]
		if !ok {
			return fmt.Errorf("fd.commitRevision: missing block %d in upload results", i)
		}
		blockTokens[i] = proton.BlockToken{
			Index: i + 1, // 1-based
			Token: ub.token,
		}
		blockSizes[i] = ub.rawSize
		manifestData = append(manifestData, ub.encHash...)
		totalSize += ub.rawSize
	}

	// Sign the manifest (concatenated SHA-256 hashes of encrypted blocks).
	manifestSig, err := fd.addrKR.SignDetached(crypto.NewPlainMessage(manifestData))
	if err != nil {
		return fmt.Errorf("fd.commitRevision: sign manifest: %w", err)
	}
	manifestSigStr, err := manifestSig.GetArmored()
	if err != nil {
		return fmt.Errorf("fd.commitRevision: armor manifest sig: %w", err)
	}

	// Build XAttr with file metadata.
	xAttrCommon := &proton.RevisionXAttrCommon{
		ModificationTime: time.Now().UTC().Format("2006-01-02T15:04:05-0700"),
		Size:             totalSize,
		BlockSizes:       blockSizes,
	}

	req := proton.UpdateRevisionReq{
		State:             proton.RevisionStateActive,
		BlockList:         blockTokens,
		ManifestSignature: manifestSigStr,
		SignatureAddress:  fd.sigAddr,
	}
	if err := req.SetEncXAttrString(fd.addrKR, fd.nodeKR, xAttrCommon); err != nil {
		return fmt.Errorf("fd.commitRevision: encrypt xattr: %w", err)
	}

	ctx, cancel := context.WithTimeout(fd.ctx, 30*time.Second)
	defer cancel()

	if err := fd.session.Client.UpdateRevision(ctx, fd.shareID, fd.linkID, fd.revisionID, req); err != nil {
		return fmt.Errorf("fd.commitRevision: %w", err)
	}

	return nil
}
