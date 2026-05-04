package account

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/go-srp"
)

// authInfoReq is the request body for POST /core/v4/auth/info.
type authInfoReq struct {
	Username string `json:"Username"`
}

// srpAuthReq is the request body for POST /core/v4/auth.
type srpAuthReq struct {
	Username        string `json:"Username"`
	ClientEphemeral string `json:"ClientEphemeral"`
	ClientProof     string `json:"ClientProof"`
	SRPSession      string `json:"SRPSession"`
}

// CookieSRPAuth performs SRP authentication within a cookie session.
// It calls auth/info to get SRP parameters, computes proofs via go-srp,
// submits the proof to auth, and verifies the server's proof.
// Returns the Auth response containing UID, tokens, 2FA status, and password mode.
func CookieSRPAuth(ctx context.Context, cs *CookieSession, username string, password []byte) (*proton.Auth, error) {
	// Step 1: Get SRP parameters from the server.
	var info proton.AuthInfo
	if err := cs.DoJSON(ctx, "POST", "/core/v4/auth/info", authInfoReq{Username: username}, &info); err != nil {
		return nil, fmt.Errorf("%w: auth info: %w", ErrAuthFailed, err)
	}

	// Step 2: Compute SRP proofs using go-srp.
	srpAuth, err := srp.NewAuth(
		info.Version,
		username,
		password,
		info.Salt,
		info.Modulus,
		info.ServerEphemeral,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: srp: %w", ErrAuthFailed, err)
	}

	proofs, err := srpAuth.GenerateProofs(2048)
	if err != nil {
		return nil, fmt.Errorf("%w: srp proofs: %w", ErrAuthFailed, err)
	}

	// Step 3: Submit SRP proof to the server.
	req := srpAuthReq{
		Username:        username,
		ClientEphemeral: base64.StdEncoding.EncodeToString(proofs.ClientEphemeral),
		ClientProof:     base64.StdEncoding.EncodeToString(proofs.ClientProof),
		SRPSession:      info.SRPSession,
	}
	var auth proton.Auth
	if err := cs.DoJSON(ctx, "POST", "/core/v4/auth", req, &auth); err != nil {
		return nil, fmt.Errorf("%w: auth: %w", ErrAuthFailed, err)
	}

	// Step 4: Verify server proof.
	serverProof, err := base64.StdEncoding.DecodeString(auth.ServerProof)
	if err != nil {
		return nil, fmt.Errorf("%w: decode server proof: %w", ErrAuthFailed, err)
	}
	if !bytes.Equal(serverProof, proofs.ExpectedServerProof) {
		return nil, fmt.Errorf("%w: server proof mismatch", ErrAuthFailed)
	}

	return &auth, nil
}

// twoFAReq is the request body for POST /core/v4/auth/2fa.
type twoFAReq struct {
	TwoFactorCode string `json:"TwoFactorCode"`
}

// CookieTwoFA submits a TOTP 2FA code within a cookie session.
func CookieTwoFA(ctx context.Context, cs *CookieSession, code string) error {
	req := twoFAReq{TwoFactorCode: code}
	return cs.DoJSON(ctx, "POST", "/core/v4/auth/2fa", req, nil)
}
