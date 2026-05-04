package drive

import (
	"mime"
	"path/filepath"
)

// detectMIMEType returns the MIME type for a file name based on extension.
// Returns "application/octet-stream" for unknown extensions.
func detectMIMEType(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return "application/octet-stream"
	}
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return "application/octet-stream"
	}
	return mimeType
}
