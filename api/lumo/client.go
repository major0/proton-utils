// Package lumo provides the Lumo API client.
package lumo

import (
	"context"
	"fmt"

	"github.com/major0/proton-utils/api"
)

// DefaultLumoBaseURL is the base URL for the Lumo API.
const DefaultLumoBaseURL = "https://lumo.proton.me/api"

// Client wraps an api.Session for Lumo API operations.
type Client struct {
	Session *api.Session
	BaseURL string // defaults to DefaultLumoBaseURL
	masterKeyFields
}

// NewClient constructs a Lumo client from an existing session.
func NewClient(session *api.Session) *Client {
	return &Client{Session: session, BaseURL: DefaultLumoBaseURL}
}

// url constructs a full URL from a relative path.
func (c *Client) url(path string) string {
	base := c.BaseURL
	if base == "" {
		base = DefaultLumoBaseURL
	}
	return base + path
}

// GenerateOpts configures a Generate request.
type GenerateOpts struct {
	// ChunkCallback is called for each decrypted response message.
	// Called synchronously from the SSE read loop.
	ChunkCallback func(GenerationResponseMessage)

	// Tools to enable for this request.
	Tools []ToolName

	// Targets for generation (defaults to ["message"]).
	Targets []GenerationTarget

	// LumoPubKey overrides the embedded production key (for testing).
	LumoPubKey string
}

// Generate sends an encrypted chat request and streams the decrypted
// response. Each message is delivered to opts.ChunkCallback. Returns
// nil on successful completion (done), or the appropriate sentinel
// error for terminal conditions.
func (c *Client) Generate(ctx context.Context, turns []Turn, opts GenerateOpts) error {
	key, err := GenerateRequestKey()
	if err != nil {
		return err
	}
	defer ZeroKey(key)

	requestID := GenerateRequestID()

	turns, err = EncryptTurns(turns, key, requestID)
	if err != nil {
		return fmt.Errorf("lumo: encrypt turns: %w", err)
	}

	pubKey := opts.LumoPubKey
	if pubKey == "" {
		pubKey = LumoPubKeyProd
	}

	encKey, err := EncryptRequestKey(key, pubKey)
	if err != nil {
		return err
	}

	targets := opts.Targets
	if targets == nil {
		targets = []GenerationTarget{TargetMessage}
	}

	var options *Options
	if len(opts.Tools) > 0 {
		options = &Options{Tools: opts.Tools}
	}

	req := ChatEndpointGenerationRequest{
		Prompt: GenerationRequest{
			Type:       "generation_request",
			Turns:      turns,
			Options:    options,
			Targets:    targets,
			RequestKey: encKey,
			RequestID:  requestID,
		},
	}

	body, err := c.Session.DoSSE(ctx, c.url("/ai/v1/chat"), req)
	if err != nil {
		return fmt.Errorf("lumo: chat request: %w", err)
	}
	defer func() { _ = body.Close() }()

	var proc StreamProcessor
	return proc.Process(ctx, body, func(msg GenerationResponseMessage) {
		if msg.Type == "token_data" && msg.Encrypted {
			if err := DecryptTokenData(&msg, key, requestID); err != nil {
				return
			}
		}
		if msg.Type == "image_data" && msg.Encrypted {
			if err := DecryptImageData(&msg, key, requestID); err != nil {
				return
			}
		}
		if opts.ChunkCallback != nil {
			opts.ChunkCallback(msg)
		}
	})
}
