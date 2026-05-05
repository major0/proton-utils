package drive

// RenameLinkPayload is the request body for PUT /drive/shares/{shareID}/links/{linkID}/rename.
type RenameLinkPayload struct {
	Name               string `json:"Name"`               // Encrypted new name (PGP message)
	Hash               string `json:"Hash"`               // For root links: random 64 hex chars. For child links: HMAC lookup hash.
	NameSignatureEmail string `json:"NameSignatureEmail"` // Email of the signing address key
	OriginalHash       string `json:"OriginalHash"`       // The link's current Hash value — used for conflict detection
}
