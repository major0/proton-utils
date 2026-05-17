package drive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
func commitRevisionFromTokens(ctx context.Context, session *api.Session, p uploadParams, tokens map[int]uploadedBlock) error {
	nBlocks := len(tokens)
	if nBlocks == 0 {
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
		SignatureAddress:  p.sigAddr,
	}
	if err := req.SetEncXAttrString(p.addrKR, p.nodeKR, xAttrCommon); err != nil {
		return fmt.Errorf("commitRevision: encrypt xattr: %w", err)
	}

	commitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := session.Client.UpdateRevision(commitCtx, p.shareID, p.linkID, p.revisionID, req); err != nil {
		return fmt.Errorf("commitRevision: %w", err)
	}

	return nil
}
