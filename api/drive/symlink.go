package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
)

// maxSymlinkTarget bounds the size of a symlink target read from content.
// A POSIX symlink target is at most PATH_MAX (4096) bytes; content larger
// than this is treated as a malformed symlink rather than a target.
const maxSymlinkTarget = 4096

// ErrNotSymlink indicates that ReadSymlinkTarget was called on a link that
// is not a symlink. Callers map this to EINVAL.
var ErrNotSymlink = errors.New("drive: not a symlink")

// ReadSymlinkTarget returns the verbatim target of a symlink by reading its
// content, bounded to maxSymlinkTarget (PATH_MAX) bytes. The target is stored
// as the file's single content block, so the returned string is exactly what
// was supplied at creation (relative, absolute, or dangling — all verbatim).
//
// It returns ErrNotSymlink if l is not a symlink, and a malformed error if
// the content exceeds maxSymlinkTarget bytes. Reading a dangling symlink
// succeeds — the target is not resolved or validated.
func (c *Client) ReadSymlinkTarget(ctx context.Context, l *Link) (string, error) {
	if !l.IsSymlink() {
		return "", ErrNotSymlink
	}

	fd, err := c.OpenFD(ctx, l)
	if err != nil {
		return "", fmt.Errorf("ReadSymlinkTarget %s: %w", l.LinkID(), err)
	}
	defer func() { _ = fd.Close() }()

	return readSymlinkTarget(fd, l.LinkID())
}

// readSymlinkTarget reads the verbatim symlink target from r, bounded to
// maxSymlinkTarget (PATH_MAX) bytes. Content up to and including the bound is
// returned verbatim; content larger than the bound is a malformed symlink and
// yields an error. This is the bounded-read tail of ReadSymlinkTarget, split
// out so the verbatim round-trip and the bound can be exercised directly over
// a decrypt-backed FileDescriptor (the same reader OpenFD produces) without
// the live-session content path.
func readSymlinkTarget(r io.ReaderAt, linkID string) (string, error) {
	// Read one byte past the bound so an over-long target is detectable
	// without reading unbounded content. ReadAt is single-pass: for a short
	// file it returns (n, io.EOF) once the content is exhausted rather than
	// looping on no-progress reads, so it cannot spin even when the reader's
	// reported size exceeds the decrypted plaintext (e.g. a symlink whose
	// encrypted on-disk size exceeds its target). io.ReadFull, by contrast,
	// has no no-progress guard and livelocks on a stream of (0, nil).
	buf := make([]byte, maxSymlinkTarget+1)
	n, err := r.ReadAt(buf, 0)
	// A target smaller than the buffer is the common case: ReadAt reports
	// io.EOF (and some readers io.ErrUnexpectedEOF) once the content is
	// exhausted, both of which mean the whole content fit within the bound.
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("ReadSymlinkTarget %s: %w", linkID, err)
	}
	if n > maxSymlinkTarget {
		return "", fmt.Errorf("ReadSymlinkTarget %s: content exceeds %d bytes: malformed symlink", linkID, maxSymlinkTarget)
	}

	return string(buf[:n]), nil
}

// CreateSymlink creates a symlink named `name` under `parent` whose target is
// the verbatim string `target`. It reuses the file upload/commit path: the
// target bytes are the single content block, committed as an active revision
// whose POSIX XAttr section carries Symlink=true. Common.Size falls out as the
// block size (= len(target)) from the normal manifest computation.
//
// It does NOT check whether the target exists — dangling symlinks are valid and
// creatable (matching symlink(2); required for staging rootfs images). The
// target is opaque (relative, absolute, in-mount, cross-share, or external) and
// stored verbatim with no normalization. An empty target is rejected with
// syscall.EINVAL (Requirement 1.4). On success it stats and returns the new
// link.
func (c *Client) CreateSymlink(ctx context.Context, share *Share, parent *Link, name, target string) (*Link, error) {
	// POSIX rejects an empty target, and a zero-length target would yield a
	// zero-block file with no content to read back.
	if target == "" {
		return nil, syscall.EINVAL
	}

	// Draft the file link, exactly as a normal file create does.
	fh, err := c.CreateFile(ctx, share, parent, name)
	if err != nil {
		return nil, fmt.Errorf("CreateSymlink %s: %w", name, err)
	}

	// Build the upload/commit params from the draft handle, marking the
	// revision as a symlink so commitRevisionFromTokens writes
	// PosixXAttr.Symlink = true into the POSIX section. This mirrors how
	// unixMode is threaded through uploadParams — no new commit path.
	params := uploadParams{
		sessionKey: fh.SessionKey,
		addrKR:     fh.AddrKR,
		nodeKR:     fh.NodeKR,
		verifyCode: fh.VerificationCode,
		addressID:  fh.AddressID,
		volumeID:   fh.VolumeID,
		shareID:    fh.ShareID,
		linkID:     fh.LinkID,
		revisionID: fh.RevisionID,
		sigAddr:    fh.SigAddr,
		symlink:    true,
		priorXAttr: fh.PriorXAttr,
	}

	// Upload the verbatim target as the single content block. The API block
	// index is 1-based (Proton convention).
	ub, err := encryptAndUploadBlock(ctx, params, c.blockStore, 1, []byte(target))
	if err != nil {
		return nil, fmt.Errorf("CreateSymlink %s: upload target block: %w", name, err)
	}

	// Commit the active revision from the single uploaded block. The token
	// map is 0-based (commitRevisionFromTokens iterates from index 0).
	tokens := map[int]uploadedBlock{0: ub}
	if err := commitRevisionFromTokens(ctx, c.Session, params, tokens, false); err != nil {
		return nil, fmt.Errorf("CreateSymlink %s: commit revision: %w", name, err)
	}

	// A new child now exists — drop the stale parent from the link table and
	// invalidate its cached children so the symlink is visible to Lookup/Readdir.
	c.deleteLink(parent.ProtonLink().LinkID)
	parent.InvalidateChildren()

	// Stat the newly created link so callers receive a valid *Link.
	link, err := c.StatLink(ctx, share, parent, fh.LinkID)
	if err != nil {
		return nil, fmt.Errorf("CreateSymlink %s: stat new link: %w", name, err)
	}
	return link, nil
}
