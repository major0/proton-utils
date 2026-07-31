package drive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// uploadParams holds the crypto and identity state needed for block
// upload and revision commit. All fields are unexported — this struct
// is internal to the drive package.
type uploadParams struct {
	sessionKey *crypto.SessionKey
	addrKR     *crypto.KeyRing
	nodeKR     *crypto.KeyRing
	verifyCode []byte
	addressID  string
	volumeID   string
	shareID    string
	linkID     string
	revisionID string
	sigAddr    string
	// unixMode carries the write-path mode. nil = not set (inherited POSIX
	// preserved); non-nil (even 0) = present, so an explicit chmod 0000
	// persists rather than being elided.
	unixMode *uint32

	// symlink marks the committed revision as a symlink by setting
	// PosixXAttr.Symlink = true in the POSIX section (the target lives in
	// the single content block). Threaded like unixMode; when both apply
	// they share one POSIX section.
	symlink bool

	// priorXAttr is the decoded XAttr of the file's previous active
	// revision, used only on the overwrite/new-revision path so a commit
	// preserves sibling sections (Media, Camera, Location, POSIX) written by
	// other Proton clients — a non-destructive read-modify-write. It is nil
	// for a brand-new file (CreateFile) or when the prior XAttr could not be
	// fetched/decrypted, in which case the commit starts from a fresh blob.
	priorXAttr *proton.RevisionXAttr
}

// encryptAndUploadBlock encrypts a plaintext block, signs it, computes
// the verification token, requests an upload URL, and uploads the block.
// Returns the upload result for manifest construction. Does not manage
// concurrency — callers own their goroutine/worker model.
//
// The ctx parameter should include a timeout (callers wrap with
// context.WithTimeout to preserve existing behavior). The apiIndex
// parameter is 1-based (Proton API convention) — callers pass
// blockIndex+1.
func encryptAndUploadBlock(ctx context.Context, p uploadParams, store blockStore, apiIndex int, data []byte) (uploadedBlock, error) {
	// Encrypt block with session key.
	plain := crypto.NewPlainMessage(data)
	encData, err := p.sessionKey.Encrypt(plain)
	if err != nil {
		return uploadedBlock{}, fmt.Errorf("encrypt block %d: %w", apiIndex, err)
	}

	// Encrypted signature of the plaintext block.
	encSig, err := p.addrKR.SignDetachedEncrypted(plain, p.nodeKR)
	if err != nil {
		return uploadedBlock{}, fmt.Errorf("sign block %d: %w", apiIndex, err)
	}
	encSigStr, err := encSig.GetArmored()
	if err != nil {
		return uploadedBlock{}, fmt.Errorf("armor block sig %d: %w", apiIndex, err)
	}

	// SHA-256 of encrypted block for manifest.
	h := sha256.New()
	h.Write(encData)
	hash := h.Sum(nil)

	// Compute verification token.
	verifyToken := computeVerificationToken(p.verifyCode, encData)

	// Request upload URL for this single block.
	req := proton.BlockUploadReq{
		AddressID:  p.addressID,
		VolumeID:   p.volumeID,
		LinkID:     p.linkID,
		RevisionID: p.revisionID,
		BlockList: []proton.BlockUploadInfo{{
			Index:        apiIndex,
			EncSignature: encSigStr,
			Verifier:     &proton.BlockVerifier{Token: base64.StdEncoding.EncodeToString(verifyToken)},
		}},
		ThumbnailList: []interface{}{},
	}

	links, err := store.RequestUpload(ctx, req)
	if err != nil {
		return uploadedBlock{}, fmt.Errorf("request upload block %d: %w", apiIndex, err)
	}
	if len(links) == 0 {
		return uploadedBlock{}, fmt.Errorf("no upload link for block %d", apiIndex)
	}

	// Upload encrypted block.
	if err := store.UploadBlock(ctx, p.linkID, apiIndex, links[0].BareURL, links[0].Token, encData); err != nil {
		return uploadedBlock{}, fmt.Errorf("upload block %d: %w", apiIndex, err)
	}

	return uploadedBlock{
		token:   links[0].Token,
		encHash: hash,
		rawSize: int64(len(data)),
	}, nil
}

