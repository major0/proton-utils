package driveCmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

func TestMakeFileDst(t *testing.T) {
	tests := []struct {
		name    string
		dstBase *resolvedEndpoint
		relPath string
		check   func(t *testing.T, ep *resolvedEndpoint)
	}{
		{
			name: "local base with relative path",
			dstBase: &resolvedEndpoint{
				pathType:  PathLocal,
				localPath: "/tmp/dest",
			},
			relPath: "sub/file.txt",
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.pathType != PathLocal {
					t.Errorf("pathType = %d, want PathLocal", ep.pathType)
				}
				want := filepath.Join("/tmp/dest", "sub/file.txt")
				if ep.localPath != want {
					t.Errorf("localPath = %q, want %q", ep.localPath, want)
				}
			},
		},
		{
			name: "proton base with relative path",
			dstBase: &resolvedEndpoint{
				pathType: PathProton,
				raw:      "proton:///dest",
				link:     drive.NewTestLink(&proton.Link{LinkID: "parent-id", Type: proton.LinkTypeFolder}, nil, nil, nil, "dest"),
				share:    nil,
			},
			relPath: "sub/file.txt",
			check: func(t *testing.T, ep *resolvedEndpoint) {
				t.Helper()
				if ep.pathType != PathProton {
					t.Errorf("pathType = %d, want PathProton", ep.pathType)
				}
				if ep.raw != "sub/file.txt" {
					t.Errorf("raw = %q, want %q", ep.raw, "sub/file.txt")
				}
				if ep.link == nil {
					t.Error("link should not be nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := makeFileDst(tt.dstBase, tt.relPath)
			if ep == nil {
				t.Fatal("makeFileDst returned nil")
			}
			tt.check(t, ep)
		})
	}
}

func TestEnsureDestDir(t *testing.T) {
	t.Run("local creates directory tree", func(t *testing.T) {
		tmp := t.TempDir()
		dstBase := &resolvedEndpoint{
			pathType:  PathLocal,
			localPath: tmp,
		}

		ensureDestDir(context.Background(), nil, dstBase, "a/b/c")

		info, err := os.Stat(filepath.Join(tmp, "a", "b", "c"))
		if err != nil {
			t.Fatalf("directory not created: %v", err)
		}
		if !info.IsDir() {
			t.Error("expected directory")
		}
	})

	t.Run("local existing directory is no-op", func(t *testing.T) {
		tmp := t.TempDir()
		sub := filepath.Join(tmp, "existing")
		if err := os.Mkdir(sub, 0700); err != nil {
			t.Fatal(err)
		}
		dstBase := &resolvedEndpoint{
			pathType:  PathLocal,
			localPath: tmp,
		}

		// Should not error.
		ensureDestDir(context.Background(), nil, dstBase, "existing")

		info, err := os.Stat(sub)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Error("expected directory")
		}
	})
}

func TestExpandRecursive(t *testing.T) {
	// expandRecursive dispatches to expandLocalRecursive for PathLocal.
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	if err := os.Mkdir(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	srcInfo, _ := os.Stat(srcDir)

	src := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       srcDir,
		localPath: srcDir,
		localInfo: srcInfo,
	}
	dst := &resolvedEndpoint{
		pathType:  PathLocal,
		raw:       filepath.Join(tmp, "dst"),
		localPath: filepath.Join(tmp, "dst"),
	}

	ctx := context.Background()
	jobs, _, err := expandRecursive(ctx, nil, src, dst, cpOptions{})
	if err != nil {
		t.Fatalf("expandRecursive: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("got %d jobs, want 1", len(jobs))
	}
}

func TestPrintVolumeRows(t *testing.T) {
	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	maxSpace := int64(10 * 1024 * 1024 * 1024) // 10 GB
	volumes := []drive.Volume{
		{
			ProtonVolume: proton.Volume{
				VolumeID:        "vol-123456789012",
				State:           proton.VolumeStateActive,
				UsedSpace:       5 * 1024 * 1024 * 1024,
				MaxSpace:        &maxSpace,
				DownloadedBytes: 1024 * 1024,
				UploadedBytes:   2 * 1024 * 1024,
				Share:           proton.VolumeShare{ShareID: "share-1"},
			},
		},
	}

	nameIndex := map[string]string{"vol-123456789012": "My Drive"}
	shareIndex := map[string]proton.ShareMetadata{}

	printVolumeRows(volumes, nameIndex, shareIndex, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if output == "" {
		t.Fatal("expected output from printVolumeRows")
	}
	// Should contain the volume name.
	if !contains(output, "My Drive") {
		t.Errorf("output should contain 'My Drive': %q", output)
	}
	// Should contain "active" state.
	if !contains(output, "active") {
		t.Errorf("output should contain 'active': %q", output)
	}
}

func TestPrintVolumeRowsUnlimited(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	volumes := []drive.Volume{
		{
			ProtonVolume: proton.Volume{
				VolumeID:  "vol-abcdef123456",
				State:     proton.VolumeStateLocked,
				UsedSpace: 1024,
				MaxSpace:  nil, // unlimited
				Share:     proton.VolumeShare{ShareID: "share-2"},
			},
		},
	}

	nameIndex := map[string]string{}
	shareIndex := map[string]proton.ShareMetadata{
		"share-2": {ShareID: "share-2", Type: proton.ShareTypeMain},
	}

	printVolumeRows(volumes, nameIndex, shareIndex, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !contains(output, "unlimited") {
		t.Errorf("output should contain 'unlimited': %q", output)
	}
	if !contains(output, "locked") {
		t.Errorf("output should contain 'locked': %q", output)
	}
}

func TestPrintVolumeRowsFallbackLabel(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	volumes := []drive.Volume{
		{
			ProtonVolume: proton.Volume{
				VolumeID:  "vol-abcdef123456789",
				State:     proton.VolumeStateActive,
				UsedSpace: 0,
				MaxSpace:  nil,
				Share:     proton.VolumeShare{ShareID: "share-unknown"},
			},
		},
	}

	// No name, no share metadata → falls back to truncated volume ID.
	nameIndex := map[string]string{}
	shareIndex := map[string]proton.ShareMetadata{}

	printVolumeRows(volumes, nameIndex, shareIndex, nil)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	// Should contain full volume ID (no short ID map provided).
	if !contains(output, "vol-abcdef123456789") {
		t.Errorf("output should contain full volume ID: %q", output)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
