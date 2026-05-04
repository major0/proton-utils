package drive

import (
	"testing"

	"pgregory.net/rapid"
)

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"file.txt", "text/plain; charset=utf-8"},
		{"photo.jpg", "image/jpeg"},
		{"doc.pdf", "application/pdf"},
		{"archive.zip", "application/zip"},
		{"noext", "application/octet-stream"},
		{"", "application/octet-stream"},
		{".hidden", "application/octet-stream"},
	}
	for _, tt := range tests {
		got := detectMIMEType(tt.name)
		if got != tt.want {
			t.Errorf("detectMIMEType(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestDetectMIMEType_Property verifies that detectMIMEType never returns
// an empty string — it always falls back to application/octet-stream.
//
// **Property 9: MIME type detection**
// **Validates: Requirements 5.5 (drive-cp)**
func TestDetectMIMEType_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.String().Draw(t, "name")
		result := detectMIMEType(name)
		if result == "" {
			t.Fatalf("detectMIMEType(%q) returned empty string", name)
		}
	})
}
