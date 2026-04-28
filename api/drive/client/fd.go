package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/drive"
)

// fdMode distinguishes read-only from read-write file descriptors.
type fdMode int

const (
	fdRead fdMode = iota
	fdWrite
)

// FileDescriptor is an active handle to an open Proton Drive file.
// It tracks byte offset, owns crypto state for decrypt/encrypt, and
// delegates block I/O to a BlockStore. Implements io.Reader,
// io.ReaderAt, io.Seeker, and io.Closer.
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

	// Shared infrastructure
	store BlockStore

	// Link reference for Stat
	link *drive.Link
}

// Compile-time interface checks.
var (
	_ io.Reader   = (*FileDescriptor)(nil)
	_ io.ReaderAt = (*FileDescriptor)(nil)
	_ io.Seeker   = (*FileDescriptor)(nil)
	_ io.Closer   = (*FileDescriptor)(nil)
)

// OpenFD opens a Proton Drive file for reading and returns a
// FileDescriptor. It wraps Client.OpenFile to fetch revision metadata
// and derive the session key, then constructs a read-mode FD backed
// by a BlockStore.
func (c *Client) OpenFD(ctx context.Context, link *drive.Link) (*FileDescriptor, error) {
	fh, err := c.OpenFile(ctx, link)
	if err != nil {
		return nil, fmt.Errorf("drive.OpenFD: %w", err)
	}

	store := NewBlockStore(c.Session, nil, nil)

	return &FileDescriptor{
		linkID:     fh.LinkID,
		revisionID: fh.RevisionID,
		shareID:    fh.Share.ProtonShare().ShareID,
		sessionKey: fh.SessionKey,
		blocks:     fh.Blocks,
		fileSize:   fh.FileSize,
		mode:       fdRead,
		store:      store,
		link:       link,
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

// blockSize returns the plaintext size of block at index, computed from
// the file size and the standard block size.
func (fd *FileDescriptor) blockSize(index int) int64 {
	offset := int64(index) * drive.BlockSize
	remaining := fd.fileSize - offset
	if remaining <= 0 {
		return 0
	}
	if remaining > drive.BlockSize {
		return drive.BlockSize
	}
	return remaining
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
		blockIdx := int(offset / drive.BlockSize)
		blockOffset := offset % drive.BlockSize

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
		blockIdx := int(off / drive.BlockSize)
		blockOffset := off % drive.BlockSize

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

// readBlock fetches and decrypts a single block by index. The BlockStore
// handles caching and read-ahead internally.
func (fd *FileDescriptor) readBlock(blockIdx int) ([]byte, error) {
	if blockIdx >= len(fd.blocks) {
		return nil, fmt.Errorf("block index %d out of range (have %d blocks)", blockIdx, len(fd.blocks))
	}
	pb := fd.blocks[blockIdx]

	// BlockStore uses 1-based indices for the API.
	encrypted, err := fd.store.GetBlock(context.Background(), fd.linkID, blockIdx+1, pb.BareURL, pb.Token)
	if err != nil {
		return nil, err
	}

	return fd.decryptBlock(encrypted)
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

// Close implements io.Closer. It marks the FD as closed so subsequent
// operations return os.ErrClosed. Idempotent — second Close is a no-op.
func (fd *FileDescriptor) Close() error {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	fd.closed = true
	return nil
}

// Stat returns metadata from the underlying Link without API calls.
func (fd *FileDescriptor) Stat() drive.FileInfo {
	return fd.link.Stat()
}
