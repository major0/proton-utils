package lumo

import (
	"errors"
	"sort"
)

// MasterKeyEntry is a single master key from the API.
type MasterKeyEntry struct {
	ID         string `json:"ID"`
	IsLatest   bool   `json:"IsLatest"`
	Version    int    `json:"Version"`
	CreateTime string `json:"CreateTime"`
	MasterKey  string `json:"MasterKey"` // PGP-armored
}

// Space is a space from the API.
type Space struct {
	ID            string         `json:"ID"`
	SpaceKey      string         `json:"SpaceKey"`            // base64 AES-KW wrapped
	SpaceTag      string         `json:"SpaceTag"`            // local ID / AEAD AD
	Encrypted     string         `json:"Encrypted,omitempty"` // base64 AES-GCM(SpacePriv JSON)
	CreateTime    string         `json:"CreateTime"`
	UpdateTime    string         `json:"UpdateTime,omitempty"`
	DeleteTime    string         `json:"DeleteTime,omitempty"`
	Conversations []Conversation `json:"Conversations,omitempty"` // embedded by GET /spaces
	Assets        []Asset        `json:"Assets,omitempty"`        // embedded by GET /spaces
}

// Asset is an attachment linked to a space.
type Asset struct {
	ID         string `json:"ID"`
	SpaceID    string `json:"SpaceID"`
	AssetTag   string `json:"AssetTag"`
	AssetType  int    `json:"AssetType,omitempty"`
	Encrypted  string `json:"Encrypted,omitempty"`
	CreateTime string `json:"CreateTime"`
	DeleteTime string `json:"DeleteTime,omitempty"`
}

// Conversation is a conversation from the API.
type Conversation struct {
	ID              string    `json:"ID"`
	SpaceID         string    `json:"SpaceID"`
	ConversationTag string    `json:"ConversationTag"`
	Encrypted       string    `json:"Encrypted,omitempty"`
	IsStarred       bool      `json:"IsStarred,omitempty"`
	CreateTime      string    `json:"CreateTime"`
	UpdateTime      string    `json:"UpdateTime,omitempty"`
	DeleteTime      string    `json:"DeleteTime,omitempty"`
	Messages        []Message `json:"Messages,omitempty"` // embedded by GET /conversations/:id
}

// Message is a message from the API.
type Message struct {
	ID             string `json:"ID"`
	ConversationID string `json:"ConversationID"`
	MessageTag     string `json:"MessageTag"`
	Role           int    `json:"Role"` // 1=user, 2=assistant
	Encrypted      string `json:"Encrypted,omitempty"`
	Status         int    `json:"Status,omitempty"` // 1=failed, 2=succeeded
	CreateTime     string `json:"CreateTime"`
	ParentID       string `json:"ParentID,omitempty"`
}

// SpacePriv is the encrypted metadata stored in Space.Encrypted.
// Uses camelCase JSON tags — the encrypted payload is defined by the
// web client, not the PHP backend.
type SpacePriv struct {
	IsProject           *bool              `json:"isProject,omitempty"`
	ProjectName         string             `json:"projectName,omitempty"`
	ProjectInstructions string             `json:"projectInstructions,omitempty"`
	ProjectIcon         string             `json:"projectIcon,omitempty"`
	LinkedDriveFolder   *LinkedDriveFolder `json:"linkedDriveFolder,omitempty"`
}

// LinkedDriveFolder references a Drive folder linked to a project space.
type LinkedDriveFolder struct {
	FolderID   string `json:"folderId"`
	FolderName string `json:"folderName"`
	FolderPath string `json:"folderPath"`
}

// --- Request types (PascalCase JSON) ---

// CreateSpaceReq is the POST body for creating a space.
type CreateSpaceReq struct {
	SpaceKey  string `json:"SpaceKey"`
	SpaceTag  string `json:"SpaceTag"`
	Encrypted string `json:"Encrypted,omitempty"`
}

