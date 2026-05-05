package drive

// ShareURL represents a public URL associated with a share.
type ShareURL struct {
	ShareURLID               string `json:"ShareURLID"`
	ShareID                  string `json:"ShareID"`
	Token                    string `json:"Token"`
	PublicURL                string `json:"PublicUrl"`
	Password                 string `json:"Password"`
	SharePassphraseKeyPacket string `json:"SharePassphraseKeyPacket"`
	SharePasswordSalt        string `json:"SharePasswordSalt"`
	SRPVerifier              string `json:"SRPVerifier"`
	SRPModulusID             string `json:"SRPModulusID"`
	URLPasswordSalt          string `json:"UrlPasswordSalt"`
	CreatorEmail             string `json:"CreatorEmail"`
	Flags                    int    `json:"Flags"`
	Permissions              int    `json:"Permissions"`
	MaxAccesses              int    `json:"MaxAccesses"`
	NumAccesses              int    `json:"NumAccesses"`
	CreateTime               int64  `json:"CreateTime"`
	ExpirationTime           *int64 `json:"ExpirationTime"`
	LastAccessTime           int64  `json:"LastAccessTime"`
}

// ShareURLsResponse wraps the list-urls API response.
type ShareURLsResponse struct {
	Code      int        `json:"Code"`
	ShareURLs []ShareURL `json:"ShareURLs"`
}

// CreateShareURLPayload is the request body for POST /drive/shares/{shareID}/urls.
type CreateShareURLPayload struct {
	Flags                    int    `json:"Flags"`
	Permissions              int    `json:"Permissions"`
	MaxAccesses              int    `json:"MaxAccesses"`
	CreatorEmail             string `json:"CreatorEmail"`
	ExpirationDuration       *int   `json:"ExpirationDuration"`
	SharePassphraseKeyPacket string `json:"SharePassphraseKeyPacket"`
	SharePasswordSalt        string `json:"SharePasswordSalt"`
	Password                 string `json:"Password"`
	SRPModulusID             string `json:"SRPModulusID"`
	SRPVerifier              string `json:"SRPVerifier"`
	URLPasswordSalt          string `json:"UrlPasswordSalt"`
}

// UpdateShareURLPayload is the request body for PUT /drive/shares/{shareID}/urls/{urlID}.
// Includes both ExpirationDuration and ExpirationTime — the API accepts
// either (duration for relative, time for absolute). Create only accepts duration.
type UpdateShareURLPayload struct {
	Flags                    int    `json:"Flags"`
	Permissions              int    `json:"Permissions"`
	MaxAccesses              int    `json:"MaxAccesses"`
	ExpirationDuration       *int   `json:"ExpirationDuration"`
	ExpirationTime           *int64 `json:"ExpirationTime"`
	SharePassphraseKeyPacket string `json:"SharePassphraseKeyPacket"`
	SharePasswordSalt        string `json:"SharePasswordSalt"`
	Password                 string `json:"Password"`
	SRPModulusID             string `json:"SRPModulusID"`
	SRPVerifier              string `json:"SRPVerifier"`
	URLPasswordSalt          string `json:"UrlPasswordSalt"`
}