// commitRevisionFromTokens builds the manifest, signs it, encrypts
// XAttr, and calls UpdateRevision to commit the revision as active.
//
// The ctx parameter should be the caller's base context — the function
// applies a 30-second timeout internally. fd.go passes fd.ctx;
// ProtonWriter passes context.Background() (ensuring commit completes
// even after pipeline context cancellation).
//
// totalSize is computed by summing rawSize from all tokens.
// ModificationTime uses time.Now().UTC() (matching current behavior).
func commitRevisionFromTokens(ctx context.Context, session *api.Session, p uploadParams, tokens map[int]uploadedBlock, allowEmpty bool) error {
	// When allowEmpty is set, nBlocks == 0 commits an empty, block-less
	// active revision (empty manifest, size 0) — this is how `touch`
	// produces a committed zero-byte file. When allowEmpty is false (the
	// copy-pipeline writer), a no-block close is a no-op.
	nBlocks := len(tokens)
	if nBlocks == 0 && !allowEmpty {
		return nil
	}

	// Build ordered block token list and manifest hash.
	blockTokens := make([]proton.BlockToken, nBlocks)
	blockSizes := make([]int64, nBlocks)
	var manifestData []byte
	var totalSize int64
	for i := 0; i < nBlocks; i++ {
		ub, ok := tokens[i]
		if !ok {
			return fmt.Errorf("commitRevision: missing block %d in upload results", i)
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
	manifestSig, err := p.addrKR.SignDetached(crypto.NewPlainMessage(manifestData))
	if err != nil {
		return fmt.Errorf("commitRevision: sign manifest: %w", err)
	}
	manifestSigStr, err := manifestSig.GetArmored()
	if err != nil {
		return fmt.Errorf("commitRevision: armor manifest sig: %w", err)
	}

	// Build the XAttr to commit. On the overwrite path p.priorXAttr carries
	// the previous revision's decoded blob, so sibling sections written by
	// other Proton clients (Media, Camera, Location, POSIX) survive the
	// commit; for a brand-new file it is nil and the blob starts fresh.
	modTime := time.Now().UTC().Format("2006-01-02T15:04:05-0700")
	xAttr := buildRevisionXAttr(p.priorXAttr, modTime, totalSize, blockSizes, p.unixMode, p.symlink)

	req := proton.UpdateRevisionReq{
		State:             proton.RevisionStateActive,
		BlockList:         blockTokens,
		ManifestSignature: manifestSigStr,
		SignatureAddress:  p.sigAddr,
	}
	if err := req.SetEncXAttrString(p.addrKR, p.nodeKR, xAttr); err != nil {
		return fmt.Errorf("commitRevision: encrypt xattr: %w", err)
	}

	commitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := session.Client.UpdateRevision(commitCtx, p.shareID, p.linkID, p.revisionID, req); err != nil {
		return fmt.Errorf("commitRevision: %w", err)
	}

	return nil
}

// buildRevisionXAttr assembles the RevisionXAttr for a new revision. This is
// a pure function (no network, no crypto) so the merge semantics are unit- and
// property-testable in isolation.
//
// When prior is non-nil (the overwrite/new-revision path) the returned blob
// starts from a copy of prior.Extra, so every sibling section written by other
// Proton clients (Media, Camera, Location) and any inherited POSIX section
// survive the commit — the non-destructive read-modify-write guarantee. Common
// is ALWAYS taken from the new revision's values and never inherited from
// prior: a stale Common would misreport size, mtime, or block sizes.
//
// The POSIX mode is built from presence, not value. unixMode == nil means "no
// explicit mode" (e.g. a plain upload that never called SetMode): the POSIX
// section carries no Mode field, so with symlink == false it marshals to "{}"
// and setPosixXAttr elides it, leaving any POSIX section inherited from prior
// untouched — a mode-less commit must NOT silently wipe a prior revision's
// POSIX metadata. A non-nil unixMode (even a pointer to 0) records the mode as
// present (masked to its lower 12 bits), so an explicit chmod 0000 persists as
// {"Mode":0} rather than being dropped.
//
// symlink marks the revision as a symlink (PosixXAttr.Symlink = true) — the
// verbatim target lives in the single content block. It is threaded alongside
// unixMode; when both apply they share one POSIX section.
func buildRevisionXAttr(prior *proton.RevisionXAttr, modTime string, size int64, blockSizes []int64, unixMode *uint32, symlink bool) *proton.RevisionXAttr {
	x := &proton.RevisionXAttr{}

	// Inherit sibling sections (and any prior POSIX) verbatim from the prior
	// revision so the commit is non-destructive. Copy into a fresh map so the
	// prior blob is never mutated.
	if prior != nil && len(prior.Extra) > 0 {
		x.Extra = make(map[string]json.RawMessage, len(prior.Extra))
		for k, v := range prior.Extra {
			x.Extra[k] = v
		}
	}

	// Common always reflects the new revision — never the stale prior Common.
	x.Common = proton.RevisionXAttrCommon{
		ModificationTime: modTime,
		Size:             size,
		BlockSizes:       blockSizes,
	}

	// Apply the POSIX section from presence. When unixMode is nil and symlink
	// is false, pfs marshals to "{}" so setPosixXAttr is a no-op that
	// preserves any inherited POSIX section (see doc comment). A present mode
	// (non-nil pointer, even to 0) and/or the symlink marker replaces it.
	var pfs PosixXAttr
	pfs.Symlink = symlink
	if unixMode != nil {
		m := *unixMode & 0o7777
		pfs.Mode = &m
	}
	setPosixXAttr(x, pfs)

	return x
}