// CreateConversationReq is the POST body for creating a conversation.
type CreateConversationReq struct {
	SpaceID         string `json:"SpaceID"`
	IsStarred       bool   `json:"IsStarred"`
	Encrypted       string `json:"Encrypted,omitempty"`
	ConversationTag string `json:"ConversationTag"`
}

// CreateMessageReq is the POST body for creating a message.
type CreateMessageReq struct {
	ConversationID string `json:"ConversationID"`
	MessageTag     string `json:"MessageTag"`
	Role           int    `json:"Role"`
	Status         int    `json:"Status,omitempty"`
	Encrypted      string `json:"Encrypted,omitempty"`
	ParentID       string `json:"ParentID,omitempty"`
}

// UpdateSpaceReq is the PUT body for updating a space.
type UpdateSpaceReq struct {
	Encrypted string `json:"Encrypted,omitempty"` // re-encrypted SpacePriv
}

// CreateMasterKeyReq is the POST body for creating a master key.
type CreateMasterKeyReq struct {
	MasterKey string `json:"MasterKey"` // PGP-armored
}

// --- Response envelopes ---

// ListMasterKeysResponse is the response from GET /api/lumo/v1/masterkeys.
type ListMasterKeysResponse struct {
	Code        int              `json:"Code"`
	Eligibility int              `json:"Eligibility"`
	MasterKeys  []MasterKeyEntry `json:"MasterKeys"`
}

// ListSpacesResponse is the response from GET /api/lumo/v1/spaces.
type ListSpacesResponse struct {
	Code   int     `json:"Code"`
	Spaces []Space `json:"Spaces"`
}

// GetSpaceResponse is the response from GET /api/lumo/v1/spaces/<id>.
type GetSpaceResponse struct {
	Code  int   `json:"Code"`
	Space Space `json:"Space"`
}

// GetConversationResponse is the response from GET /api/lumo/v1/conversations/<id>.
type GetConversationResponse struct {
	Code         int          `json:"Code"`
	Conversation Conversation `json:"Conversation"`
}

// GetMessageResponse is the response from GET /api/lumo/v1/messages/<id>.
type GetMessageResponse struct {
	Code    int     `json:"Code"`
	Message Message `json:"Message"`
}

// ListConversationsResponse is the response from GET /api/lumo/v1/spaces/{spaceID}/conversations.
type ListConversationsResponse struct {
	Code          int            `json:"Code"`
	Conversations []Conversation `json:"Conversations"`
}

// ListMessagesResponse is the response from GET /api/lumo/v1/conversations/{conversationID}/messages.
type ListMessagesResponse struct {
	Code     int       `json:"Code"`
	Messages []Message `json:"Messages"`
}

// --- Best-key selection ---

// SelectBestMasterKey returns the best master key from a non-empty list.
// Selection order: IsLatest=true first, then highest Version, then most
// recent CreateTime (lexicographic descending), then lowest ID.
func SelectBestMasterKey(keys []MasterKeyEntry) (MasterKeyEntry, error) {
	if len(keys) == 0 {
		return MasterKeyEntry{}, errors.New("lumo: no master keys available")
	}

	best := keys[0]
	for _, k := range keys[1:] {
		if betterKey(k, best) {
			best = k
		}
	}
	return best, nil
}

// betterKey reports whether a is a better key than b under the ordering:
// IsLatest=true > false, then higher Version, then later CreateTime
// (lexicographic), then lower ID.
func betterKey(a, b MasterKeyEntry) bool {
	if a.IsLatest != b.IsLatest {
		return a.IsLatest
	}
	if a.Version != b.Version {
		return a.Version > b.Version
	}
	if a.CreateTime != b.CreateTime {
		return a.CreateTime > b.CreateTime
	}
	return a.ID < b.ID
}

// SortMasterKeys sorts keys in best-first order (same ordering as
// SelectBestMasterKey). Useful for testing.
func SortMasterKeys(keys []MasterKeyEntry) {
	sort.Slice(keys, func(i, j int) bool {
		return betterKey(keys[i], keys[j])
	})
}
